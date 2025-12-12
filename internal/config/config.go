package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration for the kube-deploy server.
type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Kubernetes KubernetesConfig `yaml:"kubernetes"`
	Deploy     DeployDefaults   `yaml:"deploy"`
	Health     HealthDefaults   `yaml:"health"`
	Rollback   RollbackDefaults `yaml:"rollback"`
	Logging    LoggingConfig    `yaml:"logging"`
}

// ServerConfig holds gRPC server settings.
type ServerConfig struct {
	Host            string        `yaml:"host"`
	Port            int           `yaml:"port"`
	MaxRecvMsgSize  int           `yaml:"maxRecvMsgSize"`
	MaxSendMsgSize  int           `yaml:"maxSendMsgSize"`
	ShutdownTimeout time.Duration `yaml:"shutdownTimeout"`
}

// KubernetesConfig holds Kubernetes client settings.
type KubernetesConfig struct {
	Kubeconfig    string        `yaml:"kubeconfig"`
	Context       string        `yaml:"context"`
	InCluster     bool          `yaml:"inCluster"`
	QPS           float32       `yaml:"qps"`
	Burst         int           `yaml:"burst"`
	Timeout       time.Duration `yaml:"timeout"`
	RetryAttempts int           `yaml:"retryAttempts"`
	RetryDelay    time.Duration `yaml:"retryDelay"`
}

// DeployDefaults holds default deployment configuration.
type DeployDefaults struct {
	Strategy             string        `yaml:"strategy"`
	RolloutTimeout       time.Duration `yaml:"rolloutTimeout"`
	ProgressDeadline     time.Duration `yaml:"progressDeadline"`
	PollInterval         time.Duration `yaml:"pollInterval"`
	MaxUnavailable       int           `yaml:"maxUnavailable"`
	MaxSurge             int           `yaml:"maxSurge"`
	CanaryReplicas       int           `yaml:"canaryReplicas"`
	CanaryPercent        int           `yaml:"canaryPercent"`
	AnalysisDuration     time.Duration `yaml:"analysisDuration"`
	SuccessThreshold     int           `yaml:"successThreshold"`
	RevisionHistoryLimit int           `yaml:"revisionHistoryLimit"`
}

// HealthDefaults holds default health monitoring settings.
type HealthDefaults struct {
	CheckInterval    time.Duration `yaml:"checkInterval"`
	CheckTimeout     time.Duration `yaml:"checkTimeout"`
	FailureThreshold int           `yaml:"failureThreshold"`
	SuccessThreshold int           `yaml:"successThreshold"`
	MaxRestartCount  int           `yaml:"maxRestartCount"`
	HTTPProbeEnabled bool          `yaml:"httpProbeEnabled"`
	HTTPProbePath    string        `yaml:"httpProbePath"`
}

// RollbackDefaults holds default rollback policy settings.
type RollbackDefaults struct {
	Enabled             bool          `yaml:"enabled"`
	MaxRetries          int           `yaml:"maxRetries"`
	HealthCheckInterval time.Duration `yaml:"healthCheckInterval"`
	HealthCheckTimeout  time.Duration `yaml:"healthCheckTimeout"`
	FailureThreshold    int           `yaml:"failureThreshold"`
	SuccessThreshold    int           `yaml:"successThreshold"`
	CooldownPeriod      time.Duration `yaml:"cooldownPeriod"`
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level      string `yaml:"level"`
	Format     string `yaml:"format"`
	OutputPath string `yaml:"outputPath"`
}

// DefaultConfig returns a Config populated with sensible production defaults.
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Host:            "0.0.0.0",
			Port:            9090,
			MaxRecvMsgSize:  4 * 1024 * 1024, // 4 MB
			MaxSendMsgSize:  4 * 1024 * 1024,
			ShutdownTimeout: 15 * time.Second,
		},
		Kubernetes: KubernetesConfig{
			Kubeconfig:    "",
			Context:       "",
			InCluster:     false,
			QPS:           50,
			Burst:         100,
			Timeout:       30 * time.Second,
			RetryAttempts: 3,
			RetryDelay:    2 * time.Second,
		},
		Deploy: DeployDefaults{
			Strategy:             "rolling",
			RolloutTimeout:       300 * time.Second,
			ProgressDeadline:     600 * time.Second,
			PollInterval:         2 * time.Second,
			MaxUnavailable:       0,
			MaxSurge:             1,
			CanaryReplicas:       1,
			CanaryPercent:        10,
			AnalysisDuration:     60 * time.Second,
			SuccessThreshold:     3,
			RevisionHistoryLimit: 10,
		},
		Health: HealthDefaults{
			CheckInterval:    5 * time.Second,
			CheckTimeout:     10 * time.Second,
			FailureThreshold: 3,
			SuccessThreshold: 1,
			MaxRestartCount:  5,
			HTTPProbeEnabled: false,
			HTTPProbePath:    "/healthz",
		},
		Rollback: RollbackDefaults{
			Enabled:             true,
			MaxRetries:          2,
			HealthCheckInterval: 10 * time.Second,
			HealthCheckTimeout:  120 * time.Second,
			FailureThreshold:    3,
			SuccessThreshold:    2,
			CooldownPeriod:      60 * time.Second,
		},
		Logging: LoggingConfig{
			Level:      "info",
			Format:     "json",
			OutputPath: "stdout",
		},
	}
}

// Load reads configuration from a YAML file at the given path.
// If the file does not exist, it returns the default configuration.
// Environment variables override file values (see ApplyEnvOverrides).
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				cfg.ApplyEnvOverrides()
				return cfg, nil
			}
			return nil, fmt.Errorf("reading config file %s: %w", path, err)
		}

		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parsing config file %s: %w", path, err)
		}
	}

	cfg.ApplyEnvOverrides()

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

