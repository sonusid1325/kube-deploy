package rollback

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sonu/kube-deploy/pkg/health"
	"github.com/sonu/kube-deploy/pkg/k8s"
	"github.com/sonu/kube-deploy/pkg/models"
)

// Controller manages automated and manual rollback operations for Kubernetes
// deployments. It integrates with the health Monitor to detect unhealthy
// deployments and automatically revert them to the last known good revision.
//
// The controller maintains:
//   - Per-deployment rollback policies (thresholds, retries, cooldowns)
//   - Rollback history for audit and debugging
//   - Cooldown tracking to prevent rollback storms
//   - Integration hooks for notifications and event streaming
type Controller struct {
	client  *k8s.Client
	monitor *health.Monitor
	logger  *zap.Logger

	mu             sync.RWMutex
	policies       map[string]*PolicyState
	history        map[string][]models.RollbackResult
	cooldowns      map[string]time.Time
	defaultPolicy  models.RollbackPolicy
	onRollbackHook []RollbackCallback
}

// RollbackCallback is invoked when a rollback operation is triggered,
// whether automatic or manual. Implementations can use this for alerting,
// metrics, or audit logging.
type RollbackCallback func(namespace, deploymentName string, result models.RollbackResult)

// PolicyState tracks the runtime state of a rollback policy for a single
// deployment, including retry counters and the timestamp of the last rollback.
type PolicyState struct {
	Policy          models.RollbackPolicy
	Namespace       string
	DeploymentName  string
	RetryCount      int
	LastRollbackAt  time.Time
	IsRollingBack   bool
	LastHealthEvent models.HealthEvent
	Enabled         bool
}

// ControllerOption is a functional option for configuring the Controller.
type ControllerOption func(*Controller)

// WithDefaultPolicy sets the default rollback policy applied to deployments
// that don't have an explicit policy configured.
func WithDefaultPolicy(policy models.RollbackPolicy) ControllerOption {
	return func(c *Controller) {
		c.defaultPolicy = policy
	}
}

// WithRollbackCallback registers a callback that fires on every rollback event.
func WithRollbackCallback(cb RollbackCallback) ControllerOption {
	return func(c *Controller) {
		c.onRollbackHook = append(c.onRollbackHook, cb)
	}
}

// NewController creates a new rollback Controller.
//
// Parameters:
//   - client: Kubernetes client for performing rollback operations
//   - monitor: health Monitor for receiving unhealthy deployment notifications
//   - logger: structured logger
//   - opts: functional options for configuration
//
// The controller automatically registers itself as an unhealthy callback
// on the health monitor so that it is notified when deployments cross
// the failure threshold.
func NewController(client *k8s.Client, monitor *health.Monitor, logger *zap.Logger, opts ...ControllerOption) *Controller {
	if logger == nil {
		logger = zap.NewNop()
	}

	c := &Controller{
		client:         client,
		monitor:        monitor,
		logger:         logger.Named("rollback"),
		policies:       make(map[string]*PolicyState),
		history:        make(map[string][]models.RollbackResult),
		cooldowns:      make(map[string]time.Time),
		defaultPolicy:  models.DefaultRollbackPolicy(),
		onRollbackHook: make([]RollbackCallback, 0),
	}

	for _, opt := range opts {
		opt(c)
	}

	// Register the auto-rollback handler with the health monitor.
	if monitor != nil {
		monitor.OnUnhealthy(c.handleUnhealthy)
	}

	c.logger.Info("rollback controller initialized",
		zap.Bool("default_policy_enabled", c.defaultPolicy.Enabled),
		zap.Int("default_max_retries", c.defaultPolicy.MaxRetries),
		zap.Duration("default_health_check_timeout", c.defaultPolicy.HealthCheckTimeout),
	)

	return c
}

// SetPolicy configures the rollback policy for a specific deployment.
// This overrides the default policy for that deployment.
func (c *Controller) SetPolicy(namespace, deploymentName string, policy models.RollbackPolicy) {
	key := policyKey(namespace, deploymentName)

	c.mu.Lock()
	defer c.mu.Unlock()

	state, exists := c.policies[key]
	if !exists {
		state = &PolicyState{
			Namespace:      namespace,
			DeploymentName: deploymentName,
		}
		c.policies[key] = state
	}

	state.Policy = policy
	state.Enabled = policy.Enabled

	c.logger.Info("rollback policy set",
		zap.String("namespace", namespace),
		zap.String("deployment", deploymentName),
		zap.Bool("enabled", policy.Enabled),
		zap.Int("max_retries", policy.MaxRetries),
		zap.Int("failure_threshold", policy.FailureThreshold),
	)
}

