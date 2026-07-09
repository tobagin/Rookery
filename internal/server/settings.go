package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/rookerylabs/rookery/internal/appdb"
)

type SettingItem struct {
	Key             string `json:"key"`
	Label           string `json:"label"`
	Value           any    `json:"value"`
	Source          string `json:"source"`
	Locked          bool   `json:"locked"`
	Editable        bool   `json:"editable"`
	RestartRequired bool   `json:"restartRequired"`
}

func (s *Server) handleAlertTest(w http.ResponseWriter, r *http.Request) {
	if s.alertTest == nil {
		httpError(w, http.StatusServiceUnavailable, "failure alerts are not configured")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := s.alertTest(ctx); err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r, "alerts.test", "alerts", nil)
	writeJSON(w, http.StatusOK, map[string]any{"sent": true})
}

type SettingGroup struct {
	Name  string        `json:"name"`
	Items []SettingItem `json:"items"`
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"groups": s.effectiveSettings()})
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	if s.users == nil {
		httpError(w, http.StatusServiceUnavailable, "no app database configured")
		return
	}
	var req struct {
		Settings map[string]any `json:"settings"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	allowed := map[string]SettingItem{}
	for _, g := range s.settings {
		for _, item := range g.Items {
			if item.Editable && !item.Locked {
				allowed[item.Key] = item
			}
		}
	}
	for key, value := range req.Settings {
		if _, ok := allowed[key]; !ok {
			httpError(w, http.StatusBadRequest, key+" is read-only")
			return
		}
		if err := appdb.PutSetting(s.users.DB(), key, value); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	keys := make([]string, 0, len(req.Settings))
	for key := range req.Settings {
		keys = append(keys, key)
	}
	s.audit(r, "settings.update", "settings", map[string]any{"keys": keys})
	writeJSON(w, http.StatusOK, map[string]any{"updated": true, "restartRequired": true, "groups": s.effectiveSettings()})
}

func (s *Server) effectiveSettings() []SettingGroup {
	groups := make([]SettingGroup, len(s.settings))
	copy(groups, s.settings)
	for i := range groups {
		groups[i].Items = append([]SettingItem(nil), s.settings[i].Items...)
	}
	if s.users == nil {
		return groups
	}
	stored, err := appdb.GetSettings(s.users.DB())
	if err != nil {
		return groups
	}
	byKey := map[string]appdb.Setting{}
	for _, st := range stored {
		byKey[st.Key] = st
	}
	for gi := range groups {
		for ii := range groups[gi].Items {
			item := &groups[gi].Items[ii]
			st, ok := byKey[item.Key]
			if !ok || item.Locked {
				continue
			}
			var v any
			if err := json.Unmarshal(st.Value, &v); err == nil {
				item.Value = v
				item.Source = st.Source
				item.Locked = st.Locked
			}
		}
	}
	return groups
}
