package cmd

import (
	"flag"
	"testing"
)

func TestCLIOptions_IsCommandMode(t *testing.T) {
	opts := &CLIOptions{}
	if opts.IsCommandMode() {
		t.Fatalf("expected IsCommandMode false for empty options")
	}

	opts.AntigravityLogin = true
	if !opts.IsCommandMode() {
		t.Fatalf("expected IsCommandMode true when AntigravityLogin is set")
	}
	opts.AntigravityLogin = false

	opts.FlowSync = true
	if !opts.IsCommandMode() {
		t.Fatalf("expected IsCommandMode true when FlowSync is set")
	}
	opts.FlowSync = false

	opts.VertexImport = "/path/to/key.json"
	if !opts.IsCommandMode() {
		t.Fatalf("expected IsCommandMode true when VertexImport is set")
	}
}

func TestPluginBootstrapConfigPath(t *testing.T) {
	defaultPath := "config.yaml"

	if res := PluginBootstrapConfigPath([]string{}, defaultPath); res != defaultPath {
		t.Fatalf("expected %s, got %s", defaultPath, res)
	}
	if res := PluginBootstrapConfigPath([]string{"-config", "custom.yaml"}, defaultPath); res != "custom.yaml" {
		t.Fatalf("expected custom.yaml, got %s", res)
	}
	if res := PluginBootstrapConfigPath([]string{"--config=custom2.yaml"}, defaultPath); res != "custom2.yaml" {
		t.Fatalf("expected custom2.yaml, got %s", res)
	}
}

func TestRegisterFlagsAndParsing(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	opts := &CLIOptions{}
	RegisterFlags(fs, opts, "default.yaml")

	err := fs.Parse([]string{
		"-antigravity-login",
		"-no-browser",
		"-oauth-callback-port", "9999",
		"-config", "myconfig.yaml",
		"--port", "8499",
	})
	if err != nil {
		t.Fatalf("unexpected error parsing flags: %v", err)
	}

	if !opts.AntigravityLogin {
		t.Fatalf("expected AntigravityLogin true")
	}
	if !opts.NoBrowser {
		t.Fatalf("expected NoBrowser true")
	}
	if opts.OAuthCallbackPort != 9999 {
		t.Fatalf("expected OAuthCallbackPort 9999, got %d", opts.OAuthCallbackPort)
	}
	if opts.Port != 8499 {
		t.Fatalf("expected Port 8499, got %d", opts.Port)
	}
	if opts.ConfigPath != "myconfig.yaml" {
		t.Fatalf("expected ConfigPath myconfig.yaml, got %s", opts.ConfigPath)
	}
}

func TestDispatchCommand_NonCommandMode(t *testing.T) {
	opts := &CLIOptions{}
	handled := DispatchCommand(nil, opts)
	if handled {
		t.Fatalf("expected DispatchCommand false for non-command mode")
	}
	if DispatchCommand(nil, nil) {
		t.Fatalf("expected DispatchCommand false for nil options")
	}
}