// GetPolicy returns the effective rollback policy for a deployment.
// If no explicit policy has been set, the default policy is returned.
func (c *Controller) GetPolicy(namespace, deploymentName string) models.RollbackPolicy {
	key := policyKey(namespace, deploymentName)

	c.mu.RLock()
	defer c.mu.RUnlock()

	if state, ok := c.policies[key]; ok {
		return state.Policy
	}
	return c.defaultPolicy
}

// RemovePolicy removes the explicit rollback policy for a deployment,
// causing it to fall back to the default policy.
func (c *Controller) RemovePolicy(namespace, deploymentName string) {
	key := policyKey(namespace, deploymentName)

	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.policies, key)
	c.logger.Info("rollback policy removed, reverting to default",
		zap.String("namespace", namespace),
		zap.String("deployment", deploymentName),
	)
}

// EnableAutoRollback enables automatic rollback for a deployment using the
// configured (or default) policy. This starts health monitoring if not already
// active and sets the deployment as eligible for auto-rollback.
func (c *Controller) EnableAutoRollback(
	ctx context.Context,
	namespace, deploymentName string,
	policy models.RollbackPolicy,
) error {
	c.SetPolicy(namespace, deploymentName, policy)

	key := policyKey(namespace, deploymentName)
	c.mu.Lock()
	if state, ok := c.policies[key]; ok {
		state.Enabled = true
		state.RetryCount = 0
	}
	c.mu.Unlock()

	// Ensure the health monitor is watching this deployment.
	if !c.monitor.IsWatching(namespace, deploymentName) {
		_, err := c.monitor.Watch(
			ctx,
			namespace,
			deploymentName,
			policy.HealthCheckInterval,
			policy.FailureThreshold,
			policy.SuccessThreshold,
		)
		if err != nil {
			return fmt.Errorf("starting health watch for auto-rollback: %w", err)
		}
	}

	c.logger.Info("auto-rollback enabled",
		zap.String("namespace", namespace),
		zap.String("deployment", deploymentName),
		zap.Int("failure_threshold", policy.FailureThreshold),
		zap.Int("max_retries", policy.MaxRetries),
	)

	return nil
}

// DisableAutoRollback disables automatic rollback for a deployment.
// Health monitoring continues but will no longer trigger rollbacks.
func (c *Controller) DisableAutoRollback(namespace, deploymentName string) {
	key := policyKey(namespace, deploymentName)

	c.mu.Lock()
	defer c.mu.Unlock()

	if state, ok := c.policies[key]; ok {
		state.Enabled = false
	}

	c.logger.Info("auto-rollback disabled",
		zap.String("namespace", namespace),
		zap.String("deployment", deploymentName),
	)
}

