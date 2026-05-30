package workflow

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func ensureDatabasePath(path string) error {
	if path == "" || path == ":memory:" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create workflow database directory: %w", err)
	}
	return nil
}

// GenerateNodeID returns a new random node ID with a "node-" prefix.
func GenerateNodeID() string { return generateID("node") }

func generateID(prefix string) string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(raw[:]))
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func mustParseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func rawJSONString(value json.RawMessage) string {
	if len(value) == 0 {
		return ""
	}
	return string(value)
}
