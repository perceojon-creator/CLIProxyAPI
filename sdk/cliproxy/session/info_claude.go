package session

import (
	"net/http"

	"github.com/tidwall/gjson"
)

func extractClaudeSession(
	headers http.Header,
	payload []byte,
	root, reqRoot gjson.Result,
	hasNestedReq bool,
	parentCandidate string,
) (SessionInfo, bool) {
	// 1. Anthropic / Claude Code Headers
	if sid := sessionHeaderValue(headers, "X-Claude-Code-Session-Id"); sid != "" {
		agentID := resolveClaudeAgentID(headers, payload, root, reqRoot, hasNestedReq)
		parentAgentID := resolveClaudeParentAgentID(headers, root, reqRoot, hasNestedReq)
		return buildClaudeSessionInfo(sid, "", agentID, parentAgentID, parentCandidate), true
	}

	// 2. Claude Code metadata.user_id in payload (outranks generic headers)
	if len(payload) > 0 {
		if sid, parentSID, agentID := ClaudeMetadataIdentities(payload); sid != "" {
			if agentID == "" {
				agentID = resolveClaudeAgentID(headers, payload, root, reqRoot, hasNestedReq)
			}
			parentAgentID := resolveClaudeParentAgentID(headers, root, reqRoot, hasNestedReq)
			return buildClaudeSessionInfo(sid, parentSID, agentID, parentAgentID, parentCandidate), true
		}
	}

	return SessionInfo{}, false
}

func resolveClaudeAgentID(
	headers http.Header,
	payload []byte,
	root, reqRoot gjson.Result,
	hasNestedReq bool,
) string {
	if agentID := sessionHeaderValue(headers, "X-Claude-Code-Agent-Id"); agentID != "" {
		return agentID
	}
	if root.Exists() {
		for _, path := range []string{"metadata.agent_id", "metadata.subagent_id"} {
			if id := normalizedSessionCandidate(root.Get(path).String()); id != "" {
				return id
			}
			if hasNestedReq {
				if id := normalizedSessionCandidate(reqRoot.Get(path).String()); id != "" {
					return id
				}
			}
		}
	}
	_, _, agentID := ClaudeMetadataIdentities(payload)
	return agentID
}

func resolveClaudeParentAgentID(
	headers http.Header,
	root, reqRoot gjson.Result,
	hasNestedReq bool,
) string {
	if parentAgentID := sessionHeaderValue(headers, "X-Claude-Code-Parent-Agent-Id"); parentAgentID != "" {
		return parentAgentID
	}
	if !root.Exists() {
		return ""
	}
	for _, path := range []string{"metadata.parent_agent_id", "metadata.parentAgentId"} {
		if id := normalizedSessionCandidate(root.Get(path).String()); id != "" {
			return id
		}
		if hasNestedReq {
			if id := normalizedSessionCandidate(reqRoot.Get(path).String()); id != "" {
				return id
			}
		}
	}
	return ""
}

func buildClaudeSessionInfo(
	sid, parentSID, agentID, parentAgentID, parentCandidate string,
) SessionInfo {
	info := SessionInfo{
		ClientType: "claude",
	}

	if agentID != "" && agentID != "main" {
		info.AgentName = agentID
		info.SessionID = "claude:" + sid + ":agent:" + agentID
		info.ParentSessionID = "claude:" + sid
		if parentAgentID != "" && parentAgentID != "main" && parentAgentID != agentID {
			info.ParentSessionID = "claude:" + sid + ":agent:" + parentAgentID
		} else if parentSID != "" && parentSID != sid {
			info.ParentSessionID = "claude:" + parentSID
		} else if parentCandidate != "" && parentCandidate != sid {
			info.ParentSessionID = "claude:" + parentCandidate
		}
		return info
	}

	info.SessionID = "claude:" + sid
	if parentSID != "" && parentSID != sid {
		info.ParentSessionID = "claude:" + parentSID
		info.AgentName = "subagent"
	} else if parentCandidate != "" && parentCandidate != sid {
		info.ParentSessionID = "claude:" + parentCandidate
		info.AgentName = "subagent"
	} else {
		info.AgentName = "main"
	}
	return info
}