// ApplyEnvOverrides reads well-known environment variables and overrides
// the corresponding config fields. This allows container-native configuration
// without needing to mount a config file.
//
// Supported environment variables:
//
//	KD_SERVER_HOST            - gRPC listen host
//	KD_SERVER_PORT            - gRPC listen port
//	KD_KUBECONFIG             - path to kubeconfig file
//	KD_KUBE_CONTEXT           - kubeconfig context to use
//	KD_IN_CLUSTER             - "true" to use in-cluster config
//	KD_DEPLOY_STRATEGY        - default deploy strategy (rolling, canary)
//	KD_ROLLOUT_TIMEOUT        - rollout timeout duration (e.g., "5m")
//	KD_POLL_INTERVAL          - deployment poll interval (e.g., "2s")
//	KD_ROLLBACK_ENABLED       - "true" / "false"
//	KD_ROLLBACK_MAX_RETRIES   - max rollback retries
//	KD_HEALTH_CHECK_INTERVAL  - health check interval (e.g., "10s")
//	KD_LOG_LEVEL              - log level (debug, info, warn, error)
//	KD_LOG_FORMAT             - log format (json, console)
func (c *Config) ApplyEnvOverrides() {
	if v := os.Getenv("KD_SERVER_HOST"); v != "" {
		c.Server.Host = v
	}
	if v := os.Getenv("KD_SERVER_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			c.Server.Port = port
		}
	}
	if v := os.Getenv("KD_KUBECONFIG"); v != "" {
		c.Kubernetes.Kubeconfig = v
	}
	if v := os.Getenv("KD_KUBE_CONTEXT"); v != "" {
		c.Kubernetes.Context = v
	}
	if v := os.Getenv("KD_IN_CLUSTER"); v != "" {
		c.Kubernetes.InCluster = v == "true" || v == "1"
	}
	if v := os.Getenv("KD_DEPLOY_STRATEGY"); v != "" {
		c.Deploy.Strategy = v
	}
	if v := os.Getenv("KD_ROLLOUT_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Deploy.RolloutTimeout = d
		}
	}
	if v := os.Getenv("KD_POLL_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Deploy.PollInterval = d
		}
	}
	if v := os.Getenv("KD_ROLLBACK_ENABLED"); v != "" {
		c.Rollback.Enabled = v == "true" || v == "1"
	}
	if v := os.Getenv("KD_ROLLBACK_MAX_RETRIES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Rollback.MaxRetries = n
		}
	}
	if v := os.Getenv("KD_HEALTH_CHECK_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Health.CheckInterval = d
		}
	}
	if v := os.Getenv("KD_LOG_LEVEL"); v != "" {
		c.Logging.Level = v
	}
	if v := os.Getenv("KD_LOG_FORMAT"); v != "" {
		c.Logging.Format = v
	}
}

// Validate checks the configuration for logical errors and constraint violations.
func (c *Config) Validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server port must be between 1 and 65535, got %d", c.Server.Port)
	}

	switch c.Deploy.Strategy {
	case "rolling", "canary", "blue-green":
		// valid
	default:
		return fmt.Errorf("unsupported default deploy strategy: %q", c.Deploy.Strategy)
	}

	if c.Deploy.RolloutTimeout <= 0 {
		return fmt.Errorf("rollout timeout must be positive, got %v", c.Deploy.RolloutTimeout)
	}

	if c.Deploy.PollInterval <= 0 {
		return fmt.Errorf("poll interval must be positive, got %v", c.Deploy.PollInterval)
	}

	if c.Deploy.MaxUnavailable < 0 {
		return fmt.Errorf("maxUnavailable must be >= 0, got %d", c.Deploy.MaxUnavailable)
	}

	if c.Deploy.MaxSurge < 0 {
		return fmt.Errorf("maxSurge must be >= 0, got %d", c.Deploy.MaxSurge)
	}

	if c.Health.FailureThreshold <= 0 {
		return fmt.Errorf("health failure threshold must be positive, got %d", c.Health.FailureThreshold)
	}

	if c.Health.SuccessThreshold <= 0 {
		return fmt.Errorf("health success threshold must be positive, got %d", c.Health.SuccessThreshold)
	}

	if c.Rollback.Enabled {
		if c.Rollback.MaxRetries < 0 {
			return fmt.Errorf("rollback max retries must be >= 0, got %d", c.Rollback.MaxRetries)
		}
		if c.Rollback.HealthCheckInterval <= 0 {
			return fmt.Errorf("rollback health check interval must be positive, got %v", c.Rollback.HealthCheckInterval)
		}
		if c.Rollback.HealthCheckTimeout <= 0 {
			return fmt.Errorf("rollback health check timeout must be positive, got %v", c.Rollback.HealthCheckTimeout)
		}
	}

	switch c.Logging.Level {
	case "debug", "info", "warn", "error":
		// valid
	default:
		return fmt.Errorf("unsupported log level: %q (must be debug, info, warn, or error)", c.Logging.Level)
	}

	switch c.Logging.Format {
	case "json", "console":
		// valid
	default:
		return fmt.Errorf("unsupported log format: %q (must be json or console)", c.Logging.Format)
	}

	return nil
}

// ListenAddress returns the formatted host:port string for the gRPC server.
func (c *Config) ListenAddress() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}

// KubeconfigPath returns the effective kubeconfig path, falling back to
// the KUBECONFIG environment variable or the default ~/.kube/config path.
func (c *Config) KubeconfigPath() string {
	if c.Kubernetes.Kubeconfig != "" {
		return c.Kubernetes.Kubeconfig
	}
	if v := os.Getenv("KUBECONFIG"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home + "/.kube/config"
}