// Rollback performs a manual rollback of a deployment to the specified target
// revision. If targetRevision is 0, it rolls back to the previous revision.
//
// This method:
//  1. Validates the rollback request
//  2. Records the current state for audit
//  3. Executes the rollback via the Kubernetes client
//  4. Waits for the rollback to complete (rollout finishes)
//  5. Runs a post-rollback health check
//  6. Records the result and fires callbacks
func (c *Controller) Rollback(ctx context.Context, req models.RollbackRequest) (*models.RollbackResult, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("invalid rollback request: %w", err)
	}

	key := policyKey(req.Namespace, req.DeploymentName)

	// Check cooldown.
	c.mu.RLock()
	if cooldownUntil, ok := c.cooldowns[key]; ok && time.Now().Before(cooldownUntil) {
		c.mu.RUnlock()
		remaining := time.Until(cooldownUntil)
		return nil, fmt.Errorf("rollback is in cooldown for %s/%s, %v remaining",
			req.Namespace, req.DeploymentName, remaining.Round(time.Second))
	}
	c.mu.RUnlock()

	// Mark as rolling back.
	c.mu.Lock()
	if state, ok := c.policies[key]; ok {
		if state.IsRollingBack {
			c.mu.Unlock()
			return nil, fmt.Errorf("rollback already in progress for %s/%s",
				req.Namespace, req.DeploymentName)
		}
		state.IsRollingBack = true
	}
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		if state, ok := c.policies[key]; ok {
			state.IsRollingBack = false
		}
		c.mu.Unlock()
	}()

	c.logger.Info("initiating rollback",
		zap.String("namespace", req.Namespace),
		zap.String("deployment", req.DeploymentName),
		zap.Int64("target_revision", req.TargetRevision),
		zap.String("reason", req.Reason),
	)

	// Get current deployment state before rollback (for audit).
	currentStatus, err := c.client.GetFullDeploymentStatus(ctx, req.Namespace, req.DeploymentName)
	if err != nil {
		return nil, fmt.Errorf("getting current deployment status before rollback: %w", err)
	}

	previousImage := currentStatus.CurrentImage

	// Execute the rollback.
	rolledDeploy, err := c.client.RollbackDeployment(ctx, req.Namespace, req.DeploymentName, req.TargetRevision)
	if err != nil {
		result := &models.RollbackResult{
			Success:   false,
			Message:   fmt.Sprintf("rollback failed: %v", err),
			Timestamp: time.Now(),
		}
		c.recordResult(key, *result)
		return result, fmt.Errorf("executing rollback: %w", err)
	}

	// Determine the image we rolled back to.
	restoredImage := ""
	if len(rolledDeploy.Spec.Template.Spec.Containers) > 0 {
		restoredImage = rolledDeploy.Spec.Template.Spec.Containers[0].Image
	}

	c.logger.Info("rollback submitted, waiting for rollout",
		zap.String("namespace", req.Namespace),
		zap.String("deployment", req.DeploymentName),
		zap.String("previous_image", previousImage),
		zap.String("restored_image", restoredImage),
	)

	// Wait for the rollback rollout to complete.
	rolloutTimeout := 300 * time.Second
	pollInterval := 2 * time.Second

	policy := c.GetPolicy(req.Namespace, req.DeploymentName)
	if policy.HealthCheckTimeout > 0 {
		rolloutTimeout = policy.HealthCheckTimeout
	}
	if policy.HealthCheckInterval > 0 {
		pollInterval = policy.HealthCheckInterval
	}

	rolloutStatus, err := c.client.WaitForRollout(
		ctx,
		req.Namespace,
		req.DeploymentName,
		rolloutTimeout,
		pollInterval,
		func(rs *k8s.DeploymentRolloutStatus) {
			c.logger.Debug("rollback rollout progress",
				zap.String("deployment", req.DeploymentName),
				zap.Int32("ready", rs.ReadyReplicas),
				zap.Int32("desired", rs.DesiredReplicas),
				zap.String("message", rs.Message),
			)
		},
	)

	if err != nil {
		result := &models.RollbackResult{
			Success:       false,
			Message:       fmt.Sprintf("rollback rollout failed: %v", err),
			PreviousImage: previousImage,
			RestoredImage: restoredImage,
			Timestamp:     time.Now(),
		}
		c.recordResult(key, *result)
		return result, fmt.Errorf("waiting for rollback rollout: %w", err)
	}

	// Post-rollback health check.
	healthEvent := c.monitor.CheckNow(ctx, req.Namespace, req.DeploymentName)

	c.logger.Info("post-rollback health check",
		zap.String("deployment", req.DeploymentName),
		zap.String("overall_status", string(healthEvent.OverallStatus)),
		zap.String("summary", healthEvent.Summary),
	)

	// Build the result.
	result := &models.RollbackResult{
		Success:              rolloutStatus.Ready && healthEvent.OverallStatus != models.HealthUnhealthy,
		RolledBackToRevision: rolloutStatus.Revision,
		PreviousImage:        previousImage,
		RestoredImage:        restoredImage,
		Timestamp:            time.Now(),
	}

	if result.Success {
		result.Message = fmt.Sprintf("rollback successful: %s/%s reverted from %s to %s (revision %d), health: %s",
			req.Namespace, req.DeploymentName, previousImage, restoredImage,
			rolloutStatus.Revision, healthEvent.OverallStatus)
	} else {
		result.Message = fmt.Sprintf("rollback completed but deployment may be unhealthy: %s", healthEvent.Summary)
	}

	// Record the result and set cooldown.
	c.recordResult(key, *result)
	c.setCooldown(key, 60*time.Second)

	// Update policy state.
	c.mu.Lock()
	if state, ok := c.policies[key]; ok {
		state.LastRollbackAt = time.Now()
		if result.Success {
			state.RetryCount = 0
		}
	}
	c.mu.Unlock()

	// Fire callbacks.
	c.fireCallbacks(req.Namespace, req.DeploymentName, *result)

	c.logger.Info("rollback complete",
		zap.String("namespace", req.Namespace),
		zap.String("deployment", req.DeploymentName),
		zap.Bool("success", result.Success),
		zap.String("message", result.Message),
	)

	return result, nil
}

