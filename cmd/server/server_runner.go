package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/api"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/cmd"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/managementasset"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/misc"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pluginhost"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/tui"
	log "github.com/sirupsen/logrus"
)

func runServerOrTUI(
	cfg *config.Config,
	configFilePath string,
	configFileExists bool,
	isCloudDeploy bool,
	cliOpts *cmd.CLIOptions,
	pluginHost *pluginhost.Host,
	serverOptions []api.ServerOption,
) {
	if isCloudDeploy && !configFileExists {
		cmd.WaitForCloudDeploy()
		return
	}

	if cliOpts.LocalModel && (!cliOpts.TUIMode || cliOpts.Standalone) {
		log.Info("Local model mode: using embedded model catalogs, remote model updates disabled")
	}

	if cliOpts.TUIMode {
		if cliOpts.Standalone {
			runTUIStandalone(cfg, configFilePath, cliOpts.Password, cliOpts.LocalModel, pluginHost, serverOptions)
		} else {
			if errRun := tui.Run(cfg.Port, cliOpts.Password, nil, os.Stdout); errRun != nil {
				fmt.Fprintf(os.Stderr, "TUI error: %v\n", errRun)
			}
		}
		return
	}

	managementasset.StartAutoUpdater(context.Background(), configFilePath)
	misc.StartAntigravityVersionUpdater(context.Background())
	startModelCatalogUpdaters(cliOpts.LocalModel, cfg.Home.Enabled)
	cmd.StartServiceWithPluginHost(cfg, configFilePath, cliOpts.Password, pluginHost, serverOptions...)
}

func runTUIStandalone(
	cfg *config.Config,
	configFilePath string,
	password string,
	localModel bool,
	pluginHost *pluginhost.Host,
	serverOptions []api.ServerOption,
) {
	managementasset.StartAutoUpdater(context.Background(), configFilePath)
	misc.StartAntigravityVersionUpdater(context.Background())
	startModelCatalogUpdaters(localModel, cfg.Home.Enabled)

	hook := tui.NewLogHook(2000)
	hook.SetFormatter(&logging.LogFormatter{})
	log.AddHook(hook)

	origStdout := os.Stdout
	origStderr := os.Stderr
	origLogOutput := log.StandardLogger().Out
	log.SetOutput(io.Discard)

	devNull, errOpenDevNull := os.Open(os.DevNull)
	if errOpenDevNull == nil {
		os.Stdout = devNull
		os.Stderr = devNull
	}

	var restored bool
	restoreIO := func() {
		if restored {
			return
		}
		restored = true
		os.Stdout = origStdout
		os.Stderr = origStderr
		log.SetOutput(origLogOutput)
		if devNull != nil {
			_ = devNull.Close()
		}
	}
	defer restoreIO()

	if password == "" {
		password = fmt.Sprintf("tui-%d-%d", os.Getpid(), time.Now().UnixNano())
	}

	cancel, done := cmd.StartServiceBackgroundWithPluginHost(cfg, configFilePath, password, pluginHost, serverOptions...)

	client := tui.NewClient(cfg.Port, password)
	ready := false
	backoff := 100 * time.Millisecond
	for i := 0; i < 30; i++ {
		if _, errGetConfig := client.GetConfig(); errGetConfig == nil {
			ready = true
			break
		}
		time.Sleep(backoff)
		if backoff < time.Second {
			backoff = time.Duration(float64(backoff) * 1.5)
		}
	}

	if !ready {
		restoreIO()
		cancel()
		<-done
		fmt.Fprintf(os.Stderr, "TUI error: embedded server is not ready\n")
		return
	}

	if errRun := tui.Run(cfg.Port, password, hook, origStdout); errRun != nil {
		restoreIO()
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", errRun)
	} else {
		restoreIO()
	}

	cancel()
	<-done
}

func modelCatalogUpdaterPlan(localModel, homeEnabled bool) (startModels, startCodexClient bool) {
	if localModel {
		return false, false
	}
	return !homeEnabled, true
}

func startModelCatalogUpdaters(localModel, homeEnabled bool) {
	startModels, startCodexClient := modelCatalogUpdaterPlan(localModel, homeEnabled)
	if startCodexClient {
		registry.StartCodexClientModelsUpdater(context.Background())
	}
	if startModels {
		registry.StartModelsUpdater(context.Background())
	} else if homeEnabled {
		log.Info("Home mode: remote models.json updates disabled; Codex client model list follows Home model IDs")
	}
}

func pluginBootstrapConfigPath(args []string, defaultPath string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			return defaultPluginBootstrapConfigPath(defaultPath)
		case arg == "-config" || arg == "--config":
			if i+1 < len(args) {
				return args[i+1]
			}
			return defaultPluginBootstrapConfigPath(defaultPath)
		case strings.HasPrefix(arg, "-config="):
			return strings.TrimPrefix(arg, "-config=")
		case strings.HasPrefix(arg, "--config="):
			return strings.TrimPrefix(arg, "--config=")
		}
	}
	return defaultPluginBootstrapConfigPath(defaultPath)
}

func defaultPluginBootstrapConfigPath(defaultPath string) string {
	if strings.TrimSpace(defaultPath) != "" {
		return defaultPath
	}
	wd, errGetwd := os.Getwd()
	if errGetwd != nil {
		return "config.yaml"
	}
	return filepath.Join(wd, "config.yaml")
}

func loadPluginBootstrapConfig(path string) *config.Config {
	raw, errReadFile := os.ReadFile(path)
	if errReadFile != nil {
		if !errors.Is(errReadFile, os.ErrNotExist) {
			log.Warnf("failed to read plugin bootstrap config: %v", errReadFile)
		}
		cfg := &config.Config{}
		cfg.NormalizePluginsConfig()
		return cfg
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		cfg := &config.Config{}
		cfg.NormalizePluginsConfig()
		return cfg
	}
	cfg, errParseConfig := config.ParseConfigBytes(raw)
	if errParseConfig != nil {
		log.Warnf("failed to parse plugin bootstrap config: %v", errParseConfig)
		cfg := &config.Config{}
		cfg.NormalizePluginsConfig()
		return cfg
	}
	return cfg
}
