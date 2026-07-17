package context

import "strings"

// NewCodexAdapter returns the adapter for the "codex" provider. It parses
// Codex CLI JSONL output: one JSON object per line of shape
// {"id": "...", "msg": {"type": "...", ...}}, interleaved terminal noise
// skipped silently. Mapping (B10 freeze):
//
//	session_configured   → lifecycle_started
//	task_started         → session_status
//	agent_message        → session_status
//	*_approval_request   → attention_needed
//	task_complete        → attention_done
//	error                → attention_error
//	shutdown_complete    → lifecycle_exited
func NewCodexAdapter(cfg AdapterConfig) (*Adapter, error) {
	return newAdapter("codex", cfg, parseCodexLine)
}

func parseCodexLine(line []byte) (AgentEventKind, string, string, bool) {
	obj, ok := decodeJSONObject(line)
	if !ok {
		return "", "", "", false
	}
	msg, _ := obj["msg"].(map[string]any)
	if msg == nil {
		return "", "", "", false
	}
	typ, _ := msg["type"].(string)
	switch {
	case typ == "session_configured":
		return AgentLifecycleStarted, "codex session started", jsonString(msg, "model"), true
	case typ == "task_started":
		return AgentSessionStatus, "codex task started", "", true
	case typ == "agent_message":
		return AgentSessionStatus, "codex message", jsonString(msg, "message"), true
	case typ == "task_complete":
		return AgentAttentionDone, "codex task finished", jsonString(msg, "last_agent_message"), true
	case typ == "error":
		return AgentAttentionError, "codex error", jsonString(msg, "message"), true
	case typ == "shutdown_complete":
		return AgentLifecycleExited, "codex exited", "", true
	case strings.HasSuffix(typ, "approval_request"):
		return AgentAttentionNeeded, "codex needs approval", typ, true
	}
	return "", "", "", false
}
