package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/cmd"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/home"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/homeplugins"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/misc"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pluginhost"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/redisqueue"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/store"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	sdkAuth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	sdkpluginstore "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginstore"
	log "github.com/sirupsen/logrus"
)

type storageEnv struct {
	usePostgresStore     bool
	pgStoreDSN           string
	pgStoreSchema        string
	pgStoreLocalPath     string
	useGitStore          bool
	gitStoreRemoteURL    string
	gitStoreUser         string
	gitStorePassword     string
	gitStoreBranch       string
	gitStoreLocalPath    string
	useObjectStore       bool
	objectStoreEndpoint  string
	objectStoreAccess    string
	objectStoreSecret    string
	objectStoreBucket    string
	objectStoreLocalPath string
	homeJWT              string
}

func readStorageEnv(cliOpts *cmd.CLIOptions, wd, writableBase string) storageEnv {
	var env storageEnv
	env.homeJWT = cliOpts.HomeJWT
	if strings.TrimSpace(env.homeJWT) == "" {
		if v, ok := lookupEnv("HOME_JWT", "home_jwt"); ok {
			env.homeJWT = v
		}
	}

	if value, ok := lookupEnv("PGSTORE_DSN", "pgstore_dsn"); ok {
		env.usePostgresStore = true
		env.pgStoreDSN = value
	}
	if env.usePostgresStore {
		if value, ok := lookupEnv("PGSTORE_SCHEMA", "pgstore_schema"); ok {
			env.pgStoreSchema = value
		}
		if value, ok := lookupEnv("PGSTORE_LOCAL_PATH", "pgstore_local_path"); ok {
			env.pgStoreLocalPath = value
		}
		if env.pgStoreLocalPath == "" {
			if writableBase != "" {
				env.pgStoreLocalPath = writableBase
			} else {
				env.pgStoreLocalPath = wd
			}
		}
		env.useGitStore = false
	}

	if value, ok := lookupEnv("GITSTORE_GIT_URL", "gitstore_git_url"); ok {
		env.useGitStore = true
		env.gitStoreRemoteURL = value
	}
	if value, ok := lookupEnv("GITSTORE_GIT_USERNAME", "gitstore_git_username"); ok {
		env.gitStoreUser = value
	}
	if value, ok := lookupEnv("GITSTORE_GIT_TOKEN", "gitstore_git_token"); ok {
		env.gitStorePassword = value
	}
	if value, ok := lookupEnv("GITSTORE_LOCAL_PATH", "gitstore_local_path"); ok {
		env.gitStoreLocalPath = value
	}
	if value, ok := lookupEnv("GITSTORE_GIT_BRANCH", "gitstore_git_branch"); ok {
		env.gitStoreBranch = value
	}

	if value, ok := lookupEnv("OBJECTSTORE_ENDPOINT", "objectstore_endpoint"); ok {
		env.useObjectStore = true
		env.objectStoreEndpoint = value
	}
	if value, ok := lookupEnv("OBJECTSTORE_ACCESS_KEY", "objectstore_access_key"); ok {
		env.objectStoreAccess = value
	}
	if value, ok := lookupEnv("OBJECTSTORE_SECRET_KEY", "objectstore_secret_key"); ok {
		env.objectStoreSecret = value
	}
	if value, ok := lookupEnv("OBJECTSTORE_BUCKET", "objectstore_bucket"); ok {
		env.objectStoreBucket = value
	}
	if value, ok := lookupEnv("OBJECTSTORE_LOCAL_PATH", "objectstore_local_path"); ok {
		env.objectStoreLocalPath = value
	}

	return env
}

func lookupEnv(keys ...string) (string, bool) {
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed, true
			}
		}
	}
	return "", false
}

