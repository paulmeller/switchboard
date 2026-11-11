package classifier

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"switchboard/internal/config"
)

func TestJevWireAndFallback(t *testing.T) {
	for _, gateway := range []bool{false, true} {
		for _, mode := range []string{"ok", "wrong-model", "low", "ineligible", "malformed", "missing-probabilities", "failure", "alias"} {
			t.Run(mode, func(t *testing.T) {
				calls := 0
				conf := config.Classifier{}
				if gateway {
					conf.Provider = "ai-gateway"
				}
				if conf.Validate() != nil {
					t.Fatal("defaults")
				}

				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.URL.String() != conf.BaseURL+"/v1/systemone" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer fake-test-value" || r.Header.Get("Content-Type") != "application/json" {
						t.Error("URL or headers")
					}

					var body map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil || body["model"] != conf.Model || body["state"] != "fake task" {
						t.Error("wire request")
					}
					questions := body["questions"].(map[string]any)
					if questions["difficulty"].(map[string]any)["type"] != "score" || questions["account"].(map[string]any)["type"] != "choice" {
						t.Error("typed questions")
					}
					criteria := questions["account"].(map[string]any)["criteria"].(map[string]any)
					if len(criteria) != 2 || criteria["claude"] == nil || criteria["codex"] == nil {
						t.Fatal("family options")
					}
					for _, value := range criteria {
						option := value.(map[string]any)
						if option["what"] == "" || option["not_for"] == "" || option["examples"] == nil {
							t.Fatal("account criteria")
						}
					}
					encoded, _ := json.Marshal(criteria)
					if criteria["codex"].(map[string]any)["what"] != "Custom implementation" {
						t.Fatal("override missing on wire")
					}
					if strings.Contains(string(encoded), "Coding account") || strings.Contains(string(encoded), "Five V") || strings.Contains(string(encoded), "BorderProof") || strings.Contains(string(encoded), "/work") || strings.Contains(string(encoded), "personal") {
						t.Fatal("generic criteria")
					}
					for _, value := range questions["difficulty"].(map[string]any)["criteria"].([]any) {
						level := value.(map[string]any)
						if level["what"] == "" || level["examples"] == nil {
							t.Fatal("difficulty criteria")
						}
					}
					if mode == "failure" {
						w.WriteHeader(500)
						return
					}
					if mode == "malformed" {
						w.Write([]byte(`{`))
						return
					}
					model := conf.Model
					if mode == "alias" {
						if gateway {
							model = Model
						} else {
							model = "typesafe-ai/" + Model
						}
					}
					confidence := 0.95
					choice := "codex"
					if mode == "wrong-model" {
						model = "other"
					}
					if mode == "low" {
						confidence = 0.2
					}
					if mode == "ineligible" {
						choice = "agy"
					}
					probs := map[string]float64{"codex": .8, "claude": .2}
					if mode == "missing-probabilities" {
						probs = nil
					}
					json.NewEncoder(w).Encode(map[string]any{"model": model, "answers": map[string]any{"account": map[string]any{"type": "choice", "choice": choice, "confidence": confidence, "probabilities": probs}, "difficulty": map[string]any{"type": "score", "score": 1, "confidence": 0.99, "probabilities": map[string]float64{"0": 0.1, "1": 0.8, "2": 0.1}, "legend": map[string]any{"0": map[string]any{"what": "small"}, "1": map[string]any{"what": "scoped"}, "2": map[string]any{"what": "design"}}}}})
				})
				client := &http.Client{Transport: fakeTransport(func(r *http.Request) (*http.Response, error) {
					rec := httptest.NewRecorder()
					handler.ServeHTTP(rec, r)
					return rec.Result(), nil
				})}
				criteria := config.DefaultCriteria()
				custom := criteria["codex"]
				custom.What = "Custom implementation"
				criteria["codex"] = custom
				j := Jev{Criteria: criteria, Client: client, Endpoint: conf.BaseURL + "/v1/systemone", Model: conf.Model}
				eligible := []string{"codex", "claude"}
				if result := j.Choose(context.Background(), "fake task", eligible, 0.8); result.OK || calls != 0 {
					t.Fatal("no-key call")
				}
				j.Key = "fake-test-value"
				result := j.Choose(context.Background(), "fake task", eligible, 0.8)
				if result.OK != (mode == "ok" || mode == "alias" || mode == "low") || calls != 1 {
					t.Fatal("fallback")
				}
			})
		}
	}
}

type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
