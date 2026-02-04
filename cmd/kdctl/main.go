package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/durationpb"

	v1 "github.com/sonu/kube-deploy/api/v1"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

// Global flags
var (
	serverAddr string
	timeout    time.Duration
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "kdctl",
		Short: "kube-deploy CLI — zero-downtime Kubernetes deployment pipeline",
		Long: `kdctl is the command-line client for the kube-deploy server.

It communicates with the kube-deploy-server over gRPC to manage
zero-downtime deployments, health monitoring, and automated rollback
for Kubernetes workloads.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Global flags available to all subcommands.
	rootCmd.PersistentFlags().StringVarP(&serverAddr, "server", "s", envOrDefault("KDCTL_SERVER", "localhost:9090"),
		"kube-deploy-server gRPC address (env: KDCTL_SERVER)")
	rootCmd.PersistentFlags().DurationVar(&timeout, "timeout", 5*time.Minute,
		"Timeout for the operation")

	// Register subcommands.
	rootCmd.AddCommand(
		newDeployCmd(),
		newStatusCmd(),
		newHealthCmd(),
		newRollbackCmd(),
		newHistoryCmd(),
		newVersionCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// deploy command
// ============================================================================

func newDeployCmd() *cobra.Command {
	var (
		namespace     string
		deployment    string
		container     string
		image         string
		strategy      string
		maxUnavail    int32
		maxSurge      int32
		canaryReplica int32
		canaryPercent int32
		analysisDur   time.Duration
		successThresh int32
		rollbackOn    bool
		maxRetries    int32
		failThresh    int32
		dryRun        bool
		deployID      string
	)

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy a new version of a Kubernetes workload",
		Long: `Initiate a zero-downtime deployment and stream progress events in real time.

Examples:
  # Rolling update
  kdctl deploy --namespace default --deployment goserver --image goserver:v2

  # Canary deployment with analysis
  kdctl deploy -n default -d goserver --image goserver:v2 --strategy canary \
    --canary-replicas 1 --analysis-duration 60s

  # Dry run to preview changes
  kdctl deploy -n default -d goserver --image goserver:v2 --dry-run`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if namespace == "" || deployment == "" || image == "" {
				return fmt.Errorf("--namespace, --deployment, and --image are required")
			}

			if deployID == "" {
				deployID = fmt.Sprintf("deploy-%s-%d", deployment, time.Now().Unix())
			}

			conn, err := dialServer()
			if err != nil {
				return err
			}
			defer conn.Close()

			client := v1.NewKubeDeployServiceClient(conn)

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			// Build the deploy request.
			req := &v1.DeployRequest{
				DeployId: deployID,
				Target: &v1.DeploymentTarget{
					Namespace:      namespace,
					DeploymentName: deployment,
					ContainerName:  container,
					Image:          image,
				},
				DryRun: dryRun,
			}

			// Set strategy.
			switch strings.ToLower(strategy) {
			case "rolling", "":
				req.Strategy = v1.DeployStrategy_DEPLOY_STRATEGY_ROLLING
				req.RollingConfig = &v1.RollingUpdateConfig{
					MaxUnavailable: maxUnavail,
					MaxSurge:       maxSurge,
				}
			case "canary":
				req.Strategy = v1.DeployStrategy_DEPLOY_STRATEGY_CANARY
				req.CanaryConfig = &v1.CanaryConfig{
					CanaryReplicas:   canaryReplica,
					CanaryPercent:    canaryPercent,
					AnalysisDuration: durationpb.New(analysisDur),
					SuccessThreshold: successThresh,
				}
			case "blue-green", "bluegreen":
				req.Strategy = v1.DeployStrategy_DEPLOY_STRATEGY_BLUE_GREEN
			default:
				return fmt.Errorf("unsupported strategy: %q (use rolling, canary, or blue-green)", strategy)
			}

			// Set rollback policy.
			req.RollbackPolicy = &v1.RollbackPolicy{
				Enabled:             rollbackOn,
				MaxRetries:          maxRetries,
				FailureThreshold:    failThresh,
				SuccessThreshold:    successThresh,
				HealthCheckInterval: durationpb.New(10 * time.Second),
				HealthCheckTimeout:  durationpb.New(120 * time.Second),
			}

			if dryRun {
				printHeader("DRY RUN MODE")
			}

			printHeader("Deploying %s/%s → %s (strategy: %s)", namespace, deployment, image, strategy)
			fmt.Printf("  Deploy ID: %s\n\n", deployID)

			// Call Deploy RPC (server-streaming).
			stream, err := client.Deploy(ctx, req)
			if err != nil {
				return fmt.Errorf("deploy RPC failed: %w", err)
			}

			// Stream and display events.
			var lastPhase v1.DeploymentPhase
			for {
				event, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("stream error: %w", err)
				}

				printDeployEvent(event, lastPhase)
				lastPhase = event.Phase

				if event.IsTerminal {
					fmt.Println()
					if event.Phase == v1.DeploymentPhase_DEPLOYMENT_PHASE_COMPLETED {
						printSuccess("Deployment completed successfully!")
					} else if event.Phase == v1.DeploymentPhase_DEPLOYMENT_PHASE_FAILED {
						printError("Deployment failed: %s", event.ErrorDetail)
						return fmt.Errorf("deployment failed")
					} else if event.Phase == v1.DeploymentPhase_DEPLOYMENT_PHASE_ROLLED_BACK {
						printWarning("Deployment was rolled back: %s", event.Message)
					}
					break
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "Kubernetes namespace")
	cmd.Flags().StringVarP(&deployment, "deployment", "d", "", "Deployment name (required)")
	cmd.Flags().StringVarP(&container, "container", "c", "", "Container name (if multi-container pod)")
	cmd.Flags().StringVar(&image, "image", "", "Target container image (required)")
	cmd.Flags().StringVar(&strategy, "strategy", "rolling", "Deployment strategy: rolling, canary, blue-green")
	cmd.Flags().Int32Var(&maxUnavail, "max-unavailable", 0, "Max unavailable pods during rolling update (0 for zero-downtime)")
	cmd.Flags().Int32Var(&maxSurge, "max-surge", 1, "Max surge pods during rolling update")
	cmd.Flags().Int32Var(&canaryReplica, "canary-replicas", 1, "Number of canary replicas")
	cmd.Flags().Int32Var(&canaryPercent, "canary-percent", 10, "Percentage of traffic to route to canary")
	cmd.Flags().DurationVar(&analysisDur, "analysis-duration", 60*time.Second, "Duration of canary health analysis")
	cmd.Flags().Int32Var(&successThresh, "success-threshold", 3, "Consecutive successes required to pass health analysis")
	cmd.Flags().BoolVar(&rollbackOn, "auto-rollback", true, "Enable automatic rollback on failure")
	cmd.Flags().Int32Var(&maxRetries, "rollback-retries", 2, "Max automatic rollback retries")
	cmd.Flags().Int32Var(&failThresh, "failure-threshold", 3, "Consecutive health check failures before rollback")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview changes without applying")
	cmd.Flags().StringVar(&deployID, "deploy-id", "", "Custom deploy ID (auto-generated if empty)")

	_ = cmd.MarkFlagRequired("deployment")
	_ = cmd.MarkFlagRequired("image")

	return cmd
}

// ============================================================================
// status command
// ============================================================================

func newStatusCmd() *cobra.Command {
	var (
		namespace  string
		deployment string
		watch      bool
		interval   time.Duration
	)

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Get the current status of a deployment",
		Long: `Query the current state of a Kubernetes deployment, including pod-level details.

Examples:
  kdctl status -n default -d goserver
  kdctl status -n default -d goserver --watch
  kdctl status -n default -d goserver --watch --interval 3s`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if namespace == "" || deployment == "" {
				return fmt.Errorf("--namespace and --deployment are required")
			}

			conn, err := dialServer()
			if err != nil {
				return err
			}
			defer conn.Close()

			client := v1.NewKubeDeployServiceClient(conn)

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			if !watch {
				return printStatus(ctx, client, namespace, deployment)
			}

			// Watch mode: poll at interval.
			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			// Print immediately, then on each tick.
			if err := printStatus(ctx, client, namespace, deployment); err != nil {
				return err
			}

			for {
				select {
				case <-ctx.Done():
					return nil
				case <-ticker.C:
					fmt.Println()
					if err := printStatus(ctx, client, namespace, deployment); err != nil {
						printError("Error: %v", err)
					}
				}
			}
		},
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "Kubernetes namespace")
	cmd.Flags().StringVarP(&deployment, "deployment", "d", "", "Deployment name (required)")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Continuously watch deployment status")
	cmd.Flags().DurationVarP(&interval, "interval", "i", 2*time.Second, "Poll interval for watch mode")

	_ = cmd.MarkFlagRequired("deployment")

	return cmd
}

func printStatus(ctx context.Context, client v1.KubeDeployServiceClient, namespace, deployment string) error {
	resp, err := client.GetDeploymentStatus(ctx, &v1.GetDeploymentStatusRequest{
		Namespace:      namespace,
		DeploymentName: deployment,
	})
	if err != nil {
		return fmt.Errorf("GetDeploymentStatus RPC failed: %w", err)
	}

	printHeader("Deployment Status: %s/%s", namespace, deployment)
	fmt.Printf("  Phase:           %s\n", formatPhase(resp.Phase))
	fmt.Printf("  Image:           %s\n", resp.CurrentImage)
	fmt.Printf("  Replicas:        %d/%d ready, %d updated, %d available\n",
		resp.ReadyReplicas, resp.DesiredReplicas, resp.UpdatedReplicas, resp.AvailableReplicas)
	fmt.Printf("  Revision:        %d\n", resp.CurrentRevision)
	fmt.Printf("  Health:          %s\n", formatHealth(resp.HealthStatus))
	if resp.LastUpdated != nil {
		fmt.Printf("  Last Updated:    %s\n", resp.LastUpdated.AsTime().Local().Format("2006-01-02 15:04:05"))
	}

	if len(resp.Conditions) > 0 {
		fmt.Printf("\n  Conditions:\n")
		for _, cond := range resp.Conditions {
			fmt.Printf("    • %s\n", cond)
		}
	}

	if len(resp.Pods) > 0 {
		fmt.Printf("\n  Pods:\n")
		fmt.Printf("    %-45s %-12s %-7s %-10s %s\n", "NAME", "STATUS", "READY", "RESTARTS", "IMAGE")
		for _, pod := range resp.Pods {
			ready := "✗"
			if pod.Ready {
				ready = "✓"
			}
			img := pod.Image
			if len(img) > 40 {
				img = "..." + img[len(img)-37:]
			}
			fmt.Printf("    %-45s %-12s %-7s %-10d %s\n",
				truncate(pod.Name, 45), pod.Phase, ready, pod.RestartCount, img)
			if pod.Message != "" {
				fmt.Printf("      └─ %s\n", pod.Message)
			}
		}
	}

	return nil
}

// ============================================================================
// health command
// ============================================================================

func newHealthCmd() *cobra.Command {
	var (
		namespace  string
		deployment string
		watch      bool
		interval   time.Duration
	)

	cmd := &cobra.Command{
		Use:   "health",
		Short: "Check or watch the health of a deployment",
		Long: `Query or continuously monitor health check results for a deployment.

Examples:
  kdctl health -n default -d goserver
  kdctl health -n default -d goserver --watch
  kdctl health -n default -d goserver --watch --interval 5s`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if namespace == "" || deployment == "" {
				return fmt.Errorf("--namespace and --deployment are required")
			}

			conn, err := dialServer()
			if err != nil {
				return err
			}
			defer conn.Close()

			client := v1.NewKubeDeployServiceClient(conn)

			if !watch {
				// One-shot: get the current status which includes health info.
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()

				resp, err := client.GetDeploymentStatus(ctx, &v1.GetDeploymentStatusRequest{
					Namespace:      namespace,
					DeploymentName: deployment,
				})
				if err != nil {
					return fmt.Errorf("GetDeploymentStatus RPC failed: %w", err)
				}

				printHeader("Health: %s/%s", namespace, deployment)
				fmt.Printf("  Overall: %s\n", formatHealth(resp.HealthStatus))
				fmt.Printf("  Ready:   %d/%d\n", resp.ReadyReplicas, resp.DesiredReplicas)
				if len(resp.Pods) > 0 {
					fmt.Printf("\n  Pods:\n")
					for _, pod := range resp.Pods {
						icon := "✓"
						if !pod.Ready {
							icon = "✗"
						}
						fmt.Printf("    %s %-40s restarts=%-4d %s\n",
							icon, truncate(pod.Name, 40), pod.RestartCount, pod.Image)
						if pod.Message != "" {
							fmt.Printf("      └─ %s\n", pod.Message)
						}
					}
				}
				return nil
			}

			// Watch mode: stream health events.
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			stream, err := client.WatchHealth(ctx, &v1.WatchHealthRequest{
				Namespace:      namespace,
				DeploymentName: deployment,
				Interval:       durationpb.New(interval),
			})
			if err != nil {
				return fmt.Errorf("WatchHealth RPC failed: %w", err)
			}

			printHeader("Watching Health: %s/%s (interval: %v)", namespace, deployment, interval)
			fmt.Println()

			for {
				event, err := stream.Recv()
				if err == io.EOF {
					fmt.Println("\n  Health watch stream ended.")
					return nil
				}
				if err != nil {
					return fmt.Errorf("stream error: %w", err)
				}

				ts := ""
				if event.Timestamp != nil {
					ts = event.Timestamp.AsTime().Local().Format("15:04:05")
				}

				fmt.Printf("  [%s] %s  %s\n", ts, formatHealth(event.OverallStatus), event.Summary)

				for _, result := range event.Results {
					icon := "✓"
					if result.Status == v1.HealthStatus_HEALTH_STATUS_UNHEALTHY {
						icon = "✗"
					} else if result.Status == v1.HealthStatus_HEALTH_STATUS_DEGRADED {
						icon = "⚠"
					} else if result.Status == v1.HealthStatus_HEALTH_STATUS_UNKNOWN {
						icon = "?"
					}
					latency := ""
					if result.Latency != nil {
						latency = fmt.Sprintf(" (%v)", result.Latency.AsDuration().Round(time.Millisecond))
					}
					fmt.Printf("           %s %-20s %s%s\n", icon, result.Type, result.Message, latency)
				}
			}
		},
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "Kubernetes namespace")
	cmd.Flags().StringVarP(&deployment, "deployment", "d", "", "Deployment name (required)")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Continuously stream health events")
	cmd.Flags().DurationVarP(&interval, "interval", "i", 5*time.Second, "Health check interval for watch mode")

	_ = cmd.MarkFlagRequired("deployment")

	return cmd
}

// ============================================================================
// rollback command
// ============================================================================

func newRollbackCmd() *cobra.Command {
	var (
		namespace  string
		deployment string
		revision   int64
		reason     string
	)

	cmd := &cobra.Command{
		Use:   "rollback",
		Short: "Rollback a deployment to a previous revision",
		Long: `Trigger a manual rollback of a deployment to a specific revision.
If no revision is specified, rolls back to the previous revision.

Examples:
  kdctl rollback -n default -d goserver
  kdctl rollback -n default -d goserver --revision 3
  kdctl rollback -n default -d goserver --reason "broken healthcheck"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if namespace == "" || deployment == "" {
				return fmt.Errorf("--namespace and --deployment are required")
			}

			conn, err := dialServer()
			if err != nil {
				return err
			}
			defer conn.Close()

			client := v1.NewKubeDeployServiceClient(conn)

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			if reason == "" {
				reason = "manual rollback via kdctl"
			}

			revTarget := "previous"
			if revision > 0 {
				revTarget = fmt.Sprintf("%d", revision)
			}

			printHeader("Rolling back %s/%s to revision %s", namespace, deployment, revTarget)
			fmt.Printf("  Reason: %s\n\n", reason)

			resp, err := client.Rollback(ctx, &v1.RollbackRequest{
				Namespace:      namespace,
				DeploymentName: deployment,
				TargetRevision: revision,
				Reason:         reason,
			})
			if err != nil {
				return fmt.Errorf("Rollback RPC failed: %w", err)
			}

			if resp.Success {
				printSuccess("Rollback successful!")
			} else {
				printWarning("Rollback completed with issues")
			}

			fmt.Printf("  Message:           %s\n", resp.Message)
			fmt.Printf("  Rolled back to:    revision %d\n", resp.RolledBackToRevision)
			fmt.Printf("  Previous image:    %s\n", resp.PreviousImage)
			fmt.Printf("  Restored image:    %s\n", resp.RestoredImage)
			if resp.Timestamp != nil {
				fmt.Printf("  Timestamp:         %s\n", resp.Timestamp.AsTime().Local().Format("2006-01-02 15:04:05"))
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "Kubernetes namespace")
	cmd.Flags().StringVarP(&deployment, "deployment", "d", "", "Deployment name (required)")
	cmd.Flags().Int64VarP(&revision, "revision", "r", 0, "Target revision (0 = previous)")
	cmd.Flags().StringVar(&reason, "reason", "", "Reason for the rollback")

	_ = cmd.MarkFlagRequired("deployment")

	return cmd
}

