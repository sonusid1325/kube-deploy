package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/sonu/kube-deploy/internal/config"
	"github.com/sonu/kube-deploy/internal/tui"
	"github.com/sonu/kube-deploy/pkg/deployer"
	"github.com/sonu/kube-deploy/pkg/health"
	"github.com/sonu/kube-deploy/pkg/k8s"
	"github.com/sonu/kube-deploy/pkg/models"
	"github.com/sonu/kube-deploy/pkg/rollback"
	"github.com/sonu/kube-deploy/pkg/server"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

// Global flags shared across subcommands.
var (
	globalKubeconfig  string
	globalKubeContext string
	globalInCluster   bool
	globalNamespace   string
	globalConfigPath  string
	globalLogLevel    string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "kdctl",
		Short: "⎈ kdctl — zero-downtime Kubernetes deployment pipeline",
		Long: `kdctl is a unified CLI tool for managing zero-downtime Kubernetes
deployments with rolling updates, canary deployments, health monitoring,
and automated rollback.

Run without a subcommand (or use "kdctl ui") to launch the interactive
Bubble Tea TUI. Use subcommands for non-interactive / scriptable usage.

Examples:
  # Launch the interactive TUI
  kdctl -n default -d goserver

  # Deploy from the CLI (non-interactive)
  kdctl deploy -n default -d goserver --image goserver:v2

  # Check deployment status
  kdctl status -n default -d goserver

  # Start the gRPC server
  kdctl start --port 9090`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          runTUI,
	}

	// Global persistent flags.
	pf := rootCmd.PersistentFlags()
	pf.StringVarP(&globalNamespace, "namespace", "n", "default",
		"Kubernetes namespace")
	pf.StringVar(&globalKubeconfig, "kubeconfig", "",
		"Path to kubeconfig file (defaults to ~/.kube/config or KUBECONFIG env)")
	pf.StringVar(&globalKubeContext, "context", "",
		"Kubernetes context to use")
	pf.BoolVar(&globalInCluster, "in-cluster", false,
		"Use in-cluster Kubernetes config (when running inside a pod)")
	pf.StringVar(&globalConfigPath, "config", "",
		"Path to kdctl config file (YAML)")
	pf.StringVar(&globalLogLevel, "log-level", "",
		"Log level: debug, info, warn, error")

	// The root command needs --deployment for the TUI entrypoint.
	rootCmd.Flags().StringP("deployment", "d", "",
		"Name of the Kubernetes Deployment to manage (required for TUI)")

	// Register subcommands.
	rootCmd.AddCommand(
		newUICmd(),
		newStartCmd(),
		newDeployCmd(),
		newStatusCmd(),
		newHealthCmd(),
		newRollbackCmd(),
		newHistoryCmd(),
		newVersionCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "\n  Error: %v\n\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// Shared helpers
// ============================================================================

// loadConfig loads the config file and applies CLI-level overrides.
func loadConfig() (*config.Config, error) {
	cfg, err := config.Load(globalConfigPath)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	if globalKubeconfig != "" {
		cfg.Kubernetes.Kubeconfig = globalKubeconfig
	}
	if globalKubeContext != "" {
		cfg.Kubernetes.Context = globalKubeContext
	}
	if globalInCluster {
		cfg.Kubernetes.InCluster = true
	}
	if globalLogLevel != "" {
		cfg.Logging.Level = globalLogLevel
	}
	return cfg, nil
}

// buildK8sClient creates a Kubernetes client from the current config.
func buildK8sClient(cfg *config.Config, logger *zap.Logger) (*k8s.Client, error) {
	k8sCfg := k8s.ClientConfig{
		Kubeconfig:    cfg.KubeconfigPath(),
		Context:       cfg.Kubernetes.Context,
		InCluster:     cfg.Kubernetes.InCluster,
		QPS:           cfg.Kubernetes.QPS,
		Burst:         cfg.Kubernetes.Burst,
		Timeout:       cfg.Kubernetes.Timeout,
		RetryAttempts: cfg.Kubernetes.RetryAttempts,
		RetryDelay:    cfg.Kubernetes.RetryDelay,
	}
	return k8s.NewClient(k8sCfg, logger)
}

// buildLogger constructs a zap.Logger based on config.
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
		zap.Fields(zap.String("service", "kdctl")),
	)
	if err != nil {
		return nil, fmt.Errorf("building logger: %w", err)
	}
	return logger, nil
}

