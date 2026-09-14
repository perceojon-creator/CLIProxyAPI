// Package session derives stable conversation identities and extracts hierarchical session relationships.
package session

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/tidwall/gjson"
)

// SessionInfo encapsulates incoming request details needed for session affinity and upstream reporting.
type SessionInfo struct {
	SessionID       string         `json:"session_id"`
	ParentSessionID string         `json:"parent_session_id,omitempty"`
	AgentName       string         `json:"agent_name,omitempty"`
	ClientType      string         `json:"client_type,omitempty"`
	CallerScope     string         `json:"caller_scope,omitempty"`
	AuthID          string         `json:"auth_id,omitempty"`
	Provider        string         `json:"provider,omitempty"`
	Model           string         `json:"model,omitempty"`
	IsFork          bool           `json:"is_fork,omitempty"`
	IsSubagent      bool           `json:"is_subagent,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

// SessionTreeInfo is an alias for SessionInfo for backward compatibility.
type SessionTreeInfo = SessionInfo

// ExtractSessionInfo extracts session hierarchy and client identification from request attributes.
// Priority matches selector.go:
//  1. X-Claude-Code-Session-Id & Claude Code metadata.user_id session
//  2. Session-Id / Session_id (Codex and compatible clients)
//  3. X-Http-Session-Id (Antigravity CLI)
//  4. Client and affinity headers (X-Session-ID, X-Session-Affinity, X-Slot-Session-Id, etc.)
//  5. Payload inspection (Gemini cachedContent, OpenAI thread, generic session_id, task_id, etc.)
//  6. Execution and LCP metadata fallback
func ExtractSessionInfo(headers http.Header, payload []byte, metadata map[string]any) (SessionInfo, bool) {
	var callerScope string
	if metadata != nil {
		if scope, ok := metadata[cliproxyexecutor.CallerScopeMetadataKey].(string); ok {
			callerScope = strings.TrimSpace(scope)
		}
	}

	root, reqRoot, hasNestedReq, parentCandidate := parsePayloadAndParentCandidate(payload)

	// 1. Claude session (headers & metadata.user_id)
	if s, ok := extractClaudeSession(headers, payload, root, reqRoot, hasNestedReq, parentCandidate); ok {
		s.CallerScope = callerScope
		return finalizeSessionInfo(s)
	}

	// 2. OpenAI / Codex CLI Headers & Turn Metadata
	if s, ok := extractCodexSession(headers, root, reqRoot, hasNestedReq, parentCandidate); ok {
		s.CallerScope = callerScope
		return finalizeSessionInfo(s)
	}

	// 3. Antigravity CLI Headers
	if s, ok := extractAntigravitySession(headers, parentCandidate); ok {
		s.CallerScope = callerScope
		return finalizeSessionInfo(s)
	}

	// 4. OpenCode, Pi, Task, Conversation, and ClientReq Headers
	if s, ok := extractHeaderSessions(headers, parentCandidate); ok {
		s.CallerScope = callerScope
		return finalizeSessionInfo(s)
	}

	// 5. Payload inspection (Gemini cachedContent, OpenAI thread, task, conversation, user_id)
	if s, ok := extractPayloadSession(payload, headers, root, reqRoot, hasNestedReq, parentCandidate); ok {
		s.CallerScope = callerScope
		return finalizeSessionInfo(s)
	}

	// 6. Metadata fallback (ExecutionSessionMetadataKey, LCPAffinitySessionIDMetadataKey)
	if s, ok := extractMetadataSession(metadata); ok {
		s.CallerScope = callerScope
		return finalizeSessionInfo(s)
	}

	return SessionInfo{}, false
}

func parsePayloadAndParentCandidate(payload []byte) (root gjson.Result, reqRoot gjson.Result, hasNestedReq bool, parentCandidate string) {
	if len(payload) == 0 {
		return
	}
	root = util.ParseGJSONBytesNoCopy(payload)
	reqRoot = root
	req := root.Get("request")
	hasNestedReq = req.Exists() && !root.Get("contents").Exists()
	if hasNestedReq {
		reqRoot = req
	}
	for _, p := range []string{
		// Standard session / thread parent keys
		"parent_session_id", "parentSessionId", "parentSessionID",
		"parent_thread_id", "parentThreadId", "parentThreadID",
		"forked_from_thread_id", "forked_from_id",
		"parent_conversation_id", "parentConversationId", "parentConversationID",
		// OpenCode / generic parent ID keys
		"parent_id", "parentId", "parentID",
		// Roo Code / Cline task delegation keys
		"parent_task_id", "parentTaskId", "parentTaskID",
		// OpenHands action tree keys
		"parent_action_id", "parentActionId", "parentActionID",
		// Pi session keys
		"parent_session", "parentSession",
		// Hermes subagent keys
		"parent_subagent_id", "parentSubagentId",
		// OpenClaw fork sources
		"forkSource.sessionId", "fork_source.session_id",
		"previousSessionId", "previous_session_id",
		// Metadata nested keys
		"metadata.parent_session_id", "metadata.parentSessionId", "metadata.parentSessionID",
		"metadata.parent_thread_id", "metadata.parentThreadId",
		"metadata.forked_from_thread_id", "metadata.forked_from_id",
		"metadata.parent_id", "metadata.parentId", "metadata.parentID",
		"metadata.parent_task_id", "metadata.parentTaskId", "metadata.parentTaskID",
		"metadata.parent_action_id", "metadata.parentActionId",
		"metadata.parent_subagent_id", "metadata.parentSubagentId",
		"metadata.parent_session", "metadata.parentSession",
		"metadata.parent_agent_id", "metadata.parentAgentId",
		"metadata.forkSource.sessionId", "metadata.previousSessionId",
		// Extra body nested keys
		"extra_body.parent_session_id", "extra_body.parentSessionId", "extra_body.parentSessionID",
		"extra_body.parent_thread_id", "extra_body.parentThreadId",
		"extra_body.forked_from_thread_id", "extra_body.forked_from_id",
		"extra_body.parent_id", "extra_body.parentId", "extra_body.parentID",
		"extra_body.parent_task_id", "extra_body.parentTaskId",
		"extra_body.parent_action_id", "extra_body.parentActionId",
		"extra_body.parent_subagent_id", "extra_body.parentSubagentId",
		"extra_body.parent_session", "extra_body.parentSession",
	} {
		if val := normalizedSessionCandidate(root.Get(p).String()); val != "" {
			parentCandidate = val
			break
		}
		if hasNestedReq {
			if val := normalizedSessionCandidate(reqRoot.Get(p).String()); val != "" {
				parentCandidate = val
				break
			}
		}
	}
	if parentCandidate == "" {
		parentCandidate = ClaudeMetadataParentSessionID(payload)
	}
	return
}

func isBodyForkCandidate(root, reqRoot gjson.Result, hasNestedReq bool) bool {
	if !root.Exists() {
		return false
	}
	for _, k := range []string{
		"forked_from_thread_id", "forked_from_id",
		"forkSource.sessionId", "fork_source.session_id",
		"previousSessionId", "previous_session_id",
		"metadata.forked_from_thread_id", "metadata.forked_from_id",
		"metadata.forkSource.sessionId", "metadata.previousSessionId",
		"extra_body.forked_from_thread_id", "extra_body.forked_from_id",
		"extra_body.forkSource.sessionId", "extra_body.previousSessionId",
	} {
		if val := normalizedSessionCandidate(root.Get(k).String()); val != "" {
			return true
		}
		if hasNestedReq {
			if val := normalizedSessionCandidate(reqRoot.Get(k).String()); val != "" {
				return true
			}
		}
	}
	return false
}

// BoundSessionIdentity bounds an identifier to <= 256 bytes safely,
// preserving uniqueness via SHA256 and guarding against splitting multibyte UTF-8 characters.
func BoundSessionIdentity(id string) string {
	if len(id) <= 256 {
		return id
	}
	sum := sha256.Sum256([]byte(id))
	hashHex := hex.EncodeToString(sum[:]) // 64 bytes
	prefixLen := 255 - 1 - len(hashHex)   // 190 bytes
	if prefixLen > len(id) {
		prefixLen = len(id)
	}
	prefix := id[:prefixLen]
	for !utf8.ValidString(prefix) && len(prefix) > 0 {
		prefix = prefix[:len(prefix)-1]
	}
	return prefix + "#" + hashHex
}

func finalizeSessionInfo(info SessionInfo) (SessionInfo, bool) {
	if info.SessionID == "" {
		return SessionInfo{}, false
	}
	info.SessionID = BoundSessionIdentity(info.SessionID)
	if info.ParentSessionID != "" {
		info.ParentSessionID = BoundSessionIdentity(info.ParentSessionID)
	}
	if info.AgentName == "" {
		info.AgentName = "main"
	}
	if info.ClientType == "" {
		info.ClientType = "generic"
	}
	// Self-referential loop protection
	if info.ParentSessionID == info.SessionID {
		info.ParentSessionID = ""
	}
	return info, true
}

// ExtractTreeInfo is an alias for ExtractSessionInfo for backward compatibility.
func ExtractTreeInfo(headers http.Header, payload []byte, metadata map[string]any) (SessionInfo, bool) {
	return ExtractSessionInfo(headers, payload, metadata)
}

func sessionHeaderValue(headers http.Header, name string) string {
	if headers == nil {
		return ""
	}
	if value := normalizedSessionCandidate(headers.Get(name)); value != "" {
		return value
	}
	for key, values := range headers {
		if !strings.EqualFold(key, name) {
			continue
		}
		for _, value := range values {
			if normalized := normalizedSessionCandidate(value); normalized != "" {
				return normalized
			}
		}
	}
	return ""
}

func normalizedSessionCandidate(raw string) string {
	return NormalizeExplicitID(raw)
}
