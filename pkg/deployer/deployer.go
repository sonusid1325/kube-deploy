package deployer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sonu/kube-deploy/internal/config"
	"github.com/sonu/kube-deploy/pkg/k8s"
	"github.com/sonu/kube-deploy/pkg/models"
)

// Strategy defines the interface that all deployment strategies must implement.
// Each strategy handles the mechanics of updating a Kubernetes Deployment
// while maintaining zero-downtime guarantees.
type Strategy interface {
	// Name returns the human-readable name of the strategy.
	Name() string

	// Execute performs the deployment and sends progress events to the channel.
	// It blocks until the deployment reaches a terminal state (completed, failed,
	// or rolled back). The channel is closed by the caller after Execute returns.
	Execute(ctx context.Context, req models.DeploymentRequest, events chan<- models.DeployEvent) error

	// Validate checks whether the strategy can handle the given request
	// (e.g., canary requires certain config fields).
	Validate(req models.DeploymentRequest) error
}

// EventCallback is a function that receives deployment progress events.
// It is called synchronously on each event — implementations should not block.
type EventCallback func(event models.DeployEvent)

// Engine is the deployment orchestrator. It selects the appropriate strategy,
// manages deployment lifecycle state, and coordinates with health monitoring
// and rollback controllers.
type Engine struct {
	client     *k8s.Client
	config     *config.Config
	logger     *zap.Logger
	strategies map[models.DeployStrategy]Strategy

	mu       sync.RWMutex
	trackers map[string]*models.DeploymentTracker
}

// NewEngine creates a new deployment engine with the given Kubernetes client,
// configuration, and logger. It registers the built-in deployment strategies.
func NewEngine(client *k8s.Client, cfg *config.Config, logger *zap.Logger) *Engine {
	if logger == nil {
		logger = zap.NewNop()
	}

	e := &Engine{
		client:     client,
		config:     cfg,
		logger:     logger.Named("deployer"),
		strategies: make(map[models.DeployStrategy]Strategy),
		trackers:   make(map[string]*models.DeploymentTracker),
	}

	// Register built-in strategies.
	rolling := NewRollingStrategy(client, cfg, logger)
	e.RegisterStrategy(models.StrategyRolling, rolling)

	canary := NewCanaryStrategy(client, cfg, logger)
	e.RegisterStrategy(models.StrategyCanary, canary)

	return e
}

// RegisterStrategy registers a deployment strategy for the given strategy type.
// This can be used to add custom strategies or override built-in ones.
func (e *Engine) RegisterStrategy(strategyType models.DeployStrategy, strategy Strategy) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.strategies[strategyType] = strategy
	e.logger.Info("registered deployment strategy",
		zap.String("strategy", string(strategyType)),
		zap.String("name", strategy.Name()),
	)
}

// GetStrategy returns the strategy registered for the given type.
func (e *Engine) GetStrategy(strategyType models.DeployStrategy) (Strategy, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	s, ok := e.strategies[strategyType]
	if !ok {
		return nil, fmt.Errorf("unsupported deployment strategy: %q", strategyType)
	}
	return s, nil
}

