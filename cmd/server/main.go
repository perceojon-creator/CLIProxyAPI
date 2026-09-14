// Package main provides the entry point for the CLI Proxy API server.
// This server acts as a proxy that provides OpenAI/Gemini/Claude compatible API interfaces
// for CLI models, allowing CLI models to be used with tools and libraries designed for standard AI APIs.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	configaccess "github.com/router-for-me/CLIProxyAPI/v7/internal/access/config_access"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/api"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/buildinfo"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/cmd"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/managementasset"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pluginhost"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/safemode"
	_ "github.com/router-for-me/CLIProxyAPI/v7/internal/translator"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

var (
	Version           = "dev"
	Commit            = "none"
	BuildDate         = "unknown"
	DefaultConfigPath = ""
)

// init initializes the shared logger setup.
func init() {
	logging.SetupBaseLogger()
	buildinfo.Version = Version
	buildinfo.Commit = Commit
	buildinfo.BuildDate = BuildDate
}

func shouldEnableExampleAPIKeySafeMode(cfg *config.Config, commandMode, tuiMode, standalone, cloudConfigMissing, homeMode bool) bool {
	if cfg == nil || commandMode || homeMode || cloudConfigMissing {
		return false
	}
	if tuiMode && !standalone {
		return false
	}
	return safemode.HasExampleAPIKeys(cfg.APIKeys)
}

// main is the entry point of the application.
// It parses command-line flags, bootstraps configuration, dispatches commands,
// and starts the proxy server or terminal management UI.
func main() {
	fmt.Printf("CLIProxyAPI Version: %s, Commit: %s, BuiltAt: %s\n", buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate)

	pluginHost := pluginhost.New()
	if bootstrapCfg := loadPluginBootstrapConfig(pluginBootstrapConfigPath(os.Args[1:], DefaultConfigPath)); bootstrapCfg != nil {
		pluginHost.ApplyConfig(context.Background(), bootstrapCfg)
		pluginHost.RegisterCommandLineFlags(context.Background(), flag.CommandLine)
	}

	cliOpts, errParse := cmd.ParseCLIFlags(os.Args[1:], DefaultConfigPath, pluginHost)
	if errParse != nil {
		log.Errorf("failed to parse command line flags: %v", errParse)
		return
	}

	wd, errWd := os.Getwd()
	if errWd != nil {
		log.Errorf("failed to get working directory: %v", errWd)
		return
	}

	if errLoad := godotenv.Load(filepath.Join(wd, ".env")); errLoad != nil {
		if !errors.Is(errLoad, os.ErrNotExist) {
			log.WithError(errLoad).Warn("failed to load .env file")
		}
	}

	isCloudDeploy := os.Getenv("DEPLOY") == "cloud"
	cfg, configFilePath, configFileExists, homeClient, errBootstrap := bootstrapConfig(wd, cliOpts, isCloudDeploy, pluginHost)
	if errBootstrap != nil {
		log.Errorf("failed to initialize configuration: %v", errBootstrap)
		return
	}
	if homeClient != nil {
		defer homeClient.Close()
	}

	coreauth.SetQuotaCooldownDisabled(cfg.DisableCooling)
	coreauth.SetTransientErrorCooldownSeconds(cfg.TransientErrorCooldownSeconds)

	if err := logging.ConfigureLogOutput(cfg); err != nil {
		log.Errorf("failed to configure log output: %v", err)
		return
	}

	log.Infof("CLIProxyAPI Version: %s, Commit: %s, BuiltAt: %s", buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate)
	util.SetLogLevel(cfg)

	if resolvedAuthDir, errResolveAuthDir := util.ResolveAuthDir(cfg.AuthDir); errResolveAuthDir != nil {
		log.Errorf("failed to resolve auth directory: %v", errResolveAuthDir)
		return
	} else {
		cfg.AuthDir = resolvedAuthDir
	}
	managementasset.SetCurrentConfig(cfg)

	// Command mode execution: check if an auth/import/sync command was requested
	if cmd.DispatchCommand(cfg, cliOpts) {
		return
	}

	// Prepare server startup options
	cloudConfigMissing := isCloudDeploy && !configFileExists
	homeMode := (cfg != nil && cfg.Home.Enabled)
	exampleAPIKeySafeMode := shouldEnableExampleAPIKeySafeMode(cfg, cliOpts.IsCommandMode(), cliOpts.TUIMode, cliOpts.Standalone, cloudConfigMissing, homeMode)
	var serverOptions []api.ServerOption
	if exampleAPIKeySafeMode {
		matches := safemode.ExampleAPIKeys(cfg.APIKeys)
		log.WithField("api_keys", strings.Join(matches, ",")).Error("unsafe example API key configured; proxy API endpoints disabled until api-keys is updated")
		serverOptions = append(serverOptions, api.WithExampleAPIKeySafeMode())
	}

	configaccess.Register(&cfg.SDKConfig)
	pluginHost.ApplyConfig(context.Background(), cfg)

	if pluginHost.HasTriggeredCommandLineFlags() {
		if exitCode, handled := pluginHost.ExecuteCommandLine(context.Background(), os.Args[0], os.Args[1:], configFilePath, flag.CommandLine); handled {
			if exitCode != 0 {
				os.Exit(exitCode)
			}
			return
		}
	}

	runServerOrTUI(cfg, configFilePath, configFileExists, isCloudDeploy, cliOpts, pluginHost, serverOptions)
}
