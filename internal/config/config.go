package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

type Config struct {
	App      AppConfig      `mapstructure:"app" yaml:"app"`
	Server   ServerConfig   `mapstructure:"server" yaml:"server"`
	Logging  LoggingConfig  `mapstructure:"logging" yaml:"logging"`
	UI       UIConfig       `mapstructure:"ui" yaml:"ui"`
	Workflow WorkflowConfig `mapstructure:"workflow" yaml:"workflow"`
	Webhook  WebhookConfig  `mapstructure:"webhook" yaml:"webhook"`
	Node     NodeConfig     `mapstructure:"node" yaml:"node"`

	// ConfigFilePath is the resolved path to the active config file.
	// Set by the caller after loading — not read from TOML.
	ConfigFilePath string `mapstructure:"-" yaml:"-"`
}

type NodeConfig struct {
	ID                 string          `mapstructure:"id" yaml:"id"`
	Controller         bool            `mapstructure:"controller" yaml:"controller"`
	Worker             bool            `mapstructure:"worker" yaml:"worker"`
	MaxConcurrentTasks int             `mapstructure:"maxConcurrentTasks" yaml:"maxConcurrentTasks"`
	HealthAddr         string          `mapstructure:"healthAddr" yaml:"healthAddr"`
	Health             NodeHealthConfig `mapstructure:"health" yaml:"health"`
}

type NodeHealthConfig struct {
	HeartbeatInterval time.Duration `mapstructure:"heartbeatInterval" yaml:"heartbeatInterval"`
	OfflineThreshold  time.Duration `mapstructure:"offlineThreshold" yaml:"offlineThreshold"`
}

type WebhookConfig struct {
	Enabled           bool     `mapstructure:"enabled" yaml:"enabled"`
	CallbackAllowlist []string `mapstructure:"callbackAllowlist" yaml:"callbackAllowlist"`
}

type AppConfig struct {
	Name        string `mapstructure:"name" yaml:"name"`
	Env         string `mapstructure:"env" yaml:"env"`
	URL         string `mapstructure:"url" yaml:"url"`
	Description string `mapstructure:"description" yaml:"description"`
}

type ServerConfig struct {
	Host            string        `mapstructure:"host" yaml:"host"`
	Port            int           `mapstructure:"port" yaml:"port"`
	ReadTimeout     time.Duration `mapstructure:"readTimeout" yaml:"readTimeout"`
	WriteTimeout    time.Duration `mapstructure:"writeTimeout" yaml:"writeTimeout"`
	IdleTimeout     time.Duration `mapstructure:"idleTimeout" yaml:"idleTimeout"`
	ShutdownTimeout time.Duration `mapstructure:"shutdownTimeout" yaml:"shutdownTimeout"`
}

type LoggingConfig struct {
	Level  string `mapstructure:"level" yaml:"level"`
	Format string `mapstructure:"format" yaml:"format"`
}

type UIConfig struct {
	DevProxyURL string `mapstructure:"devProxyURL" yaml:"devProxyURL"`
}

type WorkflowConfig struct {
	Enabled                 bool          `mapstructure:"enabled" yaml:"enabled"`
	DatabaseDriver          string        `mapstructure:"databaseDriver" yaml:"databaseDriver"`
	DatabasePath            string        `mapstructure:"databasePath" yaml:"databasePath"`
	DatabaseURL             string        `mapstructure:"databaseURL" yaml:"databaseURL"`
	PollInterval            time.Duration `mapstructure:"pollInterval" yaml:"pollInterval"`
	LeaseDuration           time.Duration `mapstructure:"leaseDuration" yaml:"leaseDuration"`
	ScriptEnabled           bool          `mapstructure:"scriptEnabled" yaml:"scriptEnabled"`
	ScriptTimeout           time.Duration `mapstructure:"scriptTimeout" yaml:"scriptTimeout"`
	ScriptMaxSourceBytes    int           `mapstructure:"scriptMaxSourceBytes" yaml:"scriptMaxSourceBytes"`
	ScriptMaxOutputBytes    int           `mapstructure:"scriptMaxOutputBytes" yaml:"scriptMaxOutputBytes"`
	ScriptMaxExecutionSteps uint64        `mapstructure:"scriptMaxExecutionSteps" yaml:"scriptMaxExecutionSteps"`
	OpenAIAPIKey            string        `mapstructure:"openaiAPIKey" yaml:"openaiAPIKey"`
}

