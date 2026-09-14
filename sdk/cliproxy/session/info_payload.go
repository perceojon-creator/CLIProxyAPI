package session

import (
	"net/http"

	"github.com/tidwall/gjson"
)

func extractPayloadSession(
	payload []byte,
	headers http.Header,
	root, reqRoot gjson.Result,
	hasNestedReq bool,
	parentCandidate string,
) (SessionInfo, bool) {
	if len(payload) == 0 || !root.Exists() {
		return SessionInfo{}, false
	}

	// 1. Gemini context caching
	if info, ok := extractPayloadGemini(root, reqRoot, hasNestedReq, parentCandidate); ok {
		return info, true
	}

	// 2. OpenAI thread in payload
	if info, ok := extractPayloadOpenAIThread(root, reqRoot, hasNestedReq, parentCandidate); ok {
		return info, true
	}

	// 3. Generic session in payload
	if info, ok := extractPayloadGenericSession(headers, root, reqRoot, hasNestedReq, parentCandidate); ok {
		return info, true
	}

	// 4. Task / Action in payload (Roo Code, Cline, OpenHands)
	if info, ok := extractPayloadTask(root, reqRoot, hasNestedReq, parentCandidate); ok {
		return info, true
	}

	// 5. Prompt cache key & Conversation object
	if info, ok := extractPayloadPromptCacheAndConv(root, reqRoot, hasNestedReq, parentCandidate); ok {
		return info, true
	}

	// 6. Plain metadata.user_id & Legacy conversation string paths
	if info, ok := extractPayloadUserIDAndLegacyConv(root, reqRoot, hasNestedReq, parentCandidate); ok {
		return info, true
	}

	return SessionInfo{}, false
}

func extractPayloadGemini(
	root, reqRoot gjson.Result,
	hasNestedReq bool,
	parentCandidate string,
) (SessionInfo, bool) {
	for _, cachePath := range []string{"cachedContent", "cached_content"} {
		cacheID := normalizedSessionCandidate(root.Get(cachePath).String())
		if cacheID == "" && hasNestedReq {
			cacheID = normalizedSessionCandidate(reqRoot.Get(cachePath).String())
		}
		if cacheID != "" {
			info := SessionInfo{
				ClientType: "gemini",
				SessionID:  "geminicache:" + cacheID,
				AgentName:  "main",
			}
			if parentCandidate != "" && parentCandidate != cacheID {
				info.ParentSessionID = "geminicache:" + parentCandidate
				info.AgentName = "subagent"
			}
			return info, true
		}
	}
	return SessionInfo{}, false
}

func extractPayloadOpenAIThread(
	root, reqRoot gjson.Result,
	hasNestedReq bool,
	parentCandidate string,
) (SessionInfo, bool) {
	for _, threadPath := range []string{"thread_id", "threadId", "metadata.thread_id"} {
		tid := normalizedSessionCandidate(root.Get(threadPath).String())
		if tid == "" && hasNestedReq {
			tid = normalizedSessionCandidate(reqRoot.Get(threadPath).String())
		}
		if tid != "" {
			info := SessionInfo{
				ClientType: "openai-thread",
				SessionID:  "thread:" + tid,
				AgentName:  "main",
			}
			if parentCandidate != "" && parentCandidate != tid {
				info.ParentSessionID = "thread:" + parentCandidate
				if isBodyForkCandidate(root, reqRoot, hasNestedReq) {
					info.IsFork = true
					info.IsSubagent = false
				} else {
					info.AgentName = "subagent"
					info.IsSubagent = true
				}
			}
			return info, true
		}
	}
	return SessionInfo{}, false
}

func resolvePayloadAgentID(
	headers http.Header,
	root, reqRoot gjson.Result,
	hasNestedReq bool,
) string {
	for _, p := range []string{"metadata.agent_id", "metadata.subagent_id"} {
		if id := normalizedSessionCandidate(root.Get(p).String()); id != "" {
			return id
		}
	}
	for _, h := range []string{"X-Claude-Code-Agent-Id", "x-agent-id"} {
		if id := sessionHeaderValue(headers, h); id != "" {
			return id
		}
	}
	if hasNestedReq {
		for _, p := range []string{"metadata.agent_id", "metadata.subagent_id"} {
			if id := normalizedSessionCandidate(reqRoot.Get(p).String()); id != "" {
				return id
			}
		}
	}
	return ""
}

func extractPayloadGenericSession(
	headers http.Header,
	root, reqRoot gjson.Result,
	hasNestedReq bool,
	parentCandidate string,
) (SessionInfo, bool) {
	agentID := resolvePayloadAgentID(headers, root, reqRoot, hasNestedReq)

	for _, path := range []string{
		"session_id", "sessionId", "sessionID",
		"child_session_id", "childSessionId",
		"metadata.session_id", "metadata.sessionId", "metadata.sessionID",
		"metadata.child_session_id",
		"extra_body.session_id", "extra_body.sessionId", "extra_body.sessionID",
	} {
		sid := normalizedSessionCandidate(root.Get(path).String())
		if sid == "" && hasNestedReq {
			sid = normalizedSessionCandidate(reqRoot.Get(path).String())
		}
		if sid == "" {
			continue
		}

		info := SessionInfo{
			ClientType: "generic",
		}
		if agentID != "" && agentID != "main" {
			info.SessionID = "session:" + sid + ":agent:" + agentID
			info.ParentSessionID = "session:" + sid
			if parentCandidate != "" && parentCandidate != sid {
				info.ParentSessionID = "session:" + parentCandidate
			}
			info.AgentName = agentID
		} else {
			info.SessionID = "session:" + sid
			if parentCandidate != "" && parentCandidate != sid {
				info.ParentSessionID = "session:" + parentCandidate
				if isBodyForkCandidate(root, reqRoot, hasNestedReq) {
					info.IsFork = true
					info.IsSubagent = false
					info.AgentName = "main"
				} else {
					info.AgentName = "subagent"
					info.IsSubagent = true
				}
			} else {
				info.AgentName = "main"
			}
		}
		return info, true
	}
	return SessionInfo{}, false
}