// ============================================================================
// TUI (root command & "ui" subcommand)
// ============================================================================

// runTUI is the handler for both the root command (when --deployment is given)
// and the explicit "ui" subcommand.
func runTUI(cmd *cobra.Command, args []string) error {
	deployment, _ := cmd.Flags().GetString("deployment")
	if deployment == "" {
		return fmt.Errorf("--deployment (-d) is required for the TUI\n\nUsage:\n  kdctl -n <namespace> -d <deployment>\n  kdctl ui -n <namespace> -d <deployment>\n\nOr use a subcommand:\n  kdctl deploy -d <deployment> --image <image>\n  kdctl status -d <deployment>")
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	model, err := tui.NewModel(globalNamespace, deployment, cfg, nil)
	if err != nil {
		return fmt.Errorf("initializing TUI: %w", err)
	}

	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	model.SetProgram(p)

	_, err = p.Run()
	if err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}
	return nil
}

func newUICmd() *cobra.Command {
	var deployment string

	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Launch the interactive Bubble Tea TUI",
		Long: `Launch the interactive terminal UI for managing Kubernetes deployments.

The TUI provides tabs for Status, Health, Deploy, Rollback, History, and Logs
with real-time streaming and keyboard-driven navigation.

Examples:
  kdctl ui -n default -d goserver
  kdctl ui -d myapp --kubeconfig ~/.kube/prod.yaml --context prod-cluster
  kdctl ui -d myapp --in-cluster`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deployment == "" {
				return fmt.Errorf("--deployment (-d) is required")
			}

			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			model, err := tui.NewModel(globalNamespace, deployment, cfg, nil)
			if err != nil {
				return fmt.Errorf("initializing TUI: %w", err)
			}

			p := tea.NewProgram(
				model,
				tea.WithAltScreen(),
				tea.WithMouseCellMotion(),
			)
			model.SetProgram(p)

			_, err = p.Run()
			if err != nil {
				return fmt.Errorf("TUI error: %w", err)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&deployment, "deployment", "d", "",
		"Name of the Kubernetes Deployment to manage (required)")
	_ = cmd.MarkFlagRequired("deployment")

	return cmd
}

// ============================================================================
// start — gRPC server
// ============================================================================

func newStartCmd() *cobra.Command {
	var (
		port      int
		host      string
		logFormat string
	)

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the kube-deploy gRPC server",
		Long: `Start the kube-deploy gRPC server for remote or multi-user deployment
management. Clients can connect using the gRPC API.

Examples:
  kdctl start
  kdctl start --port 9090 --log-level debug --log-format console
  kdctl start --in-cluster --port 9090`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			// Apply start-specific overrides.
			if port > 0 {
				cfg.Server.Port = port
			}
			if host != "" {
				cfg.Server.Host = host
			}
			if logFormat != "" {
				cfg.Logging.Format = logFormat
			}

			if err := cfg.Validate(); err != nil {
				return fmt.Errorf("invalid configuration: %w", err)
			}

			logger, err := buildLogger(cfg)
			if err != nil {
				return fmt.Errorf("initializing logger: %w", err)
			}
			defer func() { _ = logger.Sync() }()

			logger.Info("starting kube-deploy server",
				zap.String("version", version),
				zap.String("commit", commit),
				zap.String("build_date", buildDate),
				zap.String("listen_address", cfg.ListenAddress()),
				zap.Bool("in_cluster", cfg.Kubernetes.InCluster),
				zap.String("kubeconfig", cfg.KubeconfigPath()),
				zap.Bool("rollback_enabled", cfg.Rollback.Enabled),
			)

			srv, err := server.NewServer(cfg, logger)
			if err != nil {
				return fmt.Errorf("creating server: %w", err)
			}

			// Signal handling for graceful shutdown.
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)
			defer stop()

			errCh := make(chan error, 1)
			go func() {
				if err := srv.Start(); err != nil {
					errCh <- err
				}
			}()

			select {
			case <-ctx.Done():
				logger.Info("received shutdown signal")
			case err := <-errCh:
				return fmt.Errorf("server error: %w", err)
			}

			shutdownStart := time.Now()
			srv.Stop()
			logger.Info("server shut down",
				zap.Duration("shutdown_duration", time.Since(shutdownStart)),
			)
			return nil
		},
	}

	cmd.Flags().IntVar(&port, "port", 0, "gRPC server listen port (overrides config, default 9090)")
	cmd.Flags().StringVar(&host, "host", "", "gRPC server listen host (overrides config)")
	cmd.Flags().StringVar(&logFormat, "log-format", "", "Log format: json, console")

	return cmd
}