func Default() Config {
	return Config{
		App: AppConfig{
			Name:        "Orchestra",
			Env:         "development",
			URL:         "http://localhost:8080",
			Description: "Durable workflow engine with an embedded React control plane.",
		},
		Server: ServerConfig{
			Host:            "0.0.0.0",
			Port:            8080,
			ReadTimeout:     15 * time.Second,
			WriteTimeout:    15 * time.Second,
			IdleTimeout:     60 * time.Second,
			ShutdownTimeout: 10 * time.Second,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "text",
		},
		UI: UIConfig{
			DevProxyURL: "http://localhost:5173",
		},
		Workflow: WorkflowConfig{
			Enabled:                 true,
			DatabaseDriver:          "sqlite",
			DatabasePath:            "data/workflows.db",
			DatabaseURL:             "",
			PollInterval:            1 * time.Second,
			LeaseDuration:           30 * time.Second,
			ScriptEnabled:           false,
			ScriptTimeout:           250 * time.Millisecond,
			ScriptMaxSourceBytes:    16 * 1024,
			ScriptMaxOutputBytes:    256 * 1024,
			ScriptMaxExecutionSteps: 25_000,
		},
		Webhook: WebhookConfig{
			Enabled:           true,
			CallbackAllowlist: []string{},
		},
		Node: NodeConfig{
			ID:                 "",
			Controller:         true,
			Worker:             true,
			MaxConcurrentTasks: 4,
			HealthAddr:         "0.0.0.0:8081",
			Health: NodeHealthConfig{
				HeartbeatInterval: 10 * time.Second,
				OfflineThreshold:  30 * time.Second,
			},
		},
	}
}

func (c Config) Address() string {
	return c.Server.Address()
}

func (s ServerConfig) Address() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

func SetDefaults(v *viper.Viper) {
	defaults := Default()

	v.SetDefault("app.name", defaults.App.Name)
	v.SetDefault("app.env", defaults.App.Env)
	v.SetDefault("app.url", defaults.App.URL)
	v.SetDefault("app.description", defaults.App.Description)
	v.SetDefault("server.host", defaults.Server.Host)
	v.SetDefault("server.port", defaults.Server.Port)
	v.SetDefault("server.readTimeout", defaults.Server.ReadTimeout)
	v.SetDefault("server.writeTimeout", defaults.Server.WriteTimeout)
	v.SetDefault("server.idleTimeout", defaults.Server.IdleTimeout)
	v.SetDefault("server.shutdownTimeout", defaults.Server.ShutdownTimeout)
	v.SetDefault("logging.level", defaults.Logging.Level)
	v.SetDefault("logging.format", defaults.Logging.Format)
	v.SetDefault("ui.devProxyURL", defaults.UI.DevProxyURL)
	v.SetDefault("workflow.enabled", defaults.Workflow.Enabled)
	v.SetDefault("workflow.databaseDriver", defaults.Workflow.DatabaseDriver)
	v.SetDefault("workflow.databasePath", defaults.Workflow.DatabasePath)
	v.SetDefault("workflow.databaseURL", defaults.Workflow.DatabaseURL)
	v.SetDefault("workflow.pollInterval", defaults.Workflow.PollInterval)
	v.SetDefault("workflow.leaseDuration", defaults.Workflow.LeaseDuration)
	v.SetDefault("workflow.scriptEnabled", defaults.Workflow.ScriptEnabled)
	v.SetDefault("workflow.scriptTimeout", defaults.Workflow.ScriptTimeout)
	v.SetDefault("workflow.scriptMaxSourceBytes", defaults.Workflow.ScriptMaxSourceBytes)
	v.SetDefault("workflow.scriptMaxOutputBytes", defaults.Workflow.ScriptMaxOutputBytes)
	v.SetDefault("workflow.scriptMaxExecutionSteps", defaults.Workflow.ScriptMaxExecutionSteps)
	v.SetDefault("webhook.enabled", defaults.Webhook.Enabled)
	v.SetDefault("webhook.callbackAllowlist", defaults.Webhook.CallbackAllowlist)
	v.SetDefault("node.id", defaults.Node.ID)
	v.SetDefault("node.controller", defaults.Node.Controller)
	v.SetDefault("node.worker", defaults.Node.Worker)
	v.SetDefault("node.maxConcurrentTasks", defaults.Node.MaxConcurrentTasks)
	v.SetDefault("node.healthAddr", defaults.Node.HealthAddr)
	v.SetDefault("node.health.heartbeatInterval", defaults.Node.Health.HeartbeatInterval)
	v.SetDefault("node.health.offlineThreshold", defaults.Node.Health.OfflineThreshold)
}