func extractPayloadTask(
	root, reqRoot gjson.Result,
	hasNestedReq bool,
	parentCandidate string,
) (SessionInfo, bool) {
	for _, path := range []string{
		"task_id", "taskId", "taskID",
		"action_id", "actionId", "actionID",
		"metadata.task_id", "metadata.taskId", "metadata.taskID",
		"metadata.action_id", "metadata.actionId", "metadata.actionID",
		"extra_body.task_id", "extra_body.taskId", "extra_body.taskID",
	} {
		tid := normalizedSessionCandidate(root.Get(path).String())
		if tid == "" && hasNestedReq {
			tid = normalizedSessionCandidate(reqRoot.Get(path).String())
		}
		if tid != "" {
			info := SessionInfo{
				ClientType: "task",
				SessionID:  "task:" + tid,
			}
			if parentCandidate != "" && parentCandidate != tid {
				info.ParentSessionID = "task:" + parentCandidate
				if isBodyForkCandidate(root, reqRoot, hasNestedReq) {
					info.IsFork = true
					info.IsSubagent = false
					info.AgentName = "main"
				} else {
					info.AgentName = "subagent"
					info.IsSubagent = true
				}
			} else {
				info.AgentName = "main"
			}
			return info, true
		}
	}
	return SessionInfo{}, false
}

func extractPayloadPromptCacheAndConv(
	root, reqRoot gjson.Result,
	hasNestedReq bool,
	parentCandidate string,
) (SessionInfo, bool) {
	var conversationID string
	conversation := root.Get("conversation")
	if !conversation.Exists() && hasNestedReq {
		conversation = reqRoot.Get("conversation")
	}
	if sid := normalizedSessionCandidate(conversation.Get("id").String()); sid != "" {
		conversationID = "conv:" + sid
	} else if conversation.Type == gjson.String {
		if sid := normalizedSessionCandidate(conversation.String()); sid != "" {
			conversationID = "conv:" + sid
		}
	}

	pck := normalizedSessionCandidate(root.Get("prompt_cache_key").String())
	if pck == "" {
		pck = normalizedSessionCandidate(root.Get("promptCacheKey").String())
	}
	if pck == "" && hasNestedReq {
		pck = normalizedSessionCandidate(reqRoot.Get("prompt_cache_key").String())
		if pck == "" {
			pck = normalizedSessionCandidate(reqRoot.Get("promptCacheKey").String())
		}
	}

	if pck != "" {
		info := SessionInfo{
			ClientType: "generic",
			SessionID:  "pck:" + pck,
			AgentName:  "main",
		}
		if parentCandidate != "" && parentCandidate != pck {
			info.ParentSessionID = "pck:" + parentCandidate
			info.AgentName = "subagent"
		}
		return info, true
	}

	if conversationID != "" {
		info := SessionInfo{
			ClientType: "conv",
			SessionID:  conversationID,
			AgentName:  "main",
		}
		if parentCandidate != "" && ("conv:"+parentCandidate) != conversationID {
			info.ParentSessionID = "conv:" + parentCandidate
			info.AgentName = "subagent"
		}
		return info, true
	}

	return SessionInfo{}, false
}

func extractPayloadUserIDAndLegacyConv(
	root, reqRoot gjson.Result,
	hasNestedReq bool,
	parentCandidate string,
) (SessionInfo, bool) {
	// 1. Plain metadata.user_id
	userID := normalizedSessionCandidate(root.Get("metadata.user_id").String())
	if userID == "" && hasNestedReq {
		userID = normalizedSessionCandidate(reqRoot.Get("metadata.user_id").String())
	}
	if userID != "" {
		return SessionInfo{
			ClientType: "generic",
			SessionID:  "user:" + userID,
			AgentName:  "main",
		}, true
	}

	// 2. Legacy conversation string paths
	for _, convPath := range []string{"conversation_id", "conversationId", "chat_id", "chatId", "metadata.conversation_id", "extra_body.conversation_id"} {
		cid := normalizedSessionCandidate(root.Get(convPath).String())
		if cid == "" && hasNestedReq {
			cid = normalizedSessionCandidate(reqRoot.Get(convPath).String())
		}
		if cid != "" {
			info := SessionInfo{
				ClientType: "conv",
				SessionID:  "conv:" + cid,
				AgentName:  "main",
			}
			if parentCandidate != "" && ("conv:"+parentCandidate) != ("conv:"+cid) {
				info.ParentSessionID = "conv:" + parentCandidate
				info.AgentName = "subagent"
			}
			return info, true
		}
	}
	return SessionInfo{}, false
}