// GetHistory returns the rollback history for a deployment.
func (c *Controller) GetHistory(namespace, deploymentName string) []models.RollbackResult {
	key := policyKey(namespace, deploymentName)

	c.mu.RLock()
	defer c.mu.RUnlock()

	results, ok := c.history[key]
	if !ok {
		return nil
	}

	// Return a copy to avoid data races.
	out := make([]models.RollbackResult, len(results))
	copy(out, results)
	return out
}

// GetLastRollback returns the most recent rollback result for a deployment.
// Returns false if no rollback has been recorded.
func (c *Controller) GetLastRollback(namespace, deploymentName string) (models.RollbackResult, bool) {
	key := policyKey(namespace, deploymentName)

	c.mu.RLock()
	defer c.mu.RUnlock()

	results, ok := c.history[key]
	if !ok || len(results) == 0 {
		return models.RollbackResult{}, false
	}

	return results[len(results)-1], true
}

// IsRollingBack returns true if a rollback is currently in progress for
// the specified deployment.
func (c *Controller) IsRollingBack(namespace, deploymentName string) bool {
	key := policyKey(namespace, deploymentName)

	c.mu.RLock()
	defer c.mu.RUnlock()

	if state, ok := c.policies[key]; ok {
		return state.IsRollingBack
	}
	return false
}

// IsInCooldown returns true if the deployment is in a rollback cooldown period.
func (c *Controller) IsInCooldown(namespace, deploymentName string) bool {
	key := policyKey(namespace, deploymentName)

	c.mu.RLock()
	defer c.mu.RUnlock()

	if cooldownUntil, ok := c.cooldowns[key]; ok {
		return time.Now().Before(cooldownUntil)
	}
	return false
}

// OnRollback registers a callback that fires on every rollback event
// (both automatic and manual).
func (c *Controller) OnRollback(cb RollbackCallback) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onRollbackHook = append(c.onRollbackHook, cb)
}