func bootstrapConfig(
	wd string,
	cliOpts *cmd.CLIOptions,
	isCloudDeploy bool,
	pluginHost *pluginhost.Host,
) (*config.Config, string, bool, *home.Client, error) {
	writableBase := util.WritablePath()
	env := readStorageEnv(cliOpts, wd, writableBase)

	var (
		cfg                   *config.Config
		configFilePath        string
		configLoadedFromHome  bool
		homeClient            *home.Client
		homePluginSyncReport  homeplugins.SyncReport
		homePluginStatusReady bool
		err                   error
	)

	if strings.TrimSpace(env.homeJWT) != "" {
		cfg, configFilePath, homeClient, homePluginSyncReport, homePluginStatusReady, err = bootstrapHomeConfig(
			env.homeJWT, cliOpts.HomeDisableClusterDiscovery, cliOpts.ConfigPath, wd, pluginHost,
		)
		if err != nil {
			return nil, "", false, nil, err
		}
		configLoadedFromHome = true
		sdkAuth.RegisterTokenStore(sdkAuth.NewFileTokenStore())
	} else if env.usePostgresStore {
		cfg, configFilePath, err = bootstrapPostgresStore(env, wd, isCloudDeploy)
		if err != nil {
			return nil, "", false, nil, err
		}
	} else if env.useObjectStore {
		cfg, configFilePath, err = bootstrapObjectStore(env, wd, writableBase, isCloudDeploy)
		if err != nil {
			return nil, "", false, nil, err
		}
	} else if env.useGitStore {
		cfg, configFilePath, err = bootstrapGitStore(env, wd, writableBase, isCloudDeploy)
		if err != nil {
			return nil, "", false, nil, err
		}
	} else {
		cfg, configFilePath, err = bootstrapLocalFileConfig(cliOpts.ConfigPath, wd, isCloudDeploy)
		if err != nil {
			return nil, "", false, nil, err
		}
		sdkAuth.RegisterTokenStore(sdkAuth.NewFileTokenStore())
	}

	if cfg == nil {
		cfg = &config.Config{}
	}

	configFileExists := evaluateConfigFileExists(isCloudDeploy, configLoadedFromHome, configFilePath, cfg)
	redisqueue.SetUsageStatisticsEnabled(cfg.UsageStatisticsEnabled)
	redisqueue.SetRetentionSeconds(cfg.RedisUsageQueueRetentionSeconds)

	if configLoadedFromHome && homePluginStatusReady {
		if errLoad := homeplugins.MarkLoadResults(&homePluginSyncReport, pluginHost); errLoad != nil {
			log.Errorf("failed to load home plugins: %v", errLoad)
			if homeClient != nil {
				homeClient.Close()
			}
			return nil, "", false, nil, errLoad
		}
		if errReport := home.ReportPluginStatus(context.Background(), homeClient, cfg.Home.NodeID, homePluginSyncReport); errReport != nil {
			log.Warnf("failed to report home plugin load status: %v", errReport)
		}
	}

	return cfg, configFilePath, configFileExists, homeClient, nil
}