// ============================================================================
// history command
// ============================================================================

func newHistoryCmd() *cobra.Command {
	var (
		namespace  string
		deployment string
		limit      int32
	)

	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show deployment revision history",
		Long: `Display the revision history for a deployment, including images,
strategies used, and rollback events.

Examples:
  kdctl history -n default -d goserver
  kdctl history -n default -d goserver --limit 20`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if namespace == "" || deployment == "" {
				return fmt.Errorf("--namespace and --deployment are required")
			}

			conn, err := dialServer()
			if err != nil {
				return err
			}
			defer conn.Close()

			client := v1.NewKubeDeployServiceClient(conn)

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			resp, err := client.GetHistory(ctx, &v1.GetHistoryRequest{
				Namespace:      namespace,
				DeploymentName: deployment,
				Limit:          limit,
			})
			if err != nil {
				return fmt.Errorf("GetHistory RPC failed: %w", err)
			}

			printHeader("Deployment History: %s/%s", namespace, deployment)

			if len(resp.Revisions) == 0 {
				fmt.Println("  No revision history found.")
				return nil
			}

			fmt.Printf("\n  %-10s %-45s %-10s %-20s %s\n",
				"REVISION", "IMAGE", "REPLICAS", "DEPLOYED AT", "NOTES")
			fmt.Printf("  %-10s %-45s %-10s %-20s %s\n",
				"--------", "-----", "--------", "-----------", "-----")

			for _, rev := range resp.Revisions {
				deployedAt := ""
				if rev.DeployedAt != nil {
					deployedAt = rev.DeployedAt.AsTime().Local().Format("2006-01-02 15:04:05")
				}

				notes := ""
				if rev.RollbackReason != "" {
					notes = fmt.Sprintf("ROLLBACK: %s", rev.RollbackReason)
				} else if rev.Result != v1.DeploymentPhase_DEPLOYMENT_PHASE_UNSPECIFIED {
					notes = formatPhase(rev.Result)
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

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "Kubernetes namespace")
	cmd.Flags().StringVarP(&deployment, "deployment", "d", "", "Deployment name (required)")
	cmd.Flags().Int32VarP(&limit, "limit", "l", 10, "Maximum number of revisions to show")

	_ = cmd.MarkFlagRequired("deployment")

	return cmd
}

// ============================================================================
// version command
// ============================================================================

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print kdctl version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("kdctl\n")
			fmt.Printf("  version:    %s\n", version)
			fmt.Printf("  commit:     %s\n", commit)
			fmt.Printf("  build date: %s\n", buildDate)
		},
	}
}