func Load(v *viper.Viper) (Config, error) {
	cfg := Default()
	if err := v.Unmarshal(&cfg, viper.DecodeHook(mapstructure.StringToTimeDurationHookFunc())); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	return cfg, nil
}

func InitProject(dir string, force bool) error {
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o750); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}

	files := map[string]string{
		filepath.Join(dir, "config.toml"):  DefaultConfigTOML,
		filepath.Join(dir, ".env.example"): DefaultEnvExample,
		filepath.Join(dir, ".env"):         DefaultEnvExample,
	}

	for path, contents := range files {
		if !force {
			if _, err := os.Stat(path); err == nil {
				continue
			}
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}

	keepFile := filepath.Join(dir, "data", ".gitkeep")
	if force || !fileExists(keepFile) {
		if err := os.WriteFile(keepFile, []byte{}, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", keepFile, err)
		}
	}

	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

const DefaultConfigTOML = `[app]
name        = "Orchestra"
env         = "development"
url         = "http://localhost:8080"
description = "Durable workflow engine with Go at the repo root and an embedded React UI."

[server]
host            = "0.0.0.0"
port            = 8080
readTimeout     = "15s"
writeTimeout    = "15s"
idleTimeout     = "60s"
shutdownTimeout = "10s"

[logging]
level  = "info"
format = "text"

[ui]
devProxyURL = "http://localhost:5173"

[workflow]
enabled                 = true
databasePath            = "data/workflows.db"
pollInterval            = "1s"
leaseDuration           = "30s"
scriptEnabled           = false
scriptTimeout           = "250ms"
scriptMaxSourceBytes    = 16384
scriptMaxOutputBytes    = 262144
scriptMaxExecutionSteps = 25000
# openaiAPIKey = ""   # set via APP_WORKFLOW_OPENAI_API_KEY env var or here
                   # required for the "Enhance with AI" feature in the agent editor

[webhook]
enabled = true
# callbackAllowlist = [
#   "https://your-domain\\.example\\.com/.*",
#   "http://localhost:.*",
# ]
# Regex list of URLs allowed as X-Callback-URL on POST /ext/webhook/{id}/start.
# An empty list (default) means no callback URLs are accepted.
`

const DefaultEnvExample = `APP_ENV=development
APP_APP_NAME=Orchestra
APP_SERVER_HOST=0.0.0.0
APP_SERVER_PORT=8080
APP_LOGGING_LEVEL=debug
APP_LOGGING_FORMAT=text
APP_UI_DEV_PROXY_URL=http://localhost:5173
APP_WORKFLOW_ENABLED=true
APP_WORKFLOW_DATABASE_PATH=data/workflows.db
APP_WORKFLOW_POLL_INTERVAL=1s
APP_WORKFLOW_LEASE_DURATION=30s
APP_WORKFLOW_SCRIPT_ENABLED=false
APP_WORKFLOW_SCRIPT_TIMEOUT=250ms
APP_WORKFLOW_SCRIPT_MAX_SOURCE_BYTES=16384
APP_WORKFLOW_SCRIPT_MAX_OUTPUT_BYTES=262144
APP_WORKFLOW_SCRIPT_MAX_EXECUTION_STEPS=25000
APP_WORKFLOW_OPENAI_API_KEY=
`