// Deploy initiates a deployment using the specified strategy and returns a
// channel that emits progress events. The deployment runs asynchronously;
// the channel is closed when the deployment reaches a terminal state.
//
// The caller should consume events from the returned channel until it is
// closed. Each event contains the current phase, replica counts, and any
// error details.
func (e *Engine) Deploy(ctx context.Context, req models.DeploymentRequest) (<-chan models.DeployEvent, error) {
	// Apply defaults for any unset fields.
	req.ApplyDefaults()

	// Validate the request.
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("invalid deployment request: %w", err)
	}

	// Resolve the strategy.
	strategy, err := e.GetStrategy(req.Strategy)
	if err != nil {
		return nil, err
	}

	// Validate strategy-specific requirements.
	if err := strategy.Validate(req); err != nil {
		return nil, fmt.Errorf("strategy validation failed: %w", err)
	}

	// Check for duplicate in-progress deployments.
	trackerKey := deploymentKey(req.Target.Namespace, req.Target.DeploymentName)
	e.mu.RLock()
	existing, exists := e.trackers[trackerKey]
	e.mu.RUnlock()
	if exists && !existing.IsFinished() {
		return nil, fmt.Errorf(
			"deployment %s/%s already in progress (deploy_id=%s, phase=%s)",
			req.Target.Namespace, req.Target.DeploymentName,
			existing.Request.DeployID, existing.Status.Phase,
		)
	}

	// Create the deployment tracker.
	tracker := models.NewDeploymentTracker(req)
	e.mu.Lock()
	e.trackers[trackerKey] = tracker
	e.trackers[req.DeployID] = tracker
	e.mu.Unlock()

	e.logger.Info("initiating deployment",
		zap.String("deploy_id", req.DeployID),
		zap.String("namespace", req.Target.Namespace),
		zap.String("deployment", req.Target.DeploymentName),
		zap.String("image", req.Target.Image),
		zap.String("strategy", string(req.Strategy)),
		zap.Bool("dry_run", req.DryRun),
	)

	// Buffered channel so the strategy can write without blocking immediately.
	events := make(chan models.DeployEvent, 64)

	// Send the initial pending event.
	initialEvent := models.NewDeployEvent(req.DeployID, models.PhasePending,
		fmt.Sprintf("deployment %s/%s queued with strategy %s",
			req.Target.Namespace, req.Target.DeploymentName, req.Strategy))
	initialEvent.TargetImage = req.Target.Image
	tracker.AddEvent(initialEvent)
	events <- initialEvent

	// Run the strategy asynchronously.
	go func() {
		defer close(events)

		strategyEvents := make(chan models.DeployEvent, 64)
		done := make(chan error, 1)

		go func() {
			done <- strategy.Execute(ctx, req, strategyEvents)
		}()

		// Forward strategy events to the caller and record them in the tracker.
		for {
			select {
			case evt, ok := <-strategyEvents:
				if !ok {
					// Strategy channel closed; wait for Execute to return.
					strategyEvents = nil
					continue
				}
				e.mu.Lock()
				tracker.AddEvent(evt)
				e.mu.Unlock()
				events <- evt

				if evt.IsTerminal {
					e.logger.Info("deployment reached terminal state",
						zap.String("deploy_id", req.DeployID),
						zap.String("phase", string(evt.Phase)),
						zap.String("message", evt.Message),
					)
				}

			case err := <-done:
				if err != nil {
					e.logger.Error("deployment strategy failed",
						zap.String("deploy_id", req.DeployID),
						zap.Error(err),
					)
					// Send a failure event if the strategy returned an error
					// and hasn't already sent a terminal event.
					e.mu.RLock()
					isFinished := tracker.IsFinished()
					e.mu.RUnlock()
					if !isFinished {
						failEvent := models.NewDeployEvent(req.DeployID, models.PhaseFailed,
							fmt.Sprintf("deployment failed: %v", err))
						failEvent.ErrorDetail = err.Error()
						failEvent.TargetImage = req.Target.Image
						e.mu.Lock()
						tracker.AddEvent(failEvent)
						e.mu.Unlock()
						events <- failEvent
					}
				}
				// Drain any remaining events from the strategy channel.
				if strategyEvents != nil {
					for evt := range strategyEvents {
						e.mu.Lock()
						tracker.AddEvent(evt)
						e.mu.Unlock()
						events <- evt
					}
				}
				return
			}
		}
	}()

	return events, nil
}

// GetTracker returns the deployment tracker for a given deploy ID or
// namespace/deployment key.
func (e *Engine) GetTracker(key string) (*models.DeploymentTracker, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	t, ok := e.trackers[key]
	return t, ok
}

// GetTrackerByDeployment returns the tracker for a namespace/deployment pair.
func (e *Engine) GetTrackerByDeployment(namespace, deploymentName string) (*models.DeploymentTracker, bool) {
	return e.GetTracker(deploymentKey(namespace, deploymentName))
}

// ListTrackers returns all deployment trackers, optionally filtered by namespace.
func (e *Engine) ListTrackers(namespace string) []*models.DeploymentTracker {
	e.mu.RLock()
	defer e.mu.RUnlock()

	seen := make(map[string]bool)
	result := make([]*models.DeploymentTracker, 0)
	for _, t := range e.trackers {
		if seen[t.Request.DeployID] {
			continue
		}
		if namespace != "" && t.Request.Target.Namespace != namespace {
			continue
		}
		seen[t.Request.DeployID] = true
		result = append(result, t)
	}
	return result
}

