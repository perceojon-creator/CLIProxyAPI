package flow

import (
	"fmt"
	"strings"
	"time"
)

const (
	// GoogleFlowDomain is the official domain for Google Flow.
	GoogleFlowDomain = "https://flow.google.com"
	// FlowDomain is the primary Google Labs domain for Flow.
	FlowDomain = "https://labs.google"
	// FlowAppURL is the landing URL for the Google Flow workspace.
	FlowAppURL = "https://flow.google.com"
	// SessionCookieName is the standard NextAuth session cookie name on labs.google.
	SessionCookieName = "__Secure-next-auth.session-token"
	// CSRFCookieName is the standard NextAuth CSRF cookie name on labs.google.
	CSRFCookieName = "__Host-next-auth.csrf-token"
)

var (
	// FlowSessionEndpoint is the NextAuth session endpoint to verify tokens.
	FlowSessionEndpoint = "https://labs.google/fx/api/auth/session"
	// FlowCSRFEndpoint is the NextAuth CSRF endpoint.
	FlowCSRFEndpoint = "https://labs.google/fx/api/auth/csrf"
)

// FlowAuth represents the credentials and session metadata for a Google Flow account.
type FlowAuth struct {
	SessionToken string `json:"session_token"`
	CSRFToken    string `json:"csrf_token,omitempty"`
	Cookies      string `json:"cookies,omitempty"`
	AtToken      string `json:"at_token,omitempty"`
	ProjectID    string `json:"project_id,omitempty"`
	Email        string `json:"email"`
	Name         string `json:"name,omitempty"`
	ProfileDir   string `json:"profile_dir,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
	UpdatedAt    int64  `json:"updated_at,omitempty"`
}

// CookieHeader formats the session tokens into an HTTP Cookie header string.
func (a *FlowAuth) CookieHeader() string {
	if a == nil {
		return ""
	}
	if strings.TrimSpace(a.Cookies) != "" {
		return strings.TrimSpace(a.Cookies)
	}
	rawToken := strings.TrimSpace(a.SessionToken)
	if rawToken == "" {
		return ""
	}
	if strings.Contains(rawToken, "=") {
		return rawToken
	}
	header := fmt.Sprintf("%s=%s", SessionCookieName, rawToken)
	if strings.TrimSpace(a.CSRFToken) != "" {
		header += fmt.Sprintf("; %s=%s", CSRFCookieName, strings.TrimSpace(a.CSRFToken))
	}
	return header
}

// IsExpired checks if the session has expired.
func (a *FlowAuth) IsExpired() bool {
	if a == nil || (strings.TrimSpace(a.SessionToken) == "" && strings.TrimSpace(a.Cookies) == "") {
		return true
	}
	now := time.Now().UnixMilli()
	if a.ExpiresAt > 0 {
		return now >= a.ExpiresAt
	}
	if a.UpdatedAt > 0 {
		return now >= a.UpdatedAt+(25*24*3600*1000)
	}
	return false
}

// FlowUserInfo represents user information returned by labs.google/fx/api/auth/session.
type FlowUserInfo struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Image   string `json:"image"`
	Expires string `json:"expires"`
}
