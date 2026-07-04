package alert

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "gopher://x", "telegram://tokenonly", "just-a-topic"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q): want error", bad)
		}
	}
}

func TestSendAllKinds(t *testing.T) {
	type hit struct {
		path, body, title, ctype string
	}
	var hits []hit
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		hits = append(hits, hit{r.URL.Path, string(b), r.Header.Get("Title"), r.Header.Get("Content-Type")})
	}))
	defer ts.Close()

	// ntfy:// forces https, so exercise ntfy semantics via a direct target;
	// parse the other kinds from spec against the test server.
	n, err := Parse(ts.URL + "/hook")
	if err != nil {
		t.Fatal(err)
	}
	n.targets = append(n.targets, target{kind: "ntfy", url: ts.URL + "/topic"})
	n.targets = append(n.targets, target{kind: "telegram", url: ts.URL + "/botTOKEN/sendMessage?chat_id=42"})

	n.Send(context.Background(), "unit failed", "jellyfin died")
	if len(hits) != 3 {
		t.Fatalf("got %d deliveries, want 3", len(hits))
	}

	var wh map[string]string
	if err := json.Unmarshal([]byte(hits[0].body), &wh); err != nil || wh["title"] != "unit failed" || wh["message"] != "jellyfin died" {
		t.Errorf("webhook = %+v (%v)", hits[0], err)
	}
	if hits[1].title != "unit failed" || hits[1].body != "jellyfin died" {
		t.Errorf("ntfy = %+v", hits[1])
	}
	var tg map[string]string
	if err := json.Unmarshal([]byte(hits[2].body), &tg); err != nil || !strings.Contains(tg["text"], "jellyfin died") {
		t.Errorf("telegram = %+v (%v)", hits[2], err)
	}
}

func TestParseNtfyAndTelegramURLs(t *testing.T) {
	n, err := Parse("ntfy://ntfy.sh/alerts, telegram://123:abc@-100200, https://example.com/hook")
	if err != nil {
		t.Fatal(err)
	}
	if len(n.targets) != 3 {
		t.Fatalf("targets = %+v", n.targets)
	}
	if n.targets[0].url != "https://ntfy.sh/alerts" {
		t.Errorf("ntfy url = %q", n.targets[0].url)
	}
	if n.targets[1].url != "https://api.telegram.org/bot123:abc/sendMessage?chat_id=-100200" {
		t.Errorf("telegram url = %q", n.targets[1].url)
	}
}