// CleanupFinished removes all finished deployment trackers older than the
// given max age. This prevents unbounded memory growth for long-running servers.
func (e *Engine) CleanupFinished(maxAge time.Duration) int {
	e.mu.Lock()
	defer e.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	removed := 0

	for key, tracker := range e.trackers {
		if tracker.IsFinished() && tracker.FinishedAt.Before(cutoff) {
			delete(e.trackers, key)
			removed++
		}
	}

	if removed > 0 {
		e.logger.Info("cleaned up finished deployment trackers",
			zap.Int("removed", removed),
			zap.Duration("max_age", maxAge),
		)
	}
	return removed
}

// deploymentKey returns the canonical key for a namespace/deployment pair.
func deploymentKey(namespace, deploymentName string) string {
	return namespace + "/" + deploymentName
}

// ============================================================================
// Rolling Update Strategy
// ============================================================================

// RollingStrategy implements the Strategy interface for rolling updates.
// It updates the Deployment image in-place and polls for rollout completion
// with maxUnavailable=0, maxSurge=1 to guarantee zero downtime.
type RollingStrategy struct {
	client *k8s.Client
	config *config.Config
	logger *zap.Logger
}

// NewRollingStrategy creates a new rolling update strategy.
func NewRollingStrategy(client *k8s.Client, cfg *config.Config, logger *zap.Logger) *RollingStrategy {
	return &RollingStrategy{
		client: client,
		config: cfg,
		logger: logger.Named("rolling"),
	}
}

// Name returns the strategy name.
func (s *RollingStrategy) Name() string {
	return "rolling-update"
}

// Validate checks that the request is valid for a rolling update.
func (s *RollingStrategy) Validate(req models.DeploymentRequest) error {
	if req.Target.Namespace == "" {
		return fmt.Errorf("namespace is required")
	}
	if req.Target.DeploymentName == "" {
		return fmt.Errorf("deployment name is required")
	}
	if req.Target.Image == "" {
		return fmt.Errorf("image is required")
	}
	return nil
}

