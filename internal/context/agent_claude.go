package context

import (
	"bytes"
	"encoding/json"
	"strings"
)

// NewClaudeCodeAdapter returns the adapter for the "claude-code" provider. It
// parses Claude Code stream-json output: one JSON object per line with a
// top-level "type" (system / assistant / result / error, plus permission
// control shapes), interleaved with arbitrary terminal noise that is skipped
// silently. Mapping (B10 freeze):
//
//	system+subtype=init            → lifecycle_started
//	result (is_error=false)        → attention_done
//	result (is_error=true), error  → attention_error
//	permission-ish types           → attention_needed
//
// Everything else (assistant deltas, tool chatter) emits nothing — the core
// state model never sees provider payloads, only these typed events.
func NewClaudeCodeAdapter(cfg AdapterConfig) (*Adapter, error) {
	return newAdapter("claude-code", cfg, parseClaudeLine)
}

func parseClaudeLine(line []byte) (AgentEventKind, string, string, bool) {
	obj, ok := decodeJSONObject(line)
	if !ok {
		return "", "", "", false
	}
	typ, _ := obj["type"].(string)
	switch {
	case typ == "system":
		if sub, _ := obj["subtype"].(string); sub == "init" {
			return AgentLifecycleStarted, "claude-code session started", jsonString(obj, "model"), true
		}
		return "", "", "", false
	case typ == "result":
		detail := jsonString(obj, "result")
		if isErr, _ := obj["is_error"].(bool); isErr {
			return AgentAttentionError, "claude-code task failed", detail, true
		}
		return AgentAttentionDone, "claude-code task finished", detail, true
	case typ == "error":
		detail := jsonString(obj, "message")
		if detail == "" {
			detail = jsonString(obj, "error")
		}
		return AgentAttentionError, "claude-code error", detail, true
	case strings.Contains(typ, "permission") || typ == "control_request":
		detail := jsonString(obj, "tool_name")
		if detail == "" {
			detail = typ
		}
		return AgentAttentionNeeded, "claude-code needs permission", detail, true
	}
	return "", "", "", false
}

// decodeJSONObject strictly decodes one line as a single JSON object,
// rejecting non-JSON noise and trailing garbage cheaply.
func decodeJSONObject(line []byte) (map[string]any, bool) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false
	}
	var obj map[string]any
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return nil, false
	}
	return obj, true
}

// jsonString reads a top-level string field, tolerating absence and non-string
// values.
func jsonString(obj map[string]any, key string) string {
	s, _ := obj[key].(string)
	return s
}
