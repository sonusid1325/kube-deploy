package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/sonu/kube-deploy/internal/config"
	"github.com/sonu/kube-deploy/internal/tui"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	var (
		namespace   string
		deployment  string
		kubeconfig  string
		kubeContext string
		inCluster   bool
		configPath  string
		logLevel    string
	)

	rootCmd := &cobra.Command{
		Use:   "kube-deploy",
		Short: "⎈ kube-deploy — zero-downtime Kubernetes deployment pipeline",
		Long: `kube-deploy is an interactive TUI for managing zero-downtime
Kubernetes deployments with rolling updates, canary deployments,
health monitoring, and automated rollback.

It connects directly to your Kubernetes cluster (no server required)
and provides a rich terminal interface for deployment operations.

Examples:
  # Launch TUI targeting a deployment
  kube-deploy -n default -d goserver

  # Use a specific kubeconfig and context
  kube-deploy -d myapp --kubeconfig ~/.kube/prod.yaml --context prod-cluster

  # Use in-cluster config (when running inside a pod)
  kube-deploy -d myapp --in-cluster`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deployment == "" {
				return fmt.Errorf("--deployment (-d) is required\n\nUsage:\n  kube-deploy -n <namespace> -d <deployment>")
			}

			// Load configuration.
			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			// Apply CLI overrides.
			if kubeconfig != "" {
				cfg.Kubernetes.Kubeconfig = kubeconfig
			}
			if kubeContext != "" {
				cfg.Kubernetes.Context = kubeContext
			}
			if inCluster {
				cfg.Kubernetes.InCluster = true
			}
			if logLevel != "" {
				cfg.Logging.Level = logLevel
			}

			// We don't need a full logger for the TUI — use a nop logger
			// so the k8s client and deployer don't spam the terminal.
			// Activity is shown in the TUI's Logs tab instead.

			// Create the TUI model.
			model, err := tui.NewModel(namespace, deployment, cfg, nil)
			if err != nil {
				return fmt.Errorf("initializing TUI: %w", err)
			}

			// Launch Bubble Tea.
			p := tea.NewProgram(
				model,
				tea.WithAltScreen(),
				tea.WithMouseCellMotion(),
			)

			// Wire the program reference so background goroutines
			// (deploy events, rollback progress) can send messages
			// into the Bubble Tea event loop via program.Send().
			model.SetProgram(p)

			finalModel, err := p.Run()
			if err != nil {
				return fmt.Errorf("TUI error: %w", err)
			}

			// If the model had a fatal error, report it.
			_ = finalModel
			return nil
		},
	}

	// Flags.
	rootCmd.Flags().StringVarP(&namespace, "namespace", "n", "default",
		"Kubernetes namespace of the target deployment")
	rootCmd.Flags().StringVarP(&deployment, "deployment", "d", "",
		"Name of the Kubernetes Deployment to manage (required)")
	rootCmd.Flags().StringVar(&kubeconfig, "kubeconfig", "",
		"Path to kubeconfig file (defaults to ~/.kube/config or KUBECONFIG env)")
	rootCmd.Flags().StringVar(&kubeContext, "context", "",
		"Kubernetes context to use (defaults to current context)")
	rootCmd.Flags().BoolVar(&inCluster, "in-cluster", false,
		"Use in-cluster Kubernetes configuration (for running inside a pod)")
	rootCmd.Flags().StringVar(&configPath, "config", "",
		"Path to kube-deploy config file (YAML)")
	rootCmd.Flags().StringVar(&logLevel, "log-level", "",
		"Log level for k8s client: debug, info, warn, error")

	_ = rootCmd.MarkFlagRequired("deployment")

	// Version subcommand.
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("kube-deploy %s\n", version)
			fmt.Printf("  commit:     %s\n", commit)
			fmt.Printf("  build date: %s\n", buildDate)
		},
	})

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "\n  Error: %v\n\n", err)
		os.Exit(1)
	}
}
