package classifier

import (
	"context"

	"switchboard/internal/config"
)

// Rules chooses the first configured preference still present in eligible.
type Rules struct{ Config config.Config }

func (r Rules) Choose(_ context.Context, task string, eligible []string, _ float64) Result {
	order, _ := r.Config.Order(task)
	for _, candidate := range order {
		for _, account := range eligible {
			if candidate == account {
				return Result{Family: candidate, OK: true}
			}
		}
	}
	return Result{}
}
