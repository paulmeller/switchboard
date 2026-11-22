package decision

type Accuracy struct {
	Family   string   `json:"family"`
	Correct  int      `json:"correct"`
	N        int      `json:"n"`
	Accuracy *float64 `json:"accuracy"`
}
type Floor struct {
	Floor    float64  `json:"floor"`
	Correct  int      `json:"correct"`
	N        int      `json:"n"`
	Coverage float64  `json:"coverage"`
	Accuracy *float64 `json:"accuracy"`
}
type Report struct {
	SchemaVersion  int        `json:"schema_version"`
	Decisions      int        `json:"decisions"`
	Labelled       int        `json:"labelled_decisions"`
	JevLabelled    int        `json:"jev_labelled_decisions"`
	TooFewLabels   bool       `json:"too_few_labels"`
	Families       []Accuracy `json:"families"`
	Floors         []Floor    `json:"floors"`
	Recommendation *float64   `json:"recommended_floor"`
}

func ratio(correct, n int) *float64 {
	if n == 0 {
		return nil
	}
	v := float64(correct) / float64(n)
	return &v
}

func Tune(records []Decision) Report {
	r := Report{SchemaVersion: 1, Families: []Accuracy{{Family: "claude"}, {Family: "codex"}, {Family: "agy"}, {Family: "grok"}}, Floors: []Floor{}}
	labels := map[string]Label{}
	for _, d := range records {
		if d.Type == "decision" && d.Label != nil {
			labels[d.ID] = *d.Label
		}
		if d.Type == "feedback" {
			labels[d.DecisionID] = Label{Family: d.Family, Account: d.Account}
		}
	}
	for _, d := range records {
		if d.Type != "decision" {
			continue
		}
		r.Decisions++
		l := labels[d.ID]
		if l.Family == "" {
			continue
		}
		r.Labelled++
		for i := range r.Families {
			f := &r.Families[i]
			if f.Family == d.Family {
				f.N++
				if d.Family == l.Family {
					f.Correct++
				}
			}
		}
		if d.Source == "jev" && d.Probability != nil {
			r.JevLabelled++
		}
	}
	for i := range r.Families {
		f := &r.Families[i]
		f.Accuracy = ratio(f.Correct, f.N)
	}
	best := -1.0
	for _, floor := range []float64{.5, .6, .7, .8, .9} {
		f := Floor{Floor: floor}
		for _, d := range records {
			l := labels[d.ID]
			if d.Type != "decision" || l.Family == "" || d.Source != "jev" || d.Probability == nil || *d.Probability < floor {
				continue
			}
			f.N++
			if d.Family == l.Family {
				f.Correct++
			}
		}
		if r.JevLabelled > 0 {
			f.Coverage = float64(f.N) / float64(r.JevLabelled)
		}
		f.Accuracy = ratio(f.Correct, f.N)
		if f.Coverage >= .8 && f.Accuracy != nil && *f.Accuracy >= best {
			best = *f.Accuracy
			v := floor
			r.Recommendation = &v
		}
		r.Floors = append(r.Floors, f)
	}
	r.TooFewLabels = r.Labelled < 30
	return r
}