// ============================================================================
// deploy — non-interactive deployment
// ============================================================================

func newDeployCmd() *cobra.Command {
	var (
		deployment     string
		container      string
		image          string
		strategy       string
		maxUnavail     int
		maxSurge       int
		canaryReplicas int
		analysisDur    time.Duration
		successThresh  int
		dryRun         bool
		deployTimeout  time.Duration
	)

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy a new version of a Kubernetes workload",
		Long: `Initiate a zero-downtime deployment and stream progress events in real time.
This command talks directly to the Kubernetes API — no server required.

Examples:
  # Rolling update
  kdctl deploy -n default -d goserver --image goserver:v2

  # Canary deployment
  kdctl deploy -n default -d goserver --image goserver:v2 --strategy canary \
    --canary-replicas 1 --analysis-duration 60s

  # Dry run
  kdctl deploy -n default -d goserver --image goserver:v2 --dry-run`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deployment == "" || image == "" {
				return fmt.Errorf("--deployment and --image are required")
			}

			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			logger, err := buildLogger(cfg)
			if err != nil {
				return fmt.Errorf("initializing logger: %w", err)
			}
			defer func() { _ = logger.Sync() }()

			client, err := buildK8sClient(cfg, logger)
			if err != nil {
				return fmt.Errorf("creating k8s client: %w", err)
			}

			engine := deployer.NewEngine(client, cfg, logger)

			deployID := fmt.Sprintf("deploy-%s-%d", deployment, time.Now().Unix())

			var strat models.DeployStrategy
			var req models.DeploymentRequest

			switch strings.ToLower(strategy) {
			case "rolling", "":
				strat = models.StrategyRolling
				req = models.DeploymentRequest{
					DeployID: deployID,
					Target: models.DeploymentTarget{
						Namespace:      globalNamespace,
						DeploymentName: deployment,
						ContainerName:  container,
						Image:          image,
					},
					Strategy: strat,
					DryRun:   dryRun,
					RollingConfig: models.RollingUpdateConfig{
						MaxUnavailable: maxUnavail,
						MaxSurge:       maxSurge,
					},
				}
			case "canary":
				strat = models.StrategyCanary
				req = models.DeploymentRequest{
					DeployID: deployID,
					Target: models.DeploymentTarget{
						Namespace:      globalNamespace,
						DeploymentName: deployment,
						ContainerName:  container,
						Image:          image,
					},
					Strategy: strat,
					DryRun:   dryRun,
					CanaryConfig: models.CanaryConfig{
						CanaryReplicas:   canaryReplicas,
						AnalysisDuration: analysisDur,
						SuccessThreshold: successThresh,
					},
				}
			default:
				return fmt.Errorf("unsupported strategy: %q (use rolling or canary)", strategy)
			}
			req.ApplyDefaults()

			if dryRun {
				printHeader("DRY RUN MODE")
			}
			printHeader("Deploying %s/%s → %s (strategy: %s)",
				globalNamespace, deployment, image, strategy)
			fmt.Printf("  Deploy ID: %s\n\n", deployID)

			ctx, cancel := context.WithTimeout(context.Background(), deployTimeout)
			defer cancel()

			eventsCh, err := engine.Deploy(ctx, req)
			if err != nil {
				return fmt.Errorf("deploy failed: %w", err)
			}

			var lastPhase models.DeploymentPhase
			var finalErr error
			for event := range eventsCh {
				printDeployEvent(event, lastPhase)
				lastPhase = event.Phase

				if event.IsTerminal {
					fmt.Println()
					if event.Phase == models.PhaseCompleted {
						printSuccess("Deployment completed successfully!")
					} else if event.Phase == models.PhaseFailed {
						printError("Deployment failed: %s", event.ErrorDetail)
						finalErr = fmt.Errorf("deployment failed")
					} else if event.Phase == models.PhaseRolledBack {
						printWarning("Deployment was rolled back: %s", event.Message)
					}
				}
			}

			return finalErr
		},
	}

	cmd.Flags().StringVarP(&deployment, "deployment", "d", "", "Deployment name (required)")
	cmd.Flags().StringVarP(&container, "container", "c", "", "Container name (if multi-container pod)")
	cmd.Flags().StringVar(&image, "image", "", "Target container image (required)")
	cmd.Flags().StringVar(&strategy, "strategy", "rolling", "Deployment strategy: rolling, canary")
	cmd.Flags().IntVar(&maxUnavail, "max-unavailable", 0, "Max unavailable pods during rolling update")
	cmd.Flags().IntVar(&maxSurge, "max-surge", 1, "Max surge pods during rolling update")
	cmd.Flags().IntVar(&canaryReplicas, "canary-replicas", 1, "Number of canary replicas")
	cmd.Flags().DurationVar(&analysisDur, "analysis-duration", 60*time.Second, "Canary health analysis duration")
	cmd.Flags().IntVar(&successThresh, "success-threshold", 3, "Consecutive successes for canary promotion")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview changes without applying")
	cmd.Flags().DurationVar(&deployTimeout, "timeout", 5*time.Minute, "Deployment timeout")

	_ = cmd.MarkFlagRequired("deployment")
	_ = cmd.MarkFlagRequired("image")

	return cmd
}