func bootstrapHomeConfig(
	homeJWT string,
	disableClusterDiscovery bool,
	configPath string,
	wd string,
	pluginHost *pluginhost.Host,
) (*config.Config, string, *home.Client, homeplugins.SyncReport, bool, error) {
	ctxHome, cancelHome := context.WithTimeout(context.Background(), 30*time.Second)
	homeCfg, errHomeCfg := home.ConfigFromJWT(ctxHome, homeJWT)
	cancelHome()
	if errHomeCfg != nil {
		return nil, "", nil, homeplugins.SyncReport{}, false, fmt.Errorf("invalid -home-jwt: %w", errHomeCfg)
	}
	if disableClusterDiscovery {
		homeCfg.DisableClusterDiscovery = true
	}
	homeClient := home.New(homeCfg)

	ctxHomeConfig, cancelHomeConfig := context.WithTimeout(context.Background(), 30*time.Second)
	raw, errGetConfig := homeClient.GetConfig(ctxHomeConfig)
	cancelHomeConfig()
	if errGetConfig != nil {
		homeClient.Close()
		return nil, "", nil, homeplugins.SyncReport{}, false, fmt.Errorf("failed to fetch config from home: %w", errGetConfig)
	}

	parsed, errParseConfig := config.ParseConfigBytes(raw)
	if errParseConfig != nil {
		homeClient.Close()
		return nil, "", nil, homeplugins.SyncReport{}, false, fmt.Errorf("failed to parse config payload from home: %w", errParseConfig)
	}
	if parsed == nil {
		parsed = &config.Config{}
	}
	parsed.Home = homeCfg
	parsed.Port = config.NormalizeHomePort(parsed.Port)
	parsed.UsageStatisticsEnabled = true
	pluginSyncCfg := *parsed
	parsed.Plugins.StoreAuth = nil

	var (
		errHomePlugins        error
		homePluginSyncReport  homeplugins.SyncReport
		homePluginStatusReady bool
	)
	platform := homeplugins.CurrentPlatform()
	if pluginSyncCfg.Plugins.Enabled {
		ctxHomePlugins, cancelHomePlugins := context.WithTimeout(context.Background(), 30*time.Second)
		installedVersions, errInstalledPlugins := homeplugins.InstalledVersions(&pluginSyncCfg)
		if errInstalledPlugins != nil {
			homePluginStatusReady = true
			errHomePlugins = errInstalledPlugins
			homePluginSyncReport = homeplugins.CompletedSyncReport(platform, errInstalledPlugins)
		} else {
			pluginSyncRequest := sdkpluginstore.PluginSyncRequest{
				SchemaVersion:     sdkpluginstore.PluginSyncSchemaVersion,
				GOOS:              platform.GOOS,
				GOARCH:            platform.GOARCH,
				InstalledVersions: installedVersions,
			}
			pluginSyncResponse, errFetchPlugins := homeClient.GetPluginSync(ctxHomePlugins, pluginSyncRequest)
			errHomePlugins = errFetchPlugins
			switch {
			case errHomePlugins == nil:
				homePluginStatusReady = true
				homePluginSyncReport, errHomePlugins = homeplugins.SyncResolvedWithReport(ctxHomePlugins, &pluginSyncCfg, pluginSyncResponse.Items, pluginSyncResponse.ExpiresAt, pluginSyncRequest.InstalledVersions, pluginHost)
			case errors.Is(errHomePlugins, home.ErrPluginSyncUnsupported):
				homePluginStatusReady = true
				homePluginSyncReport, errHomePlugins = homeplugins.SyncWithReport(ctxHomePlugins, &pluginSyncCfg, pluginHost)
			default:
				homePluginStatusReady = true
				homePluginSyncReport = homeplugins.CompletedSyncReport(platform, errHomePlugins)
			}
			pluginSyncRequest.Clear()
			pluginSyncResponse.Clear()
		}
		cancelHomePlugins()
	} else {
		homePluginStatusReady = true
		homePluginSyncReport = homeplugins.CompletedSyncReport(platform, nil)
	}
	if errHomePlugins != nil {
		log.Errorf("failed to sync plugins from home: %v", errHomePlugins)
		homeClient.Close()
		return nil, "", nil, homePluginSyncReport, false, errHomePlugins
	}
	if homePluginStatusReady {
		if errReport := home.ReportPluginStatus(context.Background(), homeClient, homeCfg.NodeID, homePluginSyncReport); errReport != nil {
			log.Warnf("failed to report home plugin sync status: %v", errReport)
		}
	}

	configFilePath := configPath
	if strings.TrimSpace(configFilePath) == "" {
		configFilePath = filepath.Join(wd, "config.yaml")
	}

	return parsed, configFilePath, homeClient, homePluginSyncReport, homePluginStatusReady, nil
}

