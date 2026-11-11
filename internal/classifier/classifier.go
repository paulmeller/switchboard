// Package classifier is only used by task routing, never usage reporting.
package classifier

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"switchboard/internal/provider"
)

const Model = "jev-1.14.0"

type Classifier interface {
	Choose(context.Context, string, []provider.Account, float64) (provider.Account, bool)
}
type Jev struct {
	Key      string
	Client   *http.Client
	Endpoint string
}
type answer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Confidence    *float64           `json:"confidence"`
	Score         *float64           `json:"score"`
	Probabilities map[string]float64 `json:"probabilities"`
	Legend        map[string]string  `json:"legend"`
}

func (j Jev) Choose(ctx context.Context, task string, eligible []provider.Account, floor float64) (provider.Account, bool) {
	none := provider.Account{}
	if j.Key == "" || len(eligible) == 0 {
		return none, false
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	criteria := map[string]string{}
	for _, a := range eligible {
		criteria[a.ID()] = "Coding account " + a.ID()
	}
	body := map[string]any{"model": Model, "state": task, "questions": map[string]any{"account": map[string]any{"type": "choice", "instructions": "Select a coding account for this task", "criteria": criteria}, "difficulty": map[string]any{"type": "score", "instructions": "Rate coding difficulty", "criteria": []string{"routine", "complex", "advanced"}}}}
	b, _ := json.Marshal(body)
	url := j.Endpoint
	if url == "" {
		url = "https://api.typesafe.ai/v1/systemone"
	}
	req, e := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
	if e != nil {
		return none, false
	}
	req.Header.Set("Authorization", "Bearer "+j.Key)
	req.Header.Set("Content-Type", "application/json")
	client := j.Client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, e := client.Do(req)
	if e != nil {
		return none, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return none, false
	}
	var result struct {
		Model   string            `json:"model"`
		Answers map[string]answer `json:"answers"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 65536))
	if decoder.Decode(&result) != nil || result.Model != Model {
		return none, false
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return none, false
	}
	a := result.Answers["account"]
	s := result.Answers["difficulty"]
	if !distribution(a.Probabilities, criteria) || !distribution(s.Probabilities, map[string]string{"0": "routine", "1": "complex", "2": "advanced"}) || len(s.Legend) != 3 {
		return none, false
	}
	for _, key := range []string{"0", "1", "2"} {
		if s.Legend[key] == "" {
			return none, false
		}
	}
	if a.Type != "choice" || a.Confidence == nil || *a.Confidence < floor || *a.Confidence > 1 || s.Type != "score" || s.Score == nil || *s.Score < 0 || *s.Score > 2 || s.Confidence == nil || *s.Confidence < floor || *s.Confidence > 1 {
		return none, false
	}
	for _, candidate := range eligible {
		if candidate.ID() == a.Choice {
			return candidate, true
		}
	}
	return none, false
}

func distribution(p map[string]float64, allowed map[string]string) bool {
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