// ============================================================================
// status — deployment status
// ============================================================================

func newStatusCmd() *cobra.Command {
	var (
		deployment string
		watch      bool
		interval   time.Duration
	)

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Get the current status of a deployment",
		Long: `Query the current state of a Kubernetes deployment, including replica
counts, pod-level details, conditions, and health status.

Examples:
  kdctl status -n default -d goserver
  kdctl status -n default -d goserver --watch
  kdctl status -n default -d goserver --watch --interval 3s`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deployment == "" {
				return fmt.Errorf("--deployment is required")
			}

			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			client, err := buildK8sClient(cfg, zap.NewNop())
			if err != nil {
				return fmt.Errorf("creating k8s client: %w", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if !watch {
				return printDeploymentStatus(ctx, client, globalNamespace, deployment)
			}

			// Watch mode: poll at interval.
			if err := printDeploymentStatus(ctx, client, globalNamespace, deployment); err != nil {
				return err
			}

			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

			for {
				select {
				case <-sigCh:
					fmt.Println("\n  Stopped watching.")
					return nil
				case <-ticker.C:
					fmt.Println()
					wCtx, wCancel := context.WithTimeout(context.Background(), 10*time.Second)
					if err := printDeploymentStatus(wCtx, client, globalNamespace, deployment); err != nil {
						fmt.Fprintf(os.Stderr, "  ✗ Error: %v\n", err)
					}
					wCancel()
				}
			}
		},
	}

	cmd.Flags().StringVarP(&deployment, "deployment", "d", "", "Deployment name (required)")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Continuously watch deployment status")
	cmd.Flags().DurationVarP(&interval, "interval", "i", 2*time.Second, "Poll interval for watch mode")

	_ = cmd.MarkFlagRequired("deployment")

	return cmd
}