// ============================================================================
// Helpers
// ============================================================================

// dialServer establishes a gRPC connection to the kube-deploy server.
func dialServer() (*grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, serverAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to kube-deploy-server at %s: %w\n\nMake sure the server is running:\n  kube-deploy-server --port 9090", serverAddr, err)
	}
	return conn, nil
}

// envOrDefault returns the value of an environment variable or a default value.
func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// printDeployEvent formats and prints a deployment progress event.
func printDeployEvent(event *v1.DeployEvent, prevPhase v1.DeploymentPhase) {
	ts := ""
	if event.Timestamp != nil {
		ts = event.Timestamp.AsTime().Local().Format("15:04:05")
	}

	phaseStr := formatPhase(event.Phase)

	// Print a phase transition header when the phase changes.
	if event.Phase != prevPhase {
		fmt.Printf("\n  ── %s ──\n", phaseStr)
	}

	// Print the event message.
	replicaInfo := ""
	if event.DesiredReplicas > 0 {
		replicaInfo = fmt.Sprintf(" [%d/%d ready]", event.ReadyReplicas, event.DesiredReplicas)
	}

	fmt.Printf("  [%s] %s%s\n", ts, event.Message, replicaInfo)
}

// printHeader prints a formatted section header.
func printHeader(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("\n╭─ %s\n", msg)
	fmt.Printf("╰─%s\n", strings.Repeat("─", len(msg)+1))
}

