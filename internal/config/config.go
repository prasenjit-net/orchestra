package config

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

type Config struct {
	App      AppConfig      `mapstructure:"app" yaml:"app"`
	Server   ServerConfig   `mapstructure:"server" yaml:"server"`
	Logging  LoggingConfig  `mapstructure:"logging" yaml:"logging"`
	UI       UIConfig       `mapstructure:"ui" yaml:"ui"`
	AI       AIConfig       `mapstructure:"ai" yaml:"ai"`
	Workflow WorkflowConfig `mapstructure:"workflow" yaml:"workflow"`
	Auth     AuthConfig     `mapstructure:"auth" yaml:"auth"`
	Webhook  WebhookConfig  `mapstructure:"webhook" yaml:"webhook"`
	Node     NodeConfig     `mapstructure:"node" yaml:"node"`

	// ConfigFilePath is the resolved path to the active config file.
	// Set by the caller after loading — not read from TOML.
	ConfigFilePath string `mapstructure:"-" yaml:"-"`
}

// AIConfig holds credentials and defaults for all AI providers.
// Configure at least one provider to use AI-powered agents and features.
type AIConfig struct {
	// HTTPProxy is an optional proxy URL used for all AI provider requests.
	// Supports credentials: http://user:pass@proxy.example.com:8080
	// Also configurable via APP_AI_HTTP_PROXY (or the standard HTTP_PROXY env var).
	HTTPProxy string `mapstructure:"httpProxy" yaml:"httpProxy"`

	// OpenAI
	OpenAIAPIKey  string `mapstructure:"openaiAPIKey" yaml:"openaiAPIKey"`
	OpenAIBaseURL string `mapstructure:"openaiBaseURL" yaml:"openaiBaseURL"`

	// Anthropic Claude
	ClaudeAPIKey     string `mapstructure:"claudeAPIKey" yaml:"claudeAPIKey"`
	ClaudeBaseURL    string `mapstructure:"claudeBaseURL" yaml:"claudeBaseURL"`
	ClaudeAPIVersion string `mapstructure:"claudeAPIVersion" yaml:"claudeAPIVersion"`

	// GitHub Copilot
	CopilotOAuthToken          string `mapstructure:"copilotOAuthToken" yaml:"copilotOAuthToken"`
	CopilotBaseURL             string `mapstructure:"copilotBaseURL" yaml:"copilotBaseURL"`
	CopilotTokenURL            string `mapstructure:"copilotTokenURL" yaml:"copilotTokenURL"`
	CopilotEditorVersion       string `mapstructure:"copilotEditorVersion" yaml:"copilotEditorVersion"`
	CopilotEditorPluginVersion string `mapstructure:"copilotEditorPluginVersion" yaml:"copilotEditorPluginVersion"`
	CopilotIntegrationID       string `mapstructure:"copilotIntegrationID" yaml:"copilotIntegrationID"`
	CopilotOpenAIIntent        string `mapstructure:"copilotOpenAIIntent" yaml:"copilotOpenAIIntent"`
}

type NodeConfig struct {
	ID                 string           `mapstructure:"id" yaml:"id"`
	Controller         bool             `mapstructure:"controller" yaml:"controller"`
	Worker             bool             `mapstructure:"worker" yaml:"worker"`
	MaxConcurrentTasks int              `mapstructure:"maxConcurrentTasks" yaml:"maxConcurrentTasks"`
	HealthAddr         string           `mapstructure:"healthAddr" yaml:"healthAddr"`
	Health             NodeHealthConfig `mapstructure:"health" yaml:"health"`
}

type NodeHealthConfig struct {
	HeartbeatInterval time.Duration `mapstructure:"heartbeatInterval" yaml:"heartbeatInterval"`
	OfflineThreshold  time.Duration `mapstructure:"offlineThreshold" yaml:"offlineThreshold"`
}

