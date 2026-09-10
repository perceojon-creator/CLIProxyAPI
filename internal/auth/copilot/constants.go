package copilot

import "time"

// OAuth client credentials and configuration for GitHub Copilot CLI.
const (
	ClientID     = "Ov23ctDVkRmgkPke0Mmm"
	CallbackPort = 51122
)

// DefaultOAuthScopes provides the minimal required scope for GitHub Copilot subscription access.
const DefaultOAuthScopes = "read:user"

// OAuth2 endpoints for GitHub authentication
const (
	AuthEndpoint     = "https://github.com/login/oauth/authorize"
	TokenEndpoint    = "https://github.com/login/oauth/access_token"
	DeviceEndpoint   = "https://github.com/login/device/code"
	UserInfoEndpoint = "https://api.github.com/user"
)

// Copilot internal API endpoints
const (
	CopilotInternalTokenURL = "https://api.github.com/copilot_internal/v2/token"
	CopilotInternalUserURL  = "https://api.github.com/copilot_internal/user"
	DefaultUpstreamBaseURL  = "https://api.individual.githubcopilot.com"
	FallbackUpstreamBaseURL = "https://api.githubcopilot.com"
)

// Standard headers matching official VS Code Copilot Chat and CLI integrations
const (
	DefaultUserAgent           = "GithubCopilot/1.0.83"
	DefaultEditorVersion       = "vscode/1.96.0"
	DefaultEditorPluginVersion = "copilot-chat/0.24.0"
	DefaultIntegrationID       = "vscode-chat"
	DefaultOpenAIOrganization  = "github-copilot"
	DefaultOpenAIIntent        = "conversation-panel"
)

// Token safety and timeouts
const (
	TokenRefreshSafetyWindow = 5 * time.Minute
	OAuthTimeout             = 5 * time.Minute
	ManualPromptTimeout      = 15 * time.Second
)
