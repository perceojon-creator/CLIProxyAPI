package cmd

import (
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

// DispatchCommand checks if any command mode is requested by CLIOptions,
// executes the corresponding command flow, and returns true if a command was handled.
func DispatchCommand(cfg *config.Config, opts *CLIOptions) bool {
	if opts == nil || !opts.IsCommandMode() {
		return false
	}

	options := &LoginOptions{
		NoBrowser:    opts.NoBrowser,
		CallbackPort: opts.OAuthCallbackPort,
		ForceBrowser: opts.CopilotBrowserLogin,
	}

	switch {
	case opts.VertexImport != "":
		DoVertexImport(cfg, opts.VertexImport, opts.VertexImportPrefix)
	case opts.AntigravityLogin:
		DoAntigravityLogin(cfg, options)
	case opts.CodexLogin:
		DoCodexLogin(cfg, options)
	case opts.CodexDeviceLogin:
		DoCodexDeviceLogin(cfg, options)
	case opts.ClaudeLogin:
		DoClaudeLogin(cfg, options)
	case opts.KimiLogin:
		DoKimiLogin(cfg, options)
	case opts.XAILogin:
		DoXAILogin(cfg, options)
	case opts.KiroLogin || opts.KiroLoginAll:
		DoKiroLogin(cfg, options, opts.KiroLoginAll)
	case opts.CopilotLogin || opts.CopilotLoginAll || opts.CopilotBrowserLogin:
		DoCopilotLogin(cfg, options, opts.CopilotLoginAll)
	case opts.FlowSync:
		StartFlowSyncServer(cfg)
	case opts.FlowLogin || opts.FlowLoginAll:
		DoFlowLogin(cfg, options, opts.FlowLoginAll)
	default:
		return false
	}

	return true
}