type WebhookConfig struct {
	Enabled            bool     `mapstructure:"enabled" yaml:"enabled"`
	AuthenticationMode string   `mapstructure:"authenticationMode" yaml:"authenticationMode"`
	CallbackAllowlist  []string `mapstructure:"callbackAllowlist" yaml:"callbackAllowlist"`
}

type AuthConfig struct {
	SessionIdleTimeout     time.Duration    `mapstructure:"sessionIdleTimeout" yaml:"sessionIdleTimeout"`
	SessionAbsoluteTimeout time.Duration    `mapstructure:"sessionAbsoluteTimeout" yaml:"sessionAbsoluteTimeout"`
	CookieSecure           string           `mapstructure:"cookieSecure" yaml:"cookieSecure"`
	BootstrapOutputPath    string           `mapstructure:"bootstrapOutputPath" yaml:"bootstrapOutputPath"`
	AuditRetention         time.Duration    `mapstructure:"auditRetention" yaml:"auditRetention"`
	TrustedProxyCIDRs      []string         `mapstructure:"trustedProxyCIDRs" yaml:"trustedProxyCIDRs"`
	APIKeys                APIKeyAuthConfig `mapstructure:"apiKeys" yaml:"apiKeys"`
}

type APIKeyAuthConfig struct {
	DefaultTTL        time.Duration `mapstructure:"defaultTTL" yaml:"defaultTTL"`
	MaximumTTL        time.Duration `mapstructure:"maximumTTL" yaml:"maximumTTL"`
	UsageWriteWindow  time.Duration `mapstructure:"usageWriteWindow" yaml:"usageWriteWindow"`
	RequestsPerMinute int           `mapstructure:"requestsPerMinute" yaml:"requestsPerMinute"`
	Burst             int           `mapstructure:"burst" yaml:"burst"`
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
		AI: AIConfig{
			ClaudeAPIVersion:           "2023-06-01",
			CopilotEditorVersion:       "vscode/1.96.0",
			CopilotEditorPluginVersion: "copilot/1.155.0",
			CopilotIntegrationID:       "vscode-chat",
			CopilotOpenAIIntent:        "conversation-panel",
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
		Auth: AuthConfig{
			SessionIdleTimeout:     30 * time.Minute,
			SessionAbsoluteTimeout: 8 * time.Hour,
			CookieSecure:           "auto",
			BootstrapOutputPath:    "data/bootstrap-admin.txt",
			AuditRetention:         90 * 24 * time.Hour,
			TrustedProxyCIDRs:      []string{},
			APIKeys: APIKeyAuthConfig{
				DefaultTTL:        90 * 24 * time.Hour,
				MaximumTTL:        365 * 24 * time.Hour,
				UsageWriteWindow:  5 * time.Minute,
				RequestsPerMinute: 60,
				Burst:             20,
			},
		},
		Webhook: WebhookConfig{
			Enabled:            true,
			AuthenticationMode: "required",
			CallbackAllowlist:  []string{},
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
	v.SetDefault("auth.sessionIdleTimeout", defaults.Auth.SessionIdleTimeout)
	v.SetDefault("auth.sessionAbsoluteTimeout", defaults.Auth.SessionAbsoluteTimeout)
	v.SetDefault("auth.cookieSecure", defaults.Auth.CookieSecure)
	v.SetDefault("auth.bootstrapOutputPath", defaults.Auth.BootstrapOutputPath)
	v.SetDefault("auth.auditRetention", defaults.Auth.AuditRetention)
	v.SetDefault("auth.trustedProxyCIDRs", defaults.Auth.TrustedProxyCIDRs)
	v.SetDefault("auth.apiKeys.defaultTTL", defaults.Auth.APIKeys.DefaultTTL)
	v.SetDefault("auth.apiKeys.maximumTTL", defaults.Auth.APIKeys.MaximumTTL)
	v.SetDefault("auth.apiKeys.usageWriteWindow", defaults.Auth.APIKeys.UsageWriteWindow)
	v.SetDefault("auth.apiKeys.requestsPerMinute", defaults.Auth.APIKeys.RequestsPerMinute)
	v.SetDefault("auth.apiKeys.burst", defaults.Auth.APIKeys.Burst)
	v.SetDefault("ai.claudeAPIVersion", defaults.AI.ClaudeAPIVersion)
	v.SetDefault("ai.copilotEditorVersion", defaults.AI.CopilotEditorVersion)
	v.SetDefault("ai.copilotEditorPluginVersion", defaults.AI.CopilotEditorPluginVersion)
	v.SetDefault("ai.copilotIntegrationID", defaults.AI.CopilotIntegrationID)
	v.SetDefault("ai.copilotOpenAIIntent", defaults.AI.CopilotOpenAIIntent)
	v.SetDefault("webhook.enabled", defaults.Webhook.Enabled)
	v.SetDefault("webhook.authenticationMode", defaults.Webhook.AuthenticationMode)
	v.SetDefault("webhook.callbackAllowlist", defaults.Webhook.CallbackAllowlist)
	v.SetDefault("node.id", defaults.Node.ID)
	v.SetDefault("node.controller", defaults.Node.Controller)
	v.SetDefault("node.worker", defaults.Node.Worker)
	v.SetDefault("node.maxConcurrentTasks", defaults.Node.MaxConcurrentTasks)
	v.SetDefault("node.healthAddr", defaults.Node.HealthAddr)
	v.SetDefault("node.health.heartbeatInterval", defaults.Node.Health.HeartbeatInterval)
	v.SetDefault("node.health.offlineThreshold", defaults.Node.Health.OfflineThreshold)

	// Bind user-friendly env var names for fields where Viper's automatic
	// camelCase→env mapping produces hard-to-type names.  Each BindEnv call
	// below also keeps the auto-mapped name working, so users can use either.
	_ = v.BindEnv("workflow.databaseURL", "APP_WORKFLOW_DATABASE_URL", "APP_WORKFLOW_DATABASEURL")
	_ = v.BindEnv("auth.sessionIdleTimeout", "APP_AUTH_SESSION_IDLE_TIMEOUT")
	_ = v.BindEnv("auth.sessionAbsoluteTimeout", "APP_AUTH_SESSION_ABSOLUTE_TIMEOUT")
	_ = v.BindEnv("auth.cookieSecure", "APP_AUTH_COOKIE_SECURE")
	_ = v.BindEnv("auth.bootstrapOutputPath", "APP_AUTH_BOOTSTRAP_OUTPUT_PATH")
	_ = v.BindEnv("auth.auditRetention", "APP_AUTH_AUDIT_RETENTION")
	_ = v.BindEnv("auth.apiKeys.defaultTTL", "APP_AUTH_API_KEYS_DEFAULT_TTL")
	_ = v.BindEnv("auth.apiKeys.maximumTTL", "APP_AUTH_API_KEYS_MAXIMUM_TTL")
	_ = v.BindEnv("auth.apiKeys.usageWriteWindow", "APP_AUTH_API_KEYS_USAGE_WRITE_WINDOW")
	_ = v.BindEnv("auth.apiKeys.requestsPerMinute", "APP_AUTH_API_KEYS_REQUESTS_PER_MINUTE")
	_ = v.BindEnv("auth.apiKeys.burst", "APP_AUTH_API_KEYS_BURST")
	_ = v.BindEnv("webhook.authenticationMode", "APP_WEBHOOK_AUTHENTICATION_MODE")
	_ = v.BindEnv("ai.httpProxy", "APP_AI_HTTP_PROXY")
	_ = v.BindEnv("ai.openaiAPIKey", "APP_AI_OPENAI_API_KEY")
	_ = v.BindEnv("ai.openaiBaseURL", "APP_AI_OPENAI_BASE_URL")
	_ = v.BindEnv("ai.claudeAPIKey", "APP_AI_CLAUDE_API_KEY")
	_ = v.BindEnv("ai.claudeBaseURL", "APP_AI_CLAUDE_BASE_URL")
	_ = v.BindEnv("ai.claudeAPIVersion", "APP_AI_CLAUDE_API_VERSION")
	_ = v.BindEnv("ai.copilotOAuthToken", "APP_AI_COPILOT_OAUTH_TOKEN")
	_ = v.BindEnv("ai.copilotBaseURL", "APP_AI_COPILOT_BASE_URL")
	_ = v.BindEnv("ai.copilotTokenURL", "APP_AI_COPILOT_TOKEN_URL")
	_ = v.BindEnv("ai.copilotEditorVersion", "APP_AI_COPILOT_EDITOR_VERSION")
	_ = v.BindEnv("ai.copilotEditorPluginVersion", "APP_AI_COPILOT_EDITOR_PLUGIN_VERSION")
	_ = v.BindEnv("ai.copilotIntegrationID", "APP_AI_COPILOT_INTEGRATION_ID")
	_ = v.BindEnv("ai.copilotOpenAIIntent", "APP_AI_COPILOT_OPENAI_INTENT")
	// Legacy workflow.* bindings kept for backwards compatibility.
	_ = v.BindEnv("ai.openaiAPIKey", "APP_WORKFLOW_OPENAI_API_KEY")
	_ = v.BindEnv("ai.claudeAPIKey", "APP_WORKFLOW_CLAUDE_API_KEY")
	_ = v.BindEnv("ai.copilotOAuthToken", "APP_WORKFLOW_COPILOT_OAUTH_TOKEN")
}

func Load(v *viper.Viper) (Config, error) {
	cfg := Default()
	if err := v.Unmarshal(&cfg, viper.DecodeHook(mapstructure.StringToTimeDurationHookFunc())); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if raw := strings.TrimSpace(os.Getenv("APP_AUTH_TRUSTED_PROXY_CIDRS")); raw != "" {
		cfg.Auth.TrustedProxyCIDRs = strings.Split(raw, ",")
	}
	if err := validateAuthConfig(cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func validateAuthConfig(cfg Config) error {
	if cfg.Auth.SessionIdleTimeout <= 0 || cfg.Auth.SessionAbsoluteTimeout <= 0 || cfg.Auth.SessionAbsoluteTimeout < cfg.Auth.SessionIdleTimeout {
		return errorsConfig("auth session timeouts must be positive and absolute timeout must be at least the idle timeout")
	}
	switch strings.ToLower(cfg.Auth.CookieSecure) {
	case "auto", "true", "required", "false", "disabled":
	default:
		return errorsConfig("auth.cookieSecure must be auto, required, or disabled")
	}
	if cfg.Auth.AuditRetention < 0 {
		return errorsConfig("auth.auditRetention cannot be negative")
	}
	if cfg.Auth.APIKeys.DefaultTTL <= 0 || cfg.Auth.APIKeys.MaximumTTL < cfg.Auth.APIKeys.DefaultTTL || cfg.Auth.APIKeys.UsageWriteWindow <= 0 {
		return errorsConfig("auth API key durations are invalid")
	}
	if cfg.Auth.APIKeys.RequestsPerMinute <= 0 || cfg.Auth.APIKeys.Burst <= 0 {
		return errorsConfig("auth API key rate limits must be positive")
	}
	for _, raw := range cfg.Auth.TrustedProxyCIDRs {
		if _, err := netip.ParsePrefix(strings.TrimSpace(raw)); err != nil {
			return fmt.Errorf("invalid auth.trustedProxyCIDRs entry %q: %w", raw, err)
		}
	}
	if cfg.Webhook.AuthenticationMode != "required" && cfg.Webhook.AuthenticationMode != "audit" {
		return errorsConfig("webhook.authenticationMode must be required or audit")
	}
	return nil
}

func errorsConfig(message string) error {
	return fmt.Errorf("invalid config: %s", message)
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

# ---------------------------------------------------------------------------
# AI providers — configure at least one to use AI agents and prompt features.
# When an agent has no explicit provider set, Orchestra auto-selects the first
# configured provider in preference order: openai → copilot → claude.
# ---------------------------------------------------------------------------
[ai]

# Optional HTTP proxy for all AI provider requests.
# Supports credentials: http://user:pass@proxy.example.com:8080
# httpProxy = ""                              # env: APP_AI_HTTP_PROXY

# --- OpenAI ---
# openaiAPIKey  = ""                          # env: APP_AI_OPENAI_API_KEY
# openaiBaseURL = ""                          # env: APP_AI_OPENAI_BASE_URL  (optional: Azure OpenAI / proxy)

# --- Anthropic Claude ---
# claudeAPIKey     = ""                       # env: APP_AI_CLAUDE_API_KEY
# claudeBaseURL    = ""                       # env: APP_AI_CLAUDE_BASE_URL  (optional: proxy)
# claudeAPIVersion = "2023-06-01"             # env: APP_AI_CLAUDE_API_VERSION

# --- GitHub Copilot ---
# copilotOAuthToken = ""                      # env: APP_AI_COPILOT_OAUTH_TOKEN  (GitHub OAuth token: gho_...)
# copilotBaseURL    = ""                      # env: APP_AI_COPILOT_BASE_URL      (optional: override completions URL)
# copilotTokenURL   = ""                      # env: APP_AI_COPILOT_TOKEN_URL     (optional: override token exchange URL)
# The fields below have sensible defaults; only override if needed.
# copilotEditorVersion       = "vscode/1.96.0"     # env: APP_AI_COPILOT_EDITOR_VERSION
# copilotEditorPluginVersion = "copilot/1.155.0"   # env: APP_AI_COPILOT_EDITOR_PLUGIN_VERSION
# copilotIntegrationID       = "vscode-chat"        # env: APP_AI_COPILOT_INTEGRATION_ID
# copilotOpenAIIntent        = "conversation-panel" # env: APP_AI_COPILOT_OPENAI_INTENT

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

[auth]
sessionIdleTimeout     = "30m"
sessionAbsoluteTimeout = "8h"
cookieSecure           = "auto"
bootstrapOutputPath    = "data/bootstrap-admin.txt"
auditRetention         = "2160h"
trustedProxyCIDRs      = []

[auth.apiKeys]
defaultTTL        = "2160h"
maximumTTL        = "8760h"
usageWriteWindow  = "5m"
requestsPerMinute = 60
burst             = 20

[webhook]
enabled            = true
authenticationMode = "required"
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
APP_AUTH_SESSION_IDLE_TIMEOUT=30m
APP_AUTH_SESSION_ABSOLUTE_TIMEOUT=8h
APP_AUTH_COOKIE_SECURE=auto
APP_AUTH_BOOTSTRAP_OUTPUT_PATH=data/bootstrap-admin.txt
APP_AUTH_AUDIT_RETENTION=2160h
APP_AUTH_TRUSTED_PROXY_CIDRS=
APP_AUTH_API_KEYS_DEFAULT_TTL=2160h
APP_AUTH_API_KEYS_MAXIMUM_TTL=8760h
APP_AUTH_API_KEYS_USAGE_WRITE_WINDOW=5m
APP_AUTH_API_KEYS_REQUESTS_PER_MINUTE=60
APP_AUTH_API_KEYS_BURST=20
APP_AUTH_INITIAL_ADMIN_USERNAME=admin
# Prefer APP_AUTH_INITIAL_ADMIN_PASSWORD_FILE in production.
APP_AUTH_INITIAL_ADMIN_PASSWORD_FILE=
APP_WEBHOOK_AUTHENTICATION_MODE=required
APP_AI_OPENAI_API_KEY=
APP_AI_CLAUDE_API_KEY=
APP_AI_COPILOT_OAUTH_TOKEN=
APP_WORKFLOW_DATABASE_URL=
`