func printDeploymentStatus(ctx context.Context, client *k8s.Client, namespace, deployment string) error {
	deploy, err := client.GetDeployment(ctx, namespace, deployment)
	if err != nil {
		return fmt.Errorf("fetching deployment: %w", err)
	}

	// Derive image.
	image := ""
	for _, c := range deploy.Spec.Template.Spec.Containers {
		image = c.Image
		break
	}

	strategyType := "RollingUpdate"
	if deploy.Spec.Strategy.Type != "" {
		strategyType = string(deploy.Spec.Strategy.Type)
	}

	desired := int32(1)
	if deploy.Spec.Replicas != nil {
		desired = *deploy.Spec.Replicas
	}

	// Determine phase.
	phase := "COMPLETED"
	if deploy.Status.ReadyReplicas < desired {
		phase = "IN_PROGRESS"
	}
	if deploy.Status.UnavailableReplicas > 0 {
		phase = "IN_PROGRESS"
	}

	// Determine health.
	healthStatus := "HEALTHY"
	if deploy.Status.ReadyReplicas == 0 && desired > 0 {
		healthStatus = "UNHEALTHY"
	} else if deploy.Status.ReadyReplicas < desired {
		healthStatus = "DEGRADED"
	}

	// Parse revision from annotation.
	revision := int64(0)
	if v, ok := deploy.Annotations["deployment.kubernetes.io/revision"]; ok {
		var r int64
		if _, scanErr := fmt.Sscanf(v, "%d", &r); scanErr == nil {
			revision = r
		}
	}

	// Last updated.
	var lastUpdated time.Time
	for _, cond := range deploy.Status.Conditions {
		if cond.LastUpdateTime.After(lastUpdated) {
			lastUpdated = cond.LastUpdateTime.Time
		}
	}

	printHeader("Deployment Status: %s/%s", namespace, deployment)
	fmt.Printf("  Phase:           %s\n", formatPhase(phase))
	fmt.Printf("  Image:           %s\n", image)
	fmt.Printf("  Strategy:        %s\n", strategyType)
	fmt.Printf("  Replicas:        %d/%d ready, %d updated, %d available\n",
		deploy.Status.ReadyReplicas, desired,
		deploy.Status.UpdatedReplicas, deploy.Status.AvailableReplicas)
	fmt.Printf("  Revision:        %d\n", revision)
	fmt.Printf("  Health:          %s\n", formatHealth(healthStatus))
	if !lastUpdated.IsZero() {
		fmt.Printf("  Last Updated:    %s\n", lastUpdated.Local().Format("2006-01-02 15:04:05"))
	}

	// Conditions.
	if len(deploy.Status.Conditions) > 0 {
		fmt.Printf("\n  Conditions:\n")
		for _, cond := range deploy.Status.Conditions {
			fmt.Printf("    • %s=%s: %s\n", cond.Type, string(cond.Status), cond.Message)
		}
	}

	// Pods.
	pods, err := client.GetDeploymentPods(ctx, namespace, deployment)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ⚠ Could not fetch pods: %v\n", err)
		return nil
	}

	if len(pods) > 0 {
		fmt.Printf("\n  Pods:\n")
		fmt.Printf("    %-45s %-12s %-7s %-10s %s\n", "NAME", "STATUS", "READY", "RESTARTS", "IMAGE")
		for _, pod := range pods {
			ready := "✗"
			podImage := ""
			restarts := int32(0)
			msg := ""
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.Ready {
					ready = "✓"
				}
				restarts = cs.RestartCount
				podImage = cs.Image
				if cs.State.Waiting != nil {
					msg = cs.State.Waiting.Reason
					if cs.State.Waiting.Message != "" {
						msg += ": " + cs.State.Waiting.Message
					}
				}
				break
			}
			if len(podImage) > 40 {
				podImage = "..." + podImage[len(podImage)-37:]
			}
			fmt.Printf("    %-45s %-12s %-7s %-10d %s\n",
				truncate(pod.Name, 45), string(pod.Status.Phase), ready, restarts, podImage)
			if msg != "" {
				fmt.Printf("      └─ %s\n", msg)
			}
		}
	}

	return nil
}

// ============================================================================
// health — deployment health check
// ============================================================================

