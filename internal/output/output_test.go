package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

type broken struct{}

func (broken) Write([]byte) (int, error) { return 0, errors.New("private writer failure") }
func TestWrite(t *testing.T) {
	var b bytes.Buffer
	if Write(&b, true, map[string]int{"schema_version": 1}, nil) != nil || !json.Valid(b.Bytes()) {
		t.Fatal("JSON output")
	}
	for _, machine := range []bool{false, true} {
		if Write(broken{}, machine, map[string]int{"n": 1}, []string{"row"}) == nil {
			t.Fatal("write failure ignored")
		}
	}
}
