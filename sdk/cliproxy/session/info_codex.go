package session

import (
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

func extractCodexSession(
	headers http.Header,
	root, reqRoot gjson.Result,
	hasNestedReq bool,
	parentCandidate string,
) (SessionInfo, bool) {
	codexTurnMetaJSON := parseCodexTurnMeta(headers)
	sid, tid := resolveCodexIDs(headers, codexTurnMetaJSON, root, reqRoot, hasNestedReq)
	if sid == "" && tid == "" {
		return SessionInfo{}, false
	}

	parentThread := resolveCodexParentThread(headers, codexTurnMetaJSON)
	forkedFrom := resolveCodexForkedFrom(codexTurnMetaJSON, root, reqRoot, hasNestedReq)
	cleanAgentName := resolveCodexAgentName(codexTurnMetaJSON)
	subagentSignal := resolveCodexSubagentSignal(headers, codexTurnMetaJSON)

	// 1. Fork detection
	if forkedFrom != "" {
		return buildCodexForkSession(sid, tid, forkedFrom), true
	}

	// 2. Subagent detection (Multi-Agent v2)
	isSub := subagentSignal || (tid != "" && sid != "" && tid != sid) || (parentThread != "" && parentThread != tid && parentThread != sid)
	if isSub {
		return buildCodexSubagentSession(sid, tid, parentThread, parentCandidate, cleanAgentName), true
	}

	// 3. Normal interactive session
	return buildCodexNormalSession(sid, tid, parentThread, parentCandidate), true
}

func parseCodexTurnMeta(headers http.Header) gjson.Result {
	for k, v := range headers {
		if strings.EqualFold(k, "X-Codex-Turn-Metadata") && len(v) > 0 {
			raw := strings.TrimSpace(v[0])
			if raw != "" {
				return gjson.Parse(raw)
			}
		}
	}
	return gjson.Result{}
}

func resolveCodexIDs(
	headers http.Header,
	metaJSON gjson.Result,
	root, reqRoot gjson.Result,
	hasNestedReq bool,
) (sid, tid string) {
	sid = sessionHeaderValue(headers, "Session-Id")
	if sid == "" {
		sid = sessionHeaderValue(headers, "Session_id")
	}
	tid = sessionHeaderValue(headers, "Thread-Id")
	if tid == "" {
		tid = sessionHeaderValue(headers, "Thread_id")
	}

	if sid == "" && metaJSON.Exists() {
		sid = normalizedSessionCandidate(metaJSON.Get("session_id").String())
	}
	if tid == "" && metaJSON.Exists() {
		tid = normalizedSessionCandidate(metaJSON.Get("thread_id").String())
	}
	if tid == "" && sid != "" && root.Exists() {
		for _, path := range []string{"thread_id", "threadId", "metadata.thread_id"} {
			if tid = normalizedSessionCandidate(root.Get(path).String()); tid != "" {
				break
			}
			if hasNestedReq {
				if tid = normalizedSessionCandidate(reqRoot.Get(path).String()); tid != "" {
					break
				}
			}
		}
	}
	return
}

func resolveCodexParentThread(headers http.Header, metaJSON gjson.Result) string {
	parent := sessionHeaderValue(headers, "x-codex-parent-thread-id")
	if parent == "" {
		parent = sessionHeaderValue(headers, "X-Codex-Parent-Thread-Id")
	}
	if parent == "" && metaJSON.Exists() {
		parent = normalizedSessionCandidate(metaJSON.Get("parent_thread_id").String())
	}
	return parent
}

func resolveCodexForkedFrom(
	metaJSON gjson.Result,
	root, reqRoot gjson.Result,
	hasNestedReq bool,
) string {
	if metaJSON.Exists() {
		forked := normalizedSessionCandidate(metaJSON.Get("forked_from_thread_id").String())
		if forked == "" {
			forked = normalizedSessionCandidate(metaJSON.Get("forked_from_id").String())
		}
		if forked != "" {
			return forked
		}
	}
	if root.Exists() {
		for _, forkPath := range []string{
			"forked_from_thread_id", "forked_from_id",
			"metadata.forked_from_thread_id", "metadata.forked_from_id",
			"extra_body.forked_from_thread_id", "extra_body.forked_from_id",
		} {
			if val := normalizedSessionCandidate(root.Get(forkPath).String()); val != "" {
				return val
			}
			if hasNestedReq {
				if val := normalizedSessionCandidate(reqRoot.Get(forkPath).String()); val != "" {
					return val
				}
			}
		}
	}
	return ""
}

func resolveCodexAgentName(metaJSON gjson.Result) string {
	if !metaJSON.Exists() {
		return ""
	}
	rawName := metaJSON.Get("agent_name").String()
	rawName = strings.TrimPrefix(rawName, "/root/")
	rawName = strings.TrimPrefix(rawName, "/")
	rawName = strings.TrimSpace(rawName)
	rawName = normalizedSessionCandidate(rawName)
	if rawName != "" && rawName != "root" && rawName != "main" {
		return rawName
	}
	return ""
}

func resolveCodexSubagentSignal(headers http.Header, metaJSON gjson.Result) bool {
	subVal := sessionHeaderValue(headers, "X-Openai-Subagent")
	if subVal != "" && !strings.EqualFold(subVal, "false") && subVal != "0" {
		return true
	}
	if metaJSON.Exists() && metaJSON.Get("subagent_kind").String() == "thread_spawn" {
		return true
	}
	return false
}

func buildCodexForkSession(sid, tid, forkedFrom string) SessionInfo {
	forkSessionID := tid
	if forkSessionID == "" {
		forkSessionID = sid
	}
	if forkSessionID == forkedFrom && sid != "" && sid != forkedFrom {
		forkSessionID = sid
	}
	return SessionInfo{
		ClientType:      "codex",
		SessionID:       "codex:" + forkSessionID,
		ParentSessionID: "codex:" + forkedFrom,
		AgentName:       "main",
		IsFork:          true,
		IsSubagent:      false,
	}
}

func buildCodexSubagentSession(sid, tid, parentThread, parentCandidate, cleanAgentName string) SessionInfo {
	childSessionID := tid
	if childSessionID == "" {
		childSessionID = sid
	}
	parentSID := parentThread
	if parentSID == "" {
		parentSID = sid
	}
	info := SessionInfo{
		ClientType: "codex",
		IsSubagent: true,
	}
	if cleanAgentName != "" && sid != "" {
		info.SessionID = "codex:" + sid + ":agent:" + cleanAgentName
		info.AgentName = cleanAgentName
		if parentSID != "" {
			info.ParentSessionID = "codex:" + parentSID
		} else if parentCandidate != "" && parentCandidate != sid {
			info.ParentSessionID = "codex:" + parentCandidate
		}
	} else {
		info.SessionID = "codex:" + childSessionID
		if cleanAgentName != "" {
			info.AgentName = cleanAgentName
		} else {
			info.AgentName = "subagent"
		}
		if parentSID != "" && parentSID != childSessionID {
			info.ParentSessionID = "codex:" + parentSID
		} else if parentCandidate != "" && parentCandidate != childSessionID {
			info.ParentSessionID = "codex:" + parentCandidate
		}
	}
	return info
}

func buildCodexNormalSession(sid, tid, parentThread, parentCandidate string) SessionInfo {
	sessionID := sid
	if sessionID == "" {
		sessionID = tid
	}
	info := SessionInfo{
		ClientType: "codex",
		SessionID:  "codex:" + sessionID,
	}
	if parentThread != "" && parentThread != sessionID {
		info.ParentSessionID = "codex:" + parentThread
		info.AgentName = "subagent"
		info.IsSubagent = true
	} else if parentCandidate != "" && parentCandidate != sessionID {
		info.ParentSessionID = "codex:" + parentCandidate
		info.AgentName = "subagent"
		info.IsSubagent = true
	} else {
		info.AgentName = "main"
	}
	return info
}
