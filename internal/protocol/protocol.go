package protocol

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

const Version = "0.1.3"

type Command struct {
	CommandID      string         `json:"command_id"`
	Action         string         `json:"action"`
	Payload        map[string]any `json:"payload"`
	Deadline       string         `json:"deadline,omitempty"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
}

func NewCommand(action string, payload map[string]any) Command {
	id := NewID("cmd")
	return Command{
		CommandID:      id,
		Action:         action,
		Payload:        payload,
		IdempotencyKey: id,
	}
}

func NewID(prefix string) string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(buf)
}

func NowISO() string {
	return time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
}

func Encode(v any) ([]byte, error) {
	return json.Marshal(v)
}
