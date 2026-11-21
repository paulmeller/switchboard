// Package classifier is only used by task routing, never usage reporting.
package classifier

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"switchboard/internal/config"
)

const Model = "jev-1.14.0"

type Classifier interface {
	Choose(context.Context, string, []string, float64) Result
}
type Result struct {
	Family               string   `json:"family"`
	Probability          *float64 `json:"probability"`
	Confidence           *float64 `json:"confidence"`
	Difficulty           *float64 `json:"difficulty"`
	DifficultyConfidence *float64 `json:"difficulty_confidence"`
	OK                   bool     `json:"-"`
}

type Jev struct {
	Criteria map[string]config.Criteria
	Key      string
	Client   *http.Client
	Endpoint string
	Model    string
}
type answer struct {
	Type          string                     `json:"type"`
	Choice        string                     `json:"choice"`
	Confidence    *float64                   `json:"confidence"`
	Score         *float64                   `json:"score"`
	Probabilities map[string]float64         `json:"probabilities"`
	Legend        map[string]json.RawMessage `json:"legend"`
}

func (j Jev) Choose(ctx context.Context, task string, eligible []string, floor float64) Result {
	none := Result{}
	if j.Key == "" || len(eligible) == 0 {
		return none
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	model := j.Model
	if model == "" {
		model = Model
	}
	criteria := map[string]config.Criteria{}
	defaults := j.Criteria
	if defaults == nil {
		defaults = config.DefaultCriteria()
	}
	for _, a := range eligible {
		criteria[a] = defaults[a]
	}
	body := map[string]any{"model": model, "state": task, "questions": map[string]any{"account": map[string]any{"type": "choice", "instructions": "Select a tool family for this task", "criteria": criteria}, "difficulty": map[string]any{"type": "score", "instructions": "Rate coding difficulty", "criteria": []map[string]any{
		{"what": "Small local change a competent engineer can finish quickly", "examples": []string{"typo in README", "rename a variable", "obvious off-by-one"}},
		{"what": "Feature, bug or refactor with clear scope requiring judgment", "examples": []string{"fix a flaky test", "add a CLI command", "refactor a scraper module"}},
		{"what": "Open-ended design, architecture or cross-system high-risk change", "examples": []string{"design a multi-tenant auth model", "plan a data migration", "choose a storage architecture"}},
	}}}}
	b, _ := json.Marshal(body)
	url := j.Endpoint
	if url == "" {
		url = "https://api.typesafe.ai/v1/systemone"
	}
	req, e := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
	if e != nil {
		return none
	}
	req.Header.Set("Authorization", "Bearer "+j.Key)
	req.Header.Set("Content-Type", "application/json")
	client := j.Client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, e := client.Do(req)
	if e != nil {
		return none
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return none
	}
	var result struct {
		Model   string            `json:"model"`
		Answers map[string]answer `json:"answers"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 65536))
	if decoder.Decode(&result) != nil || strings.TrimPrefix(result.Model, "typesafe-ai/") != strings.TrimPrefix(model, "typesafe-ai/") {
		return none
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return none
	}
	a := result.Answers["account"]
	s := result.Answers["difficulty"]
	if !distribution(a.Probabilities, criteria) || !distribution(s.Probabilities, map[string]string{"0": "routine", "1": "complex", "2": "advanced"}) || len(s.Legend) != 3 {
		return none
	}
	for _, key := range []string{"0", "1", "2"} {
		if len(s.Legend[key]) == 0 || string(s.Legend[key]) == "null" {
			return none
		}
	}
	if a.Type != "choice" || !validOptional(a.Confidence) || s.Type != "score" || s.Score == nil || *s.Score < 0 || *s.Score > 2 || !validOptional(s.Confidence) {
		return none
	}

	for _, candidate := range eligible {
		if candidate == a.Choice {
			p := a.Probabilities[a.Choice]
			return Result{Family: candidate, Probability: &p, Confidence: a.Confidence, Difficulty: s.Score, DifficultyConfidence: s.Confidence, OK: true}
		}
	}
	return none
}

func validOptional(v *float64) bool { return v == nil || (*v >= 0 && *v <= 1) }

func distribution[T any](p map[string]float64, allowed map[string]T) bool {
	if len(p) != len(allowed) {
		return false
	}
	sum := 0.0
	for key, value := range p {
		if _, ok := allowed[key]; !ok || value < 0 || value > 1 {
			return false
		}
		sum += value
	}
	return sum >= 0.99 && sum <= 1.01
}