func newHealthCmd() *cobra.Command {
	var (
		deployment string
		watch      bool
		interval   time.Duration
	)

	cmd := &cobra.Command{
		Use:   "health",
		Short: "Check or watch the health of a deployment",
		Long: `Query or continuously monitor the health of a Kubernetes deployment
by checking pod readiness, restart counts, and replica status.

Examples:
  kdctl health -n default -d goserver
  kdctl health -n default -d goserver --watch
  kdctl health -n default -d goserver --watch --interval 5s`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deployment == "" {
				return fmt.Errorf("--deployment is required")
			}

			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			logger := zap.NewNop()
			client, err := buildK8sClient(cfg, logger)
			if err != nil {
				return fmt.Errorf("creating k8s client: %w", err)
			}

			mon := health.NewMonitor(client, logger)

			if !watch {
				return printHealthOnce(client, mon, globalNamespace, deployment)
			}

			// Watch mode: poll at interval.
			printHeader("Watching Health: %s/%s (interval: %v)", globalNamespace, deployment, interval)
			fmt.Println()

			if err := printHealthOnce(client, mon, globalNamespace, deployment); err != nil {
				return err
			}

			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

			for {
				select {
				case <-sigCh:
					fmt.Println("\n  Stopped watching.")
					return nil
				case <-ticker.C:
					fmt.Println()
					if err := printHealthOnce(client, mon, globalNamespace, deployment); err != nil {
						fmt.Fprintf(os.Stderr, "  ✗ Error: %v\n", err)
					}
				}
			}
		},
	}

	cmd.Flags().StringVarP(&deployment, "deployment", "d", "", "Deployment name (required)")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Continuously watch health status")
	cmd.Flags().DurationVarP(&interval, "interval", "i", 5*time.Second, "Health check interval for watch mode")

	_ = cmd.MarkFlagRequired("deployment")

	return cmd
}

func printHealthOnce(client *k8s.Client, _ *health.Monitor, namespace, deployment string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	deploy, err := client.GetDeployment(ctx, namespace, deployment)
	if err != nil {
		return fmt.Errorf("fetching deployment: %w", err)
	}

	desired := int32(1)
	if deploy.Spec.Replicas != nil {
		desired = *deploy.Spec.Replicas
	}
	ready := deploy.Status.ReadyReplicas

	overall := "HEALTHY"
	if ready == 0 && desired > 0 {
		overall = "UNHEALTHY"
	} else if ready < desired {
		overall = "DEGRADED"
	}

	printHeader("Health: %s/%s", namespace, deployment)
	fmt.Printf("  Overall: %s\n", formatHealth(overall))
	fmt.Printf("  Ready:   %d/%d\n", ready, desired)

	// Progress bar.
	if desired > 0 {
		fmt.Printf("  Progress: %s\n", renderProgressBar(int(ready), int(desired), 30))
	}

	pods, err := client.GetDeploymentPods(ctx, namespace, deployment)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ⚠ Could not fetch pods: %v\n", err)
		return nil
	}

	if len(pods) > 0 {
		fmt.Printf("\n  Pods:\n")
		for _, pod := range pods {
			icon := "✓"
			podReady := false
			restarts := int32(0)
			podImage := ""
			msg := ""
			for _, cs := range pod.Status.ContainerStatuses {
				podReady = cs.Ready
				restarts = cs.RestartCount
				podImage = cs.Image
				if cs.State.Waiting != nil {
					msg = cs.State.Waiting.Reason
				}
				break
			}
			if !podReady {
				icon = "✗"
			}
			name := pod.Name
			if len(name) > 45 {
				name = name[:42] + "..."
			}
			fmt.Printf("    %s %-45s restarts=%-4d %s\n", icon, name, restarts, podImage)
			if msg != "" {
				fmt.Printf("      └─ %s\n", msg)
			}
		}
	}

	fmt.Printf("\n  Checked at: %s\n", time.Now().Local().Format("15:04:05"))
	return nil
}

