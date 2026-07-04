// Package alert delivers unit-failure notifications to ntfy topics,
// Telegram chats, and generic JSON webhooks. Destinations are declared as
// URLs so one flag configures any mix:
//
//	ntfy://ntfy.sh/my-topic          POST message with a Title header
//	telegram://BOT_TOKEN@CHAT_ID     Bot API sendMessage
//	https://example.com/hook         POST {"title": ..., "message": ...}
package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Notifier fans one message out to every configured destination.
type Notifier struct {
	targets []target
	client  *http.Client
}

type target struct {
	kind string // ntfy, telegram, webhook
	url  string // ready-to-POST URL
}

// Parse builds a Notifier from a comma-separated destination list.
func Parse(spec string) (*Notifier, error) {
	n := &Notifier{client: &http.Client{Timeout: 10 * time.Second}}
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		switch {
		case strings.HasPrefix(entry, "ntfy://"):
			n.targets = append(n.targets, target{kind: "ntfy", url: "https://" + strings.TrimPrefix(entry, "ntfy://")})
		case strings.HasPrefix(entry, "telegram://"):
			token, chat, ok := strings.Cut(strings.TrimPrefix(entry, "telegram://"), "@")
			if !ok || token == "" || chat == "" {
				return nil, fmt.Errorf("alert destination %q must be telegram://BOT_TOKEN@CHAT_ID", entry)
			}
			n.targets = append(n.targets, target{kind: "telegram",
				url: "https://api.telegram.org/bot" + token + "/sendMessage?chat_id=" + url.QueryEscape(chat)})
		case strings.HasPrefix(entry, "http://"), strings.HasPrefix(entry, "https://"):
			n.targets = append(n.targets, target{kind: "webhook", url: entry})
		default:
			return nil, fmt.Errorf("alert destination %q: use ntfy://, telegram://, or http(s)://", entry)
		}
	}
	if len(n.targets) == 0 {
		return nil, fmt.Errorf("no alert destinations in %q", spec)
	}
	return n, nil
}

// Send delivers title+message to every destination. Failures are logged,
// not returned — one dead webhook must not stop the others, and the caller
// (a background watcher) has nobody to report to anyway.
func (n *Notifier) Send(ctx context.Context, title, message string) {
	for _, t := range n.targets {
		if err := n.send(ctx, t, title, message); err != nil {
			log.Printf("alert %s: %v", t.kind, err)
		}
	}
}

func (n *Notifier) send(ctx context.Context, t target, title, message string) error {
	var req *http.Request
	var err error
	switch t.kind {
	case "ntfy":
		req, err = http.NewRequestWithContext(ctx, "POST", t.url, strings.NewReader(message))
		if err == nil {
			req.Header.Set("Title", title)
		}
	case "telegram":
		body, _ := json.Marshal(map[string]string{"text": title + "\n" + message})
		req, err = http.NewRequestWithContext(ctx, "POST", t.url, bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	default: // webhook
		body, _ := json.Marshal(map[string]string{"title": title, "message": message})
		req, err = http.NewRequestWithContext(ctx, "POST", t.url, bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	if err != nil {
		return err
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s: status %d", t.url, resp.StatusCode)
	}
	return nil
}