func bootstrapPostgresStore(env storageEnv, wd string, isCloudDeploy bool) (*config.Config, string, error) {
	pgStoreLocalPath := env.pgStoreLocalPath
	if pgStoreLocalPath == "" {
		pgStoreLocalPath = wd
	}
	pgStoreLocalPath = filepath.Join(pgStoreLocalPath, "pgstore")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	pgStoreInst, err := store.NewPostgresStore(ctx, store.PostgresStoreConfig{
		DSN:      env.pgStoreDSN,
		Schema:   env.pgStoreSchema,
		SpoolDir: pgStoreLocalPath,
	})
	cancel()
	if err != nil {
		return nil, "", fmt.Errorf("failed to initialize postgres token store: %w", err)
	}
	sdkAuth.RegisterTokenStore(pgStoreInst)

	examplePath := filepath.Join(wd, "config.example.yaml")
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	if errBootstrap := pgStoreInst.Bootstrap(ctx, examplePath); errBootstrap != nil {
		cancel()
		return nil, "", fmt.Errorf("failed to bootstrap postgres-backed config: %w", errBootstrap)
	}
	cancel()

	configFilePath := pgStoreInst.ConfigPath()
	cfg, err := config.LoadConfigOptional(configFilePath, isCloudDeploy)
	if err != nil {
		return nil, "", fmt.Errorf("failed to load postgres config: %w", err)
	}
	if cfg != nil {
		cfg.AuthDir = pgStoreInst.AuthDir()
	}
	log.Infof("postgres-backed token store enabled, workspace path: %s", pgStoreInst.WorkDir())
	return cfg, configFilePath, nil
}

func bootstrapObjectStore(env storageEnv, wd, writableBase string, isCloudDeploy bool) (*config.Config, string, error) {
	objectStoreLocalPath := env.objectStoreLocalPath
	if objectStoreLocalPath == "" {
		if writableBase != "" {
			objectStoreLocalPath = writableBase
		} else {
			objectStoreLocalPath = wd
		}
	}
	objectStoreRoot := filepath.Join(objectStoreLocalPath, "objectstore")
	resolvedEndpoint := strings.TrimSpace(env.objectStoreEndpoint)
	useSSL := true
	if strings.Contains(resolvedEndpoint, "://") {
		parsed, errParse := url.Parse(resolvedEndpoint)
		if errParse != nil {
			return nil, "", fmt.Errorf("failed to parse object store endpoint %q: %w", env.objectStoreEndpoint, errParse)
		}
		switch strings.ToLower(parsed.Scheme) {
		case "http":
			useSSL = false
		case "https":
			useSSL = true
		default:
			return nil, "", fmt.Errorf("unsupported object store scheme %q", parsed.Scheme)
		}
		if parsed.Host == "" {
			return nil, "", fmt.Errorf("object store endpoint %q is missing host information", env.objectStoreEndpoint)
		}
		resolvedEndpoint = parsed.Host
		if parsed.Path != "" && parsed.Path != "/" {
			resolvedEndpoint = strings.TrimSuffix(parsed.Host+parsed.Path, "/")
		}
	}
	resolvedEndpoint = strings.TrimRight(resolvedEndpoint, "/")

	objCfg := store.ObjectStoreConfig{
		Endpoint:  resolvedEndpoint,
		Bucket:    env.objectStoreBucket,
		AccessKey: env.objectStoreAccess,
		SecretKey: env.objectStoreSecret,
		LocalRoot: objectStoreRoot,
		UseSSL:    useSSL,
		PathStyle: true,
	}
	objectStoreInst, err := store.NewObjectTokenStore(objCfg)
	if err != nil {
		return nil, "", fmt.Errorf("failed to initialize object token store: %w", err)
	}
	sdkAuth.RegisterTokenStore(objectStoreInst)

	examplePath := filepath.Join(wd, "config.example.yaml")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	if errBootstrap := objectStoreInst.Bootstrap(ctx, examplePath); errBootstrap != nil {
		cancel()
		return nil, "", fmt.Errorf("failed to bootstrap object-backed config: %w", errBootstrap)
	}
	cancel()

	configFilePath := objectStoreInst.ConfigPath()
	cfg, err := config.LoadConfigOptional(configFilePath, isCloudDeploy)
	if err != nil {
		return nil, "", fmt.Errorf("failed to load object store config: %w", err)
	}
	if cfg == nil {
		cfg = &config.Config{}
	}
	cfg.AuthDir = objectStoreInst.AuthDir()
	log.Infof("object-backed token store enabled, bucket: %s", env.objectStoreBucket)
	return cfg, configFilePath, nil
}