func renderProgressBar(current, total, width int) string {
	if total <= 0 {
		return ""
	}
	filled := (current * width) / total
	if filled > width {
		filled = width
	}
	empty := width - filled
	pct := (current * 100) / total
	return fmt.Sprintf("%s%s %d%%",
		strings.Repeat("█", filled),
		strings.Repeat("░", empty),
		pct)
}

// ============================================================================
// rollback — deployment rollback
// ============================================================================

func newRollbackCmd() *cobra.Command {
	var (
		deployment string
		revision   int64
		reason     string
	)

	cmd := &cobra.Command{
		Use:   "rollback",
		Short: "Rollback a deployment to a previous revision",
		Long: `Trigger a manual rollback of a deployment. If no revision is specified,
rolls back to the previous revision. Connects directly to the Kubernetes API.

Examples:
  kdctl rollback -n default -d goserver
  kdctl rollback -n default -d goserver --revision 3
  kdctl rollback -n default -d goserver --reason "broken healthcheck"`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deployment == "" {
				return fmt.Errorf("--deployment is required")
			}

			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			logger := zap.NewNop()
			client, err := buildK8sClient(cfg, logger)
			if err != nil {
				return fmt.Errorf("creating k8s client: %w", err)
			}

			mon := health.NewMonitor(client, logger)
			rc := rollback.NewController(client, mon, logger)

			if reason == "" {
				reason = "manual rollback via kdctl"
			}

			revTarget := "previous"
			if revision > 0 {
				revTarget = fmt.Sprintf("%d", revision)
			}

			printHeader("Rolling back %s/%s to revision %s", globalNamespace, deployment, revTarget)
			fmt.Printf("  Reason: %s\n\n", reason)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			// Get current image before rollback.
			deploy, err := client.GetDeployment(ctx, globalNamespace, deployment)
			if err != nil {
				return fmt.Errorf("fetching deployment: %w", err)
			}
			oldImage := ""
			for _, c := range deploy.Spec.Template.Spec.Containers {
				oldImage = c.Image
				break
			}

			req := models.RollbackRequest{
				Namespace:      globalNamespace,
				DeploymentName: deployment,
				TargetRevision: revision,
				Reason:         reason,
			}

			result, err := rc.Rollback(ctx, req)
			if err != nil {
				return fmt.Errorf("rollback failed: %w", err)
			}

			if result.Success {
				printSuccess("Rollback successful!")
			} else {
				printWarning("Rollback completed with issues")
			}

			fmt.Printf("  Message:           %s\n", result.Message)
			fmt.Printf("  Rolled back to:    revision %d\n", result.RolledBackToRevision)
			fmt.Printf("  Previous image:    %s\n", oldImage)
			fmt.Printf("  Restored image:    %s\n", result.RestoredImage)
			fmt.Printf("  Timestamp:         %s\n", result.Timestamp.Local().Format("2006-01-02 15:04:05"))

			return nil
		},
	}

	cmd.Flags().StringVarP(&deployment, "deployment", "d", "", "Deployment name (required)")
	cmd.Flags().Int64VarP(&revision, "revision", "r", 0, "Target revision (0 = previous)")
	cmd.Flags().StringVar(&reason, "reason", "", "Reason for the rollback")

	_ = cmd.MarkFlagRequired("deployment")

	return cmd
}

// ============================================================================
// history — revision history
// ============================================================================

