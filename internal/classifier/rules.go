package classifier

import (
	"context"

	"switchboard/internal/config"
	"switchboard/internal/provider"
)

// Rules chooses the first configured preference still present in eligible.
type Rules struct{ Config config.Config }

func (r Rules) Choose(_ context.Context, task string, eligible []provider.Account, _ float64) (provider.Account, float64, bool) {
	order, _ := r.Config.Order(task)
	for _, candidate := range order {
		for _, account := range eligible {
			if candidate == account {
				return candidate, 0, true
			}
		}
	}
	return provider.Account{}, 0, false
}
