package session

import (
	"net/http"
)

type headerSessionRule struct {
	primaryHeaders []string
	parentHeaders  []string
	clientType     string
	idPrefix       string
	defaultAgent   string
}

func extractAntigravitySession(headers http.Header, parentCandidate string) (SessionInfo, bool) {
	rule := headerSessionRule{
		primaryHeaders: []string{"X-Http-Session-Id"},
		parentHeaders:  []string{"X-Parent-Session-ID", "X-Parent-Session-Id", "X-Parent-ID", "X-Parent-Id"},
		clientType:     "agy",
		idPrefix:       "agy:",
		defaultAgent:   "main",
	}
	return matchHeaderSessionRule(headers, rule, parentCandidate)
}

func extractHeaderSessions(headers http.Header, parentCandidate string) (SessionInfo, bool) {
	rules := []headerSessionRule{
		{
			primaryHeaders: []string{"X-Session-ID"},
			parentHeaders:  []string{"X-Parent-Session-ID", "X-Parent-Session-Id", "X-Parent-ID", "X-Parent-Id"},
			clientType:     "generic",
			idPrefix:       "header:",
			defaultAgent:   "main",
		},
		{
			primaryHeaders: []string{"X-Session-Affinity"},
			parentHeaders:  []string{"X-Parent-Session-Affinity", "X-Parent-Session-ID", "X-Parent-ID", "X-Parent-Id"},
			clientType:     "opencode",
			idPrefix:       "affinity:",
			defaultAgent:   "main",
		},
		{
			primaryHeaders: []string{"X-Slot-Session-Id"},
			parentHeaders:  []string{"X-Parent-Slot-Session-Id", "X-Parent-Session-ID", "X-Parent-Session-Id", "X-Parent-ID", "X-Parent-Id"},
			clientType:     "pi",
			idPrefix:       "slot:",
			defaultAgent:   "slot",
		},
		{
			primaryHeaders: []string{"X-Task-ID", "X-Task-Id", "X-Task_ID"},
			parentHeaders:  []string{"X-Parent-Task-ID", "X-Parent-Task-Id", "X-Parent-Session-ID", "X-Parent-Session-Id", "X-Parent-ID", "X-Parent-Id"},
			clientType:     "task",
			idPrefix:       "task:",
			defaultAgent:   "main",
		},
		{
			primaryHeaders: []string{"X-Conversation-Id"},
			parentHeaders:  []string{"X-Parent-Conversation-Id", "X-Parent-ID"},
			clientType:     "conv",
			idPrefix:       "conv:",
			defaultAgent:   "main",
		},
		{
			primaryHeaders: []string{"X-Thread-Id"},
			parentHeaders:  []string{"X-Parent-Thread-Id", "X-Parent-ID"},
			clientType:     "openai-thread",
			idPrefix:       "thread:",
			defaultAgent:   "main",
		},
		{
			primaryHeaders: []string{"X-Client-Request-Id"},
			parentHeaders:  []string{"X-Parent-Session-ID", "X-Parent-ID", "X-Parent-Id"},
			clientType:     "generic",
			idPrefix:       "clientreq:",
			defaultAgent:   "main",
		},
	}

	for _, rule := range rules {
		if info, ok := matchHeaderSessionRule(headers, rule, parentCandidate); ok {
			return info, true
		}
	}
	return SessionInfo{}, false
}

func matchHeaderSessionRule(
	headers http.Header,
	rule headerSessionRule,
	parentCandidate string,
) (SessionInfo, bool) {
	var sid string
	for _, h := range rule.primaryHeaders {
		if sid = sessionHeaderValue(headers, h); sid != "" {
			break
		}
	}
	if sid == "" {
		return SessionInfo{}, false
	}

	info := SessionInfo{
		ClientType: rule.clientType,
		SessionID:  rule.idPrefix + sid,
	}

	var parentSID string
	for _, ph := range rule.parentHeaders {
		if parentSID = sessionHeaderValue(headers, ph); parentSID != "" {
			break
		}
	}

	if parentSID != "" && parentSID != sid {
		info.ParentSessionID = rule.idPrefix + parentSID
		info.AgentName = "subagent"
	} else if parentCandidate != "" && parentCandidate != sid {
		info.ParentSessionID = rule.idPrefix + parentCandidate
		info.AgentName = "subagent"
	} else {
		info.AgentName = rule.defaultAgent
	}

	return info, true
}