// printSuccess prints a green success message.
func printSuccess(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("  ✓ %s\n", msg)
}

// printError prints a red error message.
func printError(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("  ✗ %s\n", msg)
}

// printWarning prints a yellow warning message.
func printWarning(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("  ⚠ %s\n", msg)
}

// formatPhase returns a human-readable representation of a deployment phase.
func formatPhase(phase v1.DeploymentPhase) string {
	switch phase {
	case v1.DeploymentPhase_DEPLOYMENT_PHASE_PENDING:
		return "⏳ PENDING"
	case v1.DeploymentPhase_DEPLOYMENT_PHASE_IN_PROGRESS:
		return "🔄 IN PROGRESS"
	case v1.DeploymentPhase_DEPLOYMENT_PHASE_HEALTH_CHECK:
		return "🏥 HEALTH CHECK"
	case v1.DeploymentPhase_DEPLOYMENT_PHASE_PROMOTING:
		return "⬆️  PROMOTING"
	case v1.DeploymentPhase_DEPLOYMENT_PHASE_ROLLING_BACK:
		return "⏪ ROLLING BACK"
	case v1.DeploymentPhase_DEPLOYMENT_PHASE_COMPLETED:
		return "✅ COMPLETED"
	case v1.DeploymentPhase_DEPLOYMENT_PHASE_FAILED:
		return "❌ FAILED"
	case v1.DeploymentPhase_DEPLOYMENT_PHASE_ROLLED_BACK:
		return "🔙 ROLLED BACK"
	default:
		return "❓ UNKNOWN"
	}
}

// formatHealth returns a human-readable representation of a health status.
func formatHealth(status v1.HealthStatus) string {
	switch status {
	case v1.HealthStatus_HEALTH_STATUS_HEALTHY:
		return "✅ HEALTHY"
	case v1.HealthStatus_HEALTH_STATUS_DEGRADED:
		return "⚠️  DEGRADED"
	case v1.HealthStatus_HEALTH_STATUS_UNHEALTHY:
		return "❌ UNHEALTHY"
	case v1.HealthStatus_HEALTH_STATUS_UNKNOWN:
		return "❓ UNKNOWN"
	default:
		return "❓ UNKNOWN"
	}
}

// truncate shortens a string to the given max length, adding "…" if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-1] + "…"
}
