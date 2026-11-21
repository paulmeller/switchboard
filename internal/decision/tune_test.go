package decision

import (
	"fmt"
	"math"
	"testing"
)

func TestTuneMath(t *testing.T) {
	records := []Decision{}
	// Ten labelled Jev decisions: eight >= .9, one .7, one .5.
	for i := range 10 {
		p := .95
		if i == 8 {
			p = .7
		}
		if i == 9 {
			p = .5
		}
		family, label := "codex", "codex"
		if i >= 8 {
			family = "claude"
			label = "codex"
		}
		id := fmt.Sprint(i)
		records = append(records, Decision{Record: Record{ID: id, Type: "decision", Family: family}, Source: "jev", Probability: &p, Label: &Label{Family: label}})
	}
	// A later label replaces the implicit label; a second feedback wins again.
	records = append(records, Decision{Record: Record{Type: "feedback", DecisionID: "0", Family: "claude"}}, Decision{Record: Record{Type: "feedback", DecisionID: "0", Family: "codex"}})
	// An unlabelled decision does not enter any denominator.
	records = append(records, Decision{Record: Record{ID: "unlabelled", Type: "decision", Family: "claude"}, Source: "rules"})
	r := Tune(records)
	if r.Decisions != 11 || r.Labelled != 10 || r.JevLabelled != 10 || !r.TooFewLabels {
		t.Fatal("counts", r)
	}
	if r.Families[0].N != 2 || r.Families[0].Correct != 0 || *r.Families[0].Accuracy != 0 || r.Families[1].N != 8 || *r.Families[1].Accuracy != 1 {
		t.Fatal("family accuracy", r.Families)
	}
	cover := []float64{1, .9, .9, .8, .8}
	n := []int{10, 9, 9, 8, 8}
	for i, f := range r.Floors {
		if f.Coverage != cover[i] || f.N != n[i] || f.Correct != 8 || math.Abs(*f.Accuracy-8/float64(n[i])) > 1e-12 {
			t.Fatal("floor math", f)
		}
	}
	if r.Recommendation == nil || *r.Recommendation != .9 {
		t.Fatal("tie must choose higher floor")
	}
	// Thirty labels removes the small-sample notice (still no guarantee).
	for i := range 20 {
		records = append(records, Decision{Record: Record{Type: "decision", ID: fmt.Sprint(i + 20), Family: "claude"}, Label: &Label{Family: "claude"}})
	}
	if Tune(records).TooFewLabels {
		t.Fatal("30 labels")
	}
}

func TestTuneNoCoverage(t *testing.T) {
	p := .4
	r := Tune([]Decision{{Record: Record{ID: "one", Type: "decision", Family: "claude"}, Source: "jev", Probability: &p, Label: &Label{Family: "claude"}}})
	if r.Recommendation != nil {
		t.Fatal("invented recommendation")
	}
	for _, f := range r.Floors {
		if f.Coverage != 0 || f.Accuracy != nil {
			t.Fatal("empty threshold accuracy")
		}
	}
	r = Tune(nil)
	if r.Decisions != 0 || r.Labelled != 0 || r.Recommendation != nil || !r.TooFewLabels {
		t.Fatal("empty report")
	}
	for _, f := range r.Families {
		if f.Accuracy != nil {
			t.Fatal("empty family accuracy")
		}
	}
}