func bootstrapGitStore(env storageEnv, wd, writableBase string, isCloudDeploy bool) (*config.Config, string, error) {
	gitStoreLocalPath := env.gitStoreLocalPath
	if gitStoreLocalPath == "" {
		if writableBase != "" {
			gitStoreLocalPath = writableBase
		} else {
			gitStoreLocalPath = wd
		}
	}
	gitStoreRoot := filepath.Join(gitStoreLocalPath, "gitstore")
	authDir := filepath.Join(gitStoreRoot, "auths")
	gitStoreInst := store.NewGitTokenStore(env.gitStoreRemoteURL, env.gitStoreUser, env.gitStorePassword, env.gitStoreBranch)
	gitStoreInst.SetBaseDir(authDir)
	if errRepo := gitStoreInst.EnsureRepository(); errRepo != nil {
		return nil, "", fmt.Errorf("failed to prepare git token store: %w", errRepo)
	}
	sdkAuth.RegisterTokenStore(gitStoreInst)

	configFilePath := gitStoreInst.ConfigPath()
	if configFilePath == "" {
		configFilePath = filepath.Join(gitStoreRoot, "config", "config.yaml")
	}
	if _, statErr := os.Stat(configFilePath); errors.Is(statErr, fs.ErrNotExist) {
		examplePath := filepath.Join(wd, "config.example.yaml")
		if _, errExample := os.Stat(examplePath); errExample != nil {
			return nil, "", fmt.Errorf("failed to find template config file: %w", errExample)
		}
		if errCopy := misc.CopyConfigTemplate(examplePath, configFilePath); errCopy != nil {
			return nil, "", fmt.Errorf("failed to bootstrap git-backed config: %w", errCopy)
		}
		if errCommit := gitStoreInst.PersistConfig(context.Background()); errCommit != nil {
			return nil, "", fmt.Errorf("failed to commit initial git-backed config: %w", errCommit)
		}
		log.Infof("git-backed config initialized from template: %s", configFilePath)
	} else if statErr != nil {
		return nil, "", fmt.Errorf("failed to inspect git-backed config: %w", statErr)
	}

	cfg, err := config.LoadConfigOptional(configFilePath, isCloudDeploy)
	if err != nil {
		return nil, "", fmt.Errorf("failed to load git config: %w", err)
	}
	if cfg != nil {
		cfg.AuthDir = gitStoreInst.AuthDir()
	}
	log.Infof("git-backed token store enabled, repository path: %s", gitStoreRoot)
	return cfg, configFilePath, nil
}

func bootstrapLocalFileConfig(configPath, wd string, isCloudDeploy bool) (*config.Config, string, error) {
	configFilePath := configPath
	if configFilePath == "" {
		configFilePath = filepath.Join(wd, "config.yaml")
	}
	cfg, err := config.LoadConfigOptional(configFilePath, isCloudDeploy)
	if err != nil {
		return nil, "", fmt.Errorf("failed to load config from %s: %w", configFilePath, err)
	}
	return cfg, configFilePath, nil
}

func evaluateConfigFileExists(isCloudDeploy, configLoadedFromHome bool, configFilePath string, cfg *config.Config) bool {
	if !isCloudDeploy {
		return true
	}
	if configLoadedFromHome && cfg != nil {
		return cfg.Port != 0
	}
	info, errStat := os.Stat(configFilePath)
	if errStat != nil {
		log.Info("Cloud deploy mode: No configuration file detected; standing by for configuration")
		return false
	}
	if info.IsDir() {
		log.Info("Cloud deploy mode: Config path is a directory; standing by for configuration")
		return false
	}
	if cfg.Port == 0 {
		log.Info("Cloud deploy mode: Configuration file is empty or invalid; standing by for valid configuration")
		return false
	}
	log.Info("Cloud deploy mode: Configuration file detected; starting service")
	return true
}