// Execute performs the rolling update deployment.
//
// Steps:
//  1. Fetch the current Deployment and record the current image.
//  2. Patch the Deployment with the new image, maxUnavailable=0, maxSurge=1.
//  3. Poll the Deployment rollout status until all replicas are updated and ready.
//  4. Send progress events on each poll tick.
//  5. On success, send a COMPLETED event; on timeout/error, send FAILED.
func (s *RollingStrategy) Execute(ctx context.Context, req models.DeploymentRequest, events chan<- models.DeployEvent) error {
	defer close(events)

	namespace := req.Target.Namespace
	deployName := req.Target.DeploymentName
	newImage := req.Target.Image
	containerName := req.Target.ContainerName

	// Step 1: Get the current deployment and record current state.
	currentDeploy, err := s.client.GetDeployment(ctx, namespace, deployName)
	if err != nil {
		return fmt.Errorf("fetching current deployment: %w", err)
	}

	currentImage := ""
	if len(currentDeploy.Spec.Template.Spec.Containers) > 0 {
		currentImage = currentDeploy.Spec.Template.Spec.Containers[0].Image
	}

	// Check if the image is already the target.
	if currentImage == newImage {
		evt := models.NewDeployEvent(req.DeployID, models.PhaseCompleted,
			fmt.Sprintf("deployment %s/%s already running image %s", namespace, deployName, newImage))
		evt.CurrentImage = currentImage
		evt.TargetImage = newImage
		events <- evt
		return nil
	}

	s.logger.Info("starting rolling update",
		zap.String("deployment", deployName),
		zap.String("namespace", namespace),
		zap.String("from_image", currentImage),
		zap.String("to_image", newImage),
	)

	// Send in-progress event.
	startEvt := models.NewDeployEvent(req.DeployID, models.PhaseInProgress,
		fmt.Sprintf("updating deployment %s/%s from %s to %s", namespace, deployName, currentImage, newImage))
	startEvt.CurrentImage = currentImage
	startEvt.TargetImage = newImage

	desired := int32(1)
	if currentDeploy.Spec.Replicas != nil {
		desired = *currentDeploy.Spec.Replicas
	}
	startEvt.DesiredReplicas = desired
	startEvt.ReadyReplicas = currentDeploy.Status.ReadyReplicas
	events <- startEvt

	// Step 2: Patch the deployment with the new image.
	if req.DryRun {
		evt := models.NewDeployEvent(req.DeployID, models.PhaseCompleted,
			fmt.Sprintf("[DRY RUN] would update %s/%s to image %s", namespace, deployName, newImage))
		evt.CurrentImage = currentImage
		evt.TargetImage = newImage
		evt.DesiredReplicas = desired
		events <- evt
		return nil
	}

	maxUnavailable := req.RollingConfig.MaxUnavailable
	maxSurge := req.RollingConfig.MaxSurge
	if maxSurge == 0 && maxUnavailable == 0 {
		// Ensure at least one of these is non-zero; default to zero-downtime.
		maxSurge = 1
	}

	_, err = s.client.UpdateDeploymentImage(ctx, namespace, deployName, containerName, newImage, maxUnavailable, maxSurge)
	if err != nil {
		return fmt.Errorf("patching deployment image: %w", err)
	}

	s.logger.Info("deployment image patched, waiting for rollout",
		zap.String("deployment", deployName),
		zap.Int("maxUnavailable", maxUnavailable),
		zap.Int("maxSurge", maxSurge),
	)

	// Step 3: Poll for rollout completion.
	rolloutTimeout := s.config.Deploy.RolloutTimeout
	if rolloutTimeout <= 0 {
		rolloutTimeout = 300 * time.Second
	}
	pollInterval := s.config.Deploy.PollInterval
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}

	lastMessage := ""
	status, err := s.client.WaitForRollout(ctx, namespace, deployName, rolloutTimeout, pollInterval,
		func(rs *k8s.DeploymentRolloutStatus) {
			msg := rs.Message
			if msg == lastMessage {
				return // Skip duplicate messages.
			}
			lastMessage = msg

			phase := models.PhaseInProgress
			if rs.Ready {
				phase = models.PhaseHealthCheck
			}

			evt := models.NewDeployEvent(req.DeployID, phase, msg)
			evt.CurrentImage = currentImage
			evt.TargetImage = newImage
			evt.ReadyReplicas = rs.ReadyReplicas
			evt.DesiredReplicas = rs.DesiredReplicas
			evt.UpdatedReplicas = rs.UpdatedReplicas
			evt.AvailableReplicas = rs.AvailableReplicas
			evt.Revision = rs.Revision
			events <- evt
		},
	)

	if err != nil {
		failEvt := models.NewDeployEvent(req.DeployID, models.PhaseFailed,
			fmt.Sprintf("rolling update failed: %v", err))
		failEvt.CurrentImage = currentImage
		failEvt.TargetImage = newImage
		failEvt.ErrorDetail = err.Error()
		if status != nil {
			failEvt.ReadyReplicas = status.ReadyReplicas
			failEvt.DesiredReplicas = status.DesiredReplicas
			failEvt.UpdatedReplicas = status.UpdatedReplicas
			failEvt.AvailableReplicas = status.AvailableReplicas
			failEvt.Revision = status.Revision
		}
		events <- failEvt
		return err
	}

	// Step 4: Rollout completed successfully.
	completeEvt := models.NewDeployEvent(req.DeployID, models.PhaseCompleted,
		fmt.Sprintf("rolling update complete: %s/%s now running %s (%d/%d ready)",
			namespace, deployName, newImage, status.ReadyReplicas, status.DesiredReplicas))
	completeEvt.CurrentImage = newImage
	completeEvt.TargetImage = newImage
	completeEvt.ReadyReplicas = status.ReadyReplicas
	completeEvt.DesiredReplicas = status.DesiredReplicas
	completeEvt.UpdatedReplicas = status.UpdatedReplicas
	completeEvt.AvailableReplicas = status.AvailableReplicas
	completeEvt.Revision = status.Revision
	events <- completeEvt

	s.logger.Info("rolling update completed successfully",
		zap.String("deployment", deployName),
		zap.String("image", newImage),
		zap.Int64("revision", status.Revision),
	)

	return nil
}
