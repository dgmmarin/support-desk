//go:build e2e

package e2e

import (
	"encoding/json"
	"testing"

	"tourdesk/internal/pipeline"
)

// unwrap decodes a stage output message: an Envelope whose Payload holds the
// stage's event (stages carry the case identity forward — NFR-R-01).
func unwrap[T any](t *testing.T, data []byte) T {
	t.Helper()
	var env pipeline.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	var v T
	if err := json.Unmarshal(env.Payload, &v); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return v
}
