package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/sonu/kube-deploy/internal/config"
	"github.com/sonu/kube-deploy/pkg/server"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	var (
		configPath  string
		showVersion bool
		port        int
		host        string
		logLevel    string
		logFormat   string
		kubeconfig  string
		kubeContext string
		inCluster   bool
	)

	flag.StringVar(&configPath, "config", "", "Path to configuration file (YAML)")
	flag.BoolVar(&showVersion, "version", false, "Print version information and exit")
	flag.IntVar(&port, "port", 0, "gRPC server listen port (overrides config)")
	flag.StringVar(&host, "host", "", "gRPC server listen host (overrides config)")
	flag.StringVar(&logLevel, "log-level", "", "Log level: debug, info, warn, error (overrides config)")
	flag.StringVar(&logFormat, "log-format", "", "Log format: json, console (overrides config)")
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file (overrides config and KUBECONFIG env)")
	flag.StringVar(&kubeContext, "context", "", "Kubernetes context to use (overrides config)")
	flag.BoolVar(&inCluster, "in-cluster", false, "Use in-cluster Kubernetes configuration")
	flag.Parse()

	if showVersion {
		fmt.Printf("kube-deploy-server\n")
		fmt.Printf("  version:    %s\n", version)
		fmt.Printf("  commit:     %s\n", commit)
		fmt.Printf("  build date: %s\n", buildDate)
		os.Exit(0)
	}

	// Load configuration from file (or defaults).
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Apply CLI flag overrides (flags take highest precedence).
	if port > 0 {
		cfg.Server.Port = port
	}
	if host != "" {
		cfg.Server.Host = host
	}
	if logLevel != "" {
		cfg.Logging.Level = logLevel
	}
	if logFormat != "" {
		cfg.Logging.Format = logFormat
	}
	if kubeconfig != "" {
		cfg.Kubernetes.Kubeconfig = kubeconfig
	}
	if kubeContext != "" {
		cfg.Kubernetes.Context = kubeContext
	}
	if inCluster {
		cfg.Kubernetes.InCluster = true
	}

	// Re-validate after overrides.
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid configuration after applying overrides: %v\n", err)
		os.Exit(1)
	}

	// Initialize the logger.
	logger, err := buildLogger(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		_ = logger.Sync()
	}()

	logger.Info("starting kube-deploy-server",
		zap.String("version", version),
		zap.String("commit", commit),
		zap.String("build_date", buildDate),
		zap.String("listen_address", cfg.ListenAddress()),
		zap.String("log_level", cfg.Logging.Level),
		zap.String("log_format", cfg.Logging.Format),
		zap.Bool("in_cluster", cfg.Kubernetes.InCluster),
		zap.String("kubeconfig", cfg.KubeconfigPath()),
		zap.Bool("rollback_enabled", cfg.Rollback.Enabled),
	)

	// Create the gRPC server with all components wired together.
	srv, err := server.NewServer(cfg, logger)
	if err != nil {
		logger.Fatal("failed to create server", zap.Error(err))
	}

	// Set up signal handling for graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)
	defer stop()

	// Start the server in a goroutine.
	errCh := make(chan error, 1)
	go func() {
		if err := srv.Start(); err != nil {
			errCh <- err
		}
	}()

	// Wait for either a shutdown signal or a server error.
	select {
	case <-ctx.Done():
		logger.Info("received shutdown signal, initiating graceful shutdown")
	case err := <-errCh:
		logger.Error("server encountered a fatal error", zap.Error(err))
	}

	// Perform graceful shutdown.
	shutdownStart := time.Now()
	srv.Stop()

	logger.Info("kube-deploy-server shut down successfully",
		zap.Duration("shutdown_duration", time.Since(shutdownStart)),
	)
}

// buildLogger constructs a zap.Logger based on the configuration settings.
func buildLogger(cfg *config.Config) (*zap.Logger, error) {
	var level zapcore.Level
	switch cfg.Logging.Level {
	case "debug":
		level = zapcore.DebugLevel
	case "info":
		level = zapcore.InfoLevel
	case "warn":
		level = zapcore.WarnLevel
	case "error":
		level = zapcore.ErrorLevel
	default:
		level = zapcore.InfoLevel
	}

	var zapCfg zap.Config
	switch cfg.Logging.Format {
	case "console":
		zapCfg = zap.NewDevelopmentConfig()
		zapCfg.Level = zap.NewAtomicLevelAt(level)
		zapCfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		zapCfg.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("15:04:05.000")
	default:
		zapCfg = zap.NewProductionConfig()
		zapCfg.Level = zap.NewAtomicLevelAt(level)
		zapCfg.EncoderConfig.TimeKey = "ts"
		zapCfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	}

	if cfg.Logging.OutputPath != "" && cfg.Logging.OutputPath != "stdout" {
		zapCfg.OutputPaths = []string{cfg.Logging.OutputPath}
		zapCfg.ErrorOutputPaths = []string{cfg.Logging.OutputPath}
	} else {
		zapCfg.OutputPaths = []string{"stdout"}
		zapCfg.ErrorOutputPaths = []string{"stderr"}
	}

	logger, err := zapCfg.Build(
		zap.AddCaller(),
		zap.AddStacktrace(zapcore.ErrorLevel),
		zap.Fields(zap.String("service", "kube-deploy-server")),
	)
	if err != nil {
		return nil, fmt.Errorf("building logger: %w", err)
	}

	return logger, nil
}
