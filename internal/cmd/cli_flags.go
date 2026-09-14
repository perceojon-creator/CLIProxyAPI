package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/pluginhost"
)

// CLIOptions holds all parsed command-line flags for CLIProxyAPI.
type CLIOptions struct {
	CodexLogin                  bool
	CodexDeviceLogin            bool
	ClaudeLogin                 bool
	NoBrowser                   bool
	OAuthCallbackPort           int
	AntigravityLogin            bool
	KimiLogin                   bool
	XAILogin                    bool
	KiroLogin                   bool
	KiroLoginAll                bool
	CopilotLogin                bool
	CopilotLoginAll             bool
	CopilotBrowserLogin         bool
	FlowLogin                   bool
	FlowLoginAll                bool
	FlowSync                    bool
	VertexImport                string
	VertexImportPrefix          string
	ConfigPath                  string
	Password                    string
	HomeJWT                     string
	HomeDisableClusterDiscovery bool
	TUIMode                     bool
	Standalone                  bool
	LocalModel                  bool
	Port                        int
}

// IsCommandMode returns true if any authentication, import, or sync command mode is active.
func (o *CLIOptions) IsCommandMode() bool {
	if o == nil {
		return false
	}
	return o.VertexImport != "" ||
		o.AntigravityLogin ||
		o.CodexLogin ||
		o.CodexDeviceLogin ||
		o.ClaudeLogin ||
		o.KimiLogin ||
		o.XAILogin ||
		o.KiroLogin ||
		o.KiroLoginAll ||
		o.CopilotLogin ||
		o.CopilotLoginAll ||
		o.CopilotBrowserLogin ||
		o.FlowLogin ||
		o.FlowLoginAll ||
		o.FlowSync
}

// RegisterFlags binds all CLIProxyAPI command-line flags to the given FlagSet.
func RegisterFlags(fs *flag.FlagSet, opts *CLIOptions, defaultConfigPath string) {
	fs.BoolVar(&opts.CodexLogin, "codex-login", false, "Login to Codex using OAuth")
	fs.BoolVar(&opts.CodexDeviceLogin, "codex-device-login", false, "Login to Codex using device code flow")
	fs.BoolVar(&opts.ClaudeLogin, "claude-login", false, "Login to Claude using OAuth")
	fs.BoolVar(&opts.NoBrowser, "no-browser", false, "Don't open browser automatically for OAuth")
	fs.IntVar(&opts.OAuthCallbackPort, "oauth-callback-port", 0, "Override OAuth callback port (defaults to provider-specific port)")
	fs.BoolVar(&opts.AntigravityLogin, "antigravity-login", false, "Login to Antigravity using OAuth")
	fs.BoolVar(&opts.KimiLogin, "kimi-login", false, "Login to Kimi using OAuth")
	fs.BoolVar(&opts.XAILogin, "xai-login", false, "Login to xAI using OAuth")
	fs.BoolVar(&opts.KiroLogin, "kiro-login", false, "Login to Kiro using local session or Google account")
	fs.BoolVar(&opts.KiroLoginAll, "kiro-login-all", false, "Login to all detected Google Chrome profiles sequentially for Kiro")
	fs.BoolVar(&opts.CopilotLogin, "copilot-login", false, "Login to GitHub Copilot subscription using OAuth, browser profiles, or local credentials")
	fs.BoolVar(&opts.CopilotLoginAll, "copilot-login-all", false, "Login to all detected browser profiles sequentially for GitHub Copilot")
	fs.BoolVar(&opts.CopilotBrowserLogin, "copilot-browser-login", false, "Force interactive browser OAuth login for GitHub Copilot (bypass local detection)")
	fs.BoolVar(&opts.FlowLogin, "flow-login", false, "Login to Google Flow using browser session or Google account")
	fs.BoolVar(&opts.FlowLoginAll, "flow-login-all", false, "Login to all detected Google Chrome profiles sequentially for Google Flow")
	fs.BoolVar(&opts.FlowSync, "flow-sync", false, "Start Google Flow local sync bridge server")
	fs.StringVar(&opts.ConfigPath, "config", defaultConfigPath, "Configure File Path")
	fs.StringVar(&opts.VertexImport, "vertex-import", "", "Import Vertex service account key JSON file")
	fs.StringVar(&opts.VertexImportPrefix, "vertex-import-prefix", "", "Prefix for Vertex model namespacing (use with -vertex-import)")
	fs.StringVar(&opts.Password, "password", "", "")
	fs.StringVar(&opts.HomeJWT, "home-jwt", "", "Home control plane JWT for mTLS certificate bootstrap and connection")
	fs.BoolVar(&opts.HomeDisableClusterDiscovery, "home-disable-cluster-discovery", false, "Disable Home CLUSTER NODES discovery and keep using the configured -home-jwt address")
	fs.BoolVar(&opts.TUIMode, "tui", false, "Start with terminal management UI")
	fs.BoolVar(&opts.Standalone, "standalone", false, "In TUI mode, start an embedded local server")
	fs.BoolVar(&opts.LocalModel, "local-model", false, "Use embedded models.json and codex_client_models.json only, skip remote model catalog fetching")
	fs.IntVar(&opts.Port, "port", 0, "Server port")

	fs.Usage = func() {
		out := fs.Output()
		_, _ = fmt.Fprintf(out, "Usage of %s\n", os.Args[0])
		fs.VisitAll(func(f *flag.Flag) {
			if f.Name == "password" {
				return
			}
			s := fmt.Sprintf("  -%s", f.Name)
			name, unquoteUsage := flag.UnquoteUsage(f)
			if name != "" {
				s += " " + name
			}
			if len(s) <= 4 {
				s += "\t"
			} else {
				s += "\n    "
			}
			if unquoteUsage != "" {
				s += unquoteUsage
			}
			if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" {
				s += fmt.Sprintf(" (default %s)", f.DefValue)
			}
			_, _ = fmt.Fprint(out, s+"\n")
		})
	}
}

// PluginBootstrapConfigPath searches arguments for custom config flag, falling back to defaultConfigPath.
func PluginBootstrapConfigPath(args []string, defaultConfigPath string) string {
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "-config" || arg == "--config" {
			if i+1 < len(args) {
				return strings.TrimSpace(args[i+1])
			}
		}
		if strings.HasPrefix(arg, "-config=") || strings.HasPrefix(arg, "--config=") {
			parts := strings.SplitN(arg, "=", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return strings.TrimSpace(defaultConfigPath)
}

// ParseCLIFlags registers flags, attaches plugins, and parses os.Args.
func ParseCLIFlags(args []string, defaultConfigPath string, pluginHost *pluginhost.Host) (*CLIOptions, error) {
	opts := &CLIOptions{}
	RegisterFlags(flag.CommandLine, opts, defaultConfigPath)

	if pluginHost != nil {
		pluginHost.RegisterCommandLineFlags(context.Background(), flag.CommandLine)
	}

	if err := flag.CommandLine.Parse(args); err != nil {
		return nil, err
	}
	return opts, nil
}
