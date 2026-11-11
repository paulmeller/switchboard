package classifier

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"switchboard/internal/provider"
)

func TestJevWireAndFallback(t *testing.T) {
	for _, mode := range []string{"ok", "wrong-model", "low", "ineligible", "malformed", "missing-probabilities", "failure"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil || body["model"] != Model || body["state"] != "fake task" {
					t.Error("wire request")
				}
				questions := body["questions"].(map[string]any)
				if questions["difficulty"].(map[string]any)["type"] != "score" || questions["account"].(map[string]any)["type"] != "choice" {
					t.Error("typed questions")
				}
				if mode == "failure" {
					w.WriteHeader(500)
					return
				}
				if mode == "malformed" {
					w.Write([]byte(`{`))
					return
				}
				model := Model
				confidence := 0.95
				choice := "codex/work"
				if mode == "wrong-model" {
					model = "other"
				}
				if mode == "low" {
					confidence = 0.2
				}
				if mode == "ineligible" {
					choice = "claude/personal"
				}
				probs := map[string]float64{"codex/work": 1}
				if mode == "missing-probabilities" {
					probs = nil
				}
				json.NewEncoder(w).Encode(map[string]any{"model": model, "answers": map[string]any{"account": map[string]any{"type": "choice", "choice": choice, "confidence": confidence, "probabilities": probs}, "difficulty": map[string]any{"type": "score", "score": 1, "confidence": 0.99, "probabilities": map[string]float64{"0": 0.1, "1": 0.8, "2": 0.1}, "legend": map[string]string{"0": "routine", "1": "complex", "2": "advanced"}}}})
			})
			client := &http.Client{Transport: fakeTransport(func(r *http.Request) (*http.Response, error) {
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, r)
				return rec.Result(), nil
			})}
			j := Jev{Client: client, Endpoint: "https://fake.invalid"}
			eligible := []provider.Account{{Tool: "codex", Profile: "work"}}
			if _, ok := j.Choose(context.Background(), "fake task", eligible, 0.8); ok || calls != 0 {
				t.Fatal("no-key call")
			}
			j.Key = "fake-test-value"
			_, ok := j.Choose(context.Background(), "fake task", eligible, 0.8)
			if ok != (mode == "ok") || calls != 1 {
				t.Fatal("fallback")
			}
		})
	}
}

type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
