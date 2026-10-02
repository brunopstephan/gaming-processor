//go:build !integration && !e2e

package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestNewWritesJSONAtLevel(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelInfo)
	log.Debug("hidden")
	log.Info("visible", "correlationId", "c-1")
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("not one JSON line: %q (%v)", buf.String(), err)
	}
	if rec["msg"] != "visible" || rec["correlationId"] != "c-1" || rec["level"] != "INFO" {
		t.Fatalf("record = %v", rec)
	}
}