func newHistoryCmd() *cobra.Command {
	var (
		deployment string
		limit      int
	)

	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show deployment revision history",
		Long: `Display the revision history for a deployment, including images,
replica counts, and rollback events. Connects directly to the Kubernetes API.

Examples:
  kdctl history -n default -d goserver
  kdctl history -n default -d goserver --limit 20`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deployment == "" {
				return fmt.Errorf("--deployment is required")
			}

			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			client, err := buildK8sClient(cfg, zap.NewNop())
			if err != nil {
				return fmt.Errorf("creating k8s client: %w", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			history, err := client.GetDeploymentRevisionHistory(ctx, globalNamespace, deployment, 0)
			if err != nil {
				return fmt.Errorf("fetching history: %w", err)
			}

			// Sort by revision descending.
			sort.Slice(history, func(i, j int) bool {
				return history[i].Revision > history[j].Revision
			})

			// Apply limit.
			if limit > 0 && len(history) > limit {
				history = history[:limit]
			}

			printHeader("Deployment History: %s/%s", globalNamespace, deployment)

			if len(history) == 0 {
				fmt.Println("  No revision history found.")
				return nil
			}

			fmt.Printf("\n  %-10s %-45s %-10s %-20s %s\n",
				"REVISION", "IMAGE", "REPLICAS", "DEPLOYED AT", "NOTES")
			fmt.Printf("  %-10s %-45s %-10s %-20s %s\n",
				"--------", "-----", "--------", "-----------", "-----")

			for _, rev := range history {
				deployedAt := ""
				if !rev.DeployedAt.IsZero() {
					deployedAt = rev.DeployedAt.Local().Format("2006-01-02 15:04:05")
				}

				notes := ""
				if rev.RollbackReason != "" {
					notes = fmt.Sprintf("ROLLBACK: %s", rev.RollbackReason)
				}

				img := rev.Image
				if len(img) > 45 {
					img = "..." + img[len(img)-42:]
				}

				fmt.Printf("  %-10d %-45s %-10d %-20s %s\n",
					rev.Revision, img, rev.Replicas, deployedAt, notes)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&deployment, "deployment", "d", "", "Deployment name (required)")
	cmd.Flags().IntVarP(&limit, "limit", "l", 10, "Maximum number of revisions to show")

	_ = cmd.MarkFlagRequired("deployment")

	return cmd
}

// ============================================================================
// version
// ============================================================================

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print kdctl version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("kdctl %s\n", version)
			fmt.Printf("  commit:     %s\n", commit)
			fmt.Printf("  build date: %s\n", buildDate)
		},
	}
}

// ============================================================================
// Print helpers (for non-interactive CLI output)
// ============================================================================

func printDeployEvent(event models.DeployEvent, prevPhase models.DeploymentPhase) {
	ts := event.Timestamp.Local().Format("15:04:05")
	phaseStr := formatPhase(string(event.Phase))

	if event.Phase != prevPhase {
		fmt.Printf("\n  ── %s ──\n", phaseStr)
	}

	replicaInfo := ""
	if event.DesiredReplicas > 0 {
		replicaInfo = fmt.Sprintf(" [%d/%d ready]", event.ReadyReplicas, event.DesiredReplicas)
	}

	fmt.Printf("  [%s] %s%s\n", ts, event.Message, replicaInfo)
}

func printHeader(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("\n╭─ %s\n", msg)
	fmt.Printf("╰─%s\n", strings.Repeat("─", len(msg)+1))
}

func printSuccess(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("  ✓ %s\n", msg)
}

func printError(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("  ✗ %s\n", msg)
}

func printWarning(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("  ⚠ %s\n", msg)
}

func formatPhase(phase string) string {
	switch phase {
	case "PENDING", string(models.PhasePending):
		return "⏳ PENDING"
	case "IN_PROGRESS", string(models.PhaseInProgress):
		return "🔄 IN PROGRESS"
	case "HEALTH_CHECK", string(models.PhaseHealthCheck):
		return "🏥 HEALTH CHECK"
	case "PROMOTING", string(models.PhasePromoting):
		return "⬆️  PROMOTING"
	case "ROLLING_BACK", string(models.PhaseRollingBack):
		return "⏪ ROLLING BACK"
	case "COMPLETED", string(models.PhaseCompleted):
		return "✅ COMPLETED"
	case "FAILED", string(models.PhaseFailed):
		return "❌ FAILED"
	case "ROLLED_BACK", string(models.PhaseRolledBack):
		return "🔙 ROLLED BACK"
	default:
		return "❓ " + phase
	}
}

func formatHealth(status string) string {
	switch status {
	case "HEALTHY":
		return "✅ HEALTHY"
	case "DEGRADED":
		return "⚠️  DEGRADED"
	case "UNHEALTHY":
		return "❌ UNHEALTHY"
	default:
		return "❓ UNKNOWN"
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-1] + "…"
}
