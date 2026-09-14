package session

import (
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func extractMetadataSession(metadata map[string]any) (SessionInfo, bool) {
	if metadata == nil {
		return SessionInfo{}, false
	}

	var info SessionInfo

	// 1. ExecutionSessionMetadataKey
	if executionID, ok := metadata[cliproxyexecutor.ExecutionSessionMetadataKey].(string); ok {
		if executionID = normalizedSessionCandidate(executionID); executionID != "" {
			info.ClientType = "generic"
			info.SessionID = "execution:" + executionID
			info.AgentName = "main"
			return info, true
		}
	}

	// 2. LCPAffinitySessionIDMetadataKey
	if lcpID, ok := metadata[cliproxyexecutor.LCPAffinitySessionIDMetadataKey].(string); ok {
		if lcpID = normalizedSessionCandidate(lcpID); lcpID != "" {
			info.ClientType = "lcp"
			info.SessionID = lcpID
			if parentID, okParent := metadata[cliproxyexecutor.ParentSessionIDMetadataKey].(string); okParent {
				if parentID = normalizedSessionCandidate(parentID); parentID != "" && parentID != lcpID {
					info.ParentSessionID = parentID
					info.AgentName = "subagent"
					info.IsFork = true
				} else {
					info.AgentName = "main"
				}
			} else {
				info.AgentName = "main"
			}
			return info, true
		}
	}

	return SessionInfo{}, false
}
