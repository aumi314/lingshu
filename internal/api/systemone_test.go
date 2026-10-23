package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aumi314/lingshu/internal/store"
)

func TestSystemOneTemplatesAndChannelPersistence(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	var templates []ChannelTemplate
	if code, body := a.do("GET", "/api/admin/channel-templates", nil, &templates); code != 200 {
		t.Fatalf("templates: %d %s", code, body)
	}
	want := map[string]string{"typesafe": "https://api.typesafe.ai/v1", "nace": "https://console.nace.ai/v1"}
	for _, tpl := range templates {
		if base, ok := want[tpl.ID]; ok {
			if tpl.BaseURLSystemOne != base || !tpl.HasModelList || tpl.BaseURLChat != "" || tpl.BaseURLMessages != "" || tpl.BaseURLResponses != "" {
				t.Errorf("invalid template: %+v", tpl)
			}
			delete(want, tpl.ID)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing templates: %v", want)
	}
	input := map[string]any{"name": "decision", "template": "typesafe", "api_key": "test-systemone-secret", "base_url_systemone": " https://api.typesafe.ai/v1/ ", "enabled": true, "group_ids": []int64{1}}
	var saved struct{ ID int64 }
	if code, body := a.do("POST", "/api/admin/channels", input, &saved); code != 200 {
		t.Fatalf("save: %d %s", code, body)
	}
	var channels []*store.Channel
	if code, body := a.do("GET", "/api/admin/channels", nil, &channels); code != 200 || strings.Contains(body, "test-systemone-secret") {
		t.Fatalf("list status or credential exposure: %d", code)
	}
	if len(channels) != 1 || channels[0].BaseURLSystemOne != "https://api.typesafe.ai/v1" || channels[0].Template != "typesafe" {
		t.Fatalf("channel did not round-trip: %+v", channels)
	}
	input["api_key"] = ""
	input["base_url_systemone"] = "https://console.nace.ai/v1"
	input["template"] = "nace"
	if code, body := a.do("PUT", "/api/admin/channels/1", input, nil); code != 200 {
		t.Fatalf("update: %d %s", code, body)
	}
	ch, err := e.store.ChannelSecret(context.Background(), saved.ID)
	if err != nil || ch.BaseURLSystemOne != "https://console.nace.ai/v1" || ch.APIKey != "test-systemone-secret" {
		t.Fatal("update or secret retention failed")
	}
	input["base_url_systemone"] = "ftp://invalid"
	if code, _ := a.do("PUT", "/api/admin/channels/1", input, nil); code != 400 {
		t.Errorf("invalid System One URL accepted: %d", code)
	}
}

func TestSystemOneAdminFetchAndProbe(t *testing.T) {
	e := newEnv(t)
	a := e.login("admin")
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer saved-systemone-key" {
			t.Error("stored credential not used")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			w.Write([]byte(`{"models":[{"name":"drex-v2.0"}]}`))
		case "/v1/systemone":
			w.Write([]byte(`{"model":"drex-v2.0","answers":{"is_urgent":{"type":"noul","noul":0.7353}},"usage":{"input_tokens":26,"output_tokens":24}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer up.Close()
	_, err := e.store.Pool.Exec(context.Background(), `INSERT INTO channels (id, name, api_key, base_url_systemone) VALUES (1, 'decision', 'saved-systemone-key', $1)`, up.URL+"/v1")
	if err != nil {
		t.Fatal(err)
	}
	draft := map[string]any{"id": 1, "base_url_systemone": up.URL + "/v1"}
	var models []string
	if code, body := a.do("POST", "/api/admin/channels/fetch-models", draft, &models); code != 200 || len(models) != 1 || models[0] != "drex-v2.0" {
		t.Fatalf("fetch: %d %s", code, body)
	}
	draft["targets"] = []map[string]string{{"protocol": "systemone", "model": "drex-v2.0"}}
	var results []struct {
		OK            bool  `json:"ok"`
		UsageReported bool  `json:"usage_reported"`
		InputTokens   int64 `json:"input_tokens"`
	}
	if code, body := a.do("POST", "/api/admin/channels/probe", draft, &results); code != 200 || len(results) != 1 || !results[0].OK || !results[0].UsageReported || results[0].InputTokens != 26 {
		t.Fatalf("probe: %d %s", code, body)
	}
	if code, _ := e.login("bob").do("POST", "/api/admin/channels/fetch-models", draft, nil); code != 403 {
		t.Errorf("user could call admin fetch: %d", code)
	}
}
