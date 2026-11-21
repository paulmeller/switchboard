// Package output keeps reporting stdout separate from command diagnostics.
package output

import (
	"encoding/json"
	"fmt"
	"io"
)

func Write(w io.Writer, machine bool, value any, lines []string) error {
	if machine {
		return json.NewEncoder(w).Encode(value)
	}
	for _, line := range lines {
		if _, e := fmt.Fprintln(w, line); e != nil {
			return e
		}
	}
	return nil
}