// handleUnhealthy is the callback registered with the health monitor.
// It is called when a deployment crosses the failure threshold and is
// considered unhealthy. This triggers the automated rollback logic.
func (c *Controller) handleUnhealthy(namespace, deploymentName string, event models.HealthEvent) {
	key := policyKey(namespace, deploymentName)

	c.mu.RLock()
	state, hasPolicy := c.policies[key]
	var policy models.RollbackPolicy
	if hasPolicy {
		policy = state.Policy
	} else {
		policy = c.defaultPolicy
	}
	c.mu.RUnlock()

	// Check if auto-rollback is enabled.
	if !policy.Enabled {
		c.logger.Debug("auto-rollback disabled for deployment, ignoring unhealthy event",
			zap.String("namespace", namespace),
			zap.String("deployment", deploymentName),
		)
		return
	}

	// Check if explicitly disabled at the state level.
	if hasPolicy && !state.Enabled {
		c.logger.Debug("auto-rollback explicitly disabled for deployment",
			zap.String("namespace", namespace),
			zap.String("deployment", deploymentName),
		)
		return
	}

	// Check cooldown.
	c.mu.RLock()
	if cooldownUntil, ok := c.cooldowns[key]; ok && time.Now().Before(cooldownUntil) {
		c.mu.RUnlock()
		c.logger.Warn("auto-rollback skipped: deployment is in cooldown",
			zap.String("namespace", namespace),
			zap.String("deployment", deploymentName),
			zap.Duration("remaining", time.Until(cooldownUntil)),
		)
		return
	}
	c.mu.RUnlock()

	// Check if already rolling back.
	c.mu.RLock()
	if hasPolicy && state.IsRollingBack {
		c.mu.RUnlock()
		c.logger.Debug("rollback already in progress, skipping",
			zap.String("namespace", namespace),
			zap.String("deployment", deploymentName),
		)
		return
	}
	c.mu.RUnlock()

	// Check retry limit.
	c.mu.RLock()
	retryCount := 0
	if hasPolicy {
		retryCount = state.RetryCount
	}
	c.mu.RUnlock()

	if retryCount >= policy.MaxRetries {
		c.logger.Error("auto-rollback max retries exceeded, manual intervention required",
			zap.String("namespace", namespace),
			zap.String("deployment", deploymentName),
			zap.Int("retry_count", retryCount),
			zap.Int("max_retries", policy.MaxRetries),
		)
		return
	}

	c.logger.Warn("auto-rollback triggered by health monitor",
		zap.String("namespace", namespace),
		zap.String("deployment", deploymentName),
		zap.String("health_status", string(event.OverallStatus)),
		zap.String("summary", event.Summary),
		zap.Int("retry_attempt", retryCount+1),
		zap.Int("max_retries", policy.MaxRetries),
	)

	// Increment retry count before starting.
	c.mu.Lock()
	if !hasPolicy {
		state = &PolicyState{
			Namespace:      namespace,
			DeploymentName: deploymentName,
			Policy:         policy,
			Enabled:        true,
		}
		c.policies[key] = state
	}
	state.RetryCount++
	state.LastHealthEvent = event
	c.mu.Unlock()

	// Perform the rollback asynchronously to avoid blocking the health monitor.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), policy.HealthCheckTimeout)
		defer cancel()

		reason := fmt.Sprintf("auto-rollback: health monitor detected unhealthy state (%s) — %s",
			event.OverallStatus, event.Summary)

		req := models.RollbackRequest{
			Namespace:      namespace,
			DeploymentName: deploymentName,
			TargetRevision: 0, // Previous revision.
			Reason:         reason,
		}

		result, err := c.Rollback(ctx, req)
		if err != nil {
			c.logger.Error("auto-rollback failed",
				zap.String("namespace", namespace),
				zap.String("deployment", deploymentName),
				zap.Error(err),
			)
			return
		}

		if result.Success {
			c.logger.Info("auto-rollback succeeded",
				zap.String("namespace", namespace),
				zap.String("deployment", deploymentName),
				zap.String("restored_image", result.RestoredImage),
				zap.Int64("revision", result.RolledBackToRevision),
			)
		} else {
			c.logger.Warn("auto-rollback completed with warnings",
				zap.String("namespace", namespace),
				zap.String("deployment", deploymentName),
				zap.String("message", result.Message),
			)
		}
	}()
}

// recordResult appends a rollback result to the history for a deployment.
// It caps the history at 50 entries to prevent unbounded growth.
func (c *Controller) recordResult(key string, result models.RollbackResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.history[key]; !ok {
		c.history[key] = make([]models.RollbackResult, 0, 8)
	}

	c.history[key] = append(c.history[key], result)

	// Cap history length.
	const maxHistory = 50
	if len(c.history[key]) > maxHistory {
		c.history[key] = c.history[key][len(c.history[key])-maxHistory:]
	}
}

// setCooldown sets the rollback cooldown for a deployment.
func (c *Controller) setCooldown(key string, duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cooldowns[key] = time.Now().Add(duration)
}

// fireCallbacks invokes all registered rollback callbacks.
func (c *Controller) fireCallbacks(namespace, deploymentName string, result models.RollbackResult) {
	c.mu.RLock()
	hooks := make([]RollbackCallback, len(c.onRollbackHook))
	copy(hooks, c.onRollbackHook)
	c.mu.RUnlock()

	for _, hook := range hooks {
		go func(h RollbackCallback) {
			defer func() {
				if r := recover(); r != nil {
					c.logger.Error("panic in rollback callback",
						zap.Any("recover", r),
						zap.String("namespace", namespace),
						zap.String("deployment", deploymentName),
					)
				}
			}()
			h(namespace, deploymentName, result)
		}(hook)
	}
}

// policyKey returns the canonical map key for a namespace/deployment pair.
func policyKey(namespace, deploymentName string) string {
	return namespace + "/" + deploymentName
}
