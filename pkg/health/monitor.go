package health

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sonu/kube-deploy/pkg/k8s"
	"github.com/sonu/kube-deploy/pkg/models"
)

// Checker is the interface for pluggable health check implementations.
// Each checker targets a specific aspect of deployment health (pod readiness,
// restart counts, HTTP probes, etc.).
type Checker interface {
	// Type returns the health check type identifier.
	Type() models.HealthCheckType

	// Check performs a single health check and returns the result.
	// The context should be used for timeouts and cancellation.
	Check(ctx context.Context, namespace, deploymentName string) models.HealthCheckResult
}

// Monitor continuously watches the health of a Kubernetes Deployment by
// running a set of configurable health checks at a regular interval.
// It aggregates results into HealthEvents and provides both streaming
// (channel-based) and polling interfaces.
//
// The monitor supports:
//   - Pod readiness checks (are all pods Ready?)
//   - Restart count monitoring (CrashLoopBackOff detection)
//   - HTTP health endpoint probing (custom /healthz checks)
//   - Configurable failure/success thresholds for state transitions
//   - Callback hooks for integration with rollback controllers
type Monitor struct {
	client   *k8s.Client
	logger   *zap.Logger
	checkers []Checker

	mu               sync.RWMutex
	watches          map[string]*watchState
	onUnhealthyHooks []UnhealthyCallback
}

// UnhealthyCallback is invoked when a deployment transitions to an unhealthy state
// and the failure threshold is exceeded. This is the integration point for the
// rollback controller.
type UnhealthyCallback func(namespace, deploymentName string, event models.HealthEvent)

// watchState tracks the internal state of a health watch for a single deployment.
type watchState struct {
	namespace        string
	deploymentName   string
	interval         time.Duration
	cancel           context.CancelFunc
	lastEvent        models.HealthEvent
	consecutiveFail  int
	consecutivePass  int
	failThreshold    int
	successThreshold int
	started          time.Time
}

// MonitorOption is a functional option for configuring the Monitor.
type MonitorOption func(*Monitor)

// WithCheckers adds custom health checkers to the monitor.
func WithCheckers(checkers ...Checker) MonitorOption {
	return func(m *Monitor) {
		m.checkers = append(m.checkers, checkers...)
	}
}

// WithUnhealthyCallback registers a callback for unhealthy state transitions.
func WithUnhealthyCallback(cb UnhealthyCallback) MonitorOption {
	return func(m *Monitor) {
		m.onUnhealthyHooks = append(m.onUnhealthyHooks, cb)
	}
}

// NewMonitor creates a new health Monitor with the given Kubernetes client,
// logger, and optional configuration. If no checkers are provided, the default
// set (pod readiness + restart count) is registered.
func NewMonitor(client *k8s.Client, logger *zap.Logger, opts ...MonitorOption) *Monitor {
	if logger == nil {
		logger = zap.NewNop()
	}

	m := &Monitor{
		client:           client,
		logger:           logger.Named("health-monitor"),
		checkers:         make([]Checker, 0),
		watches:          make(map[string]*watchState),
		onUnhealthyHooks: make([]UnhealthyCallback, 0),
	}

	for _, opt := range opts {
		opt(m)
	}

	// Register default checkers if none were provided.
	if len(m.checkers) == 0 {
		m.checkers = append(m.checkers,
			NewPodReadinessChecker(client, logger),
			NewRestartCountChecker(client, logger, 5),
		)
	}

	return m
}

// AddChecker adds a health checker to the monitor at runtime.
func (m *Monitor) AddChecker(checker Checker) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkers = append(m.checkers, checker)
	m.logger.Info("added health checker", zap.String("type", string(checker.Type())))
}

// OnUnhealthy registers a callback that fires when a deployment crosses
// the failure threshold and is considered unhealthy.
func (m *Monitor) OnUnhealthy(cb UnhealthyCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onUnhealthyHooks = append(m.onUnhealthyHooks, cb)
}

// Watch starts continuous health monitoring for a deployment and returns a
// channel that emits HealthEvents at the configured interval. The watch
// runs until the context is cancelled or StopWatch is called.
//
// Parameters:
//   - ctx: parent context; cancelling stops the watch
//   - namespace: Kubernetes namespace of the deployment
//   - deploymentName: name of the Deployment to monitor
//   - interval: how often to run health checks
//   - failureThreshold: consecutive failures before declaring unhealthy
//   - successThreshold: consecutive successes before declaring healthy
//
// The returned channel is closed when the watch terminates.
func (m *Monitor) Watch(
	ctx context.Context,
	namespace, deploymentName string,
	interval time.Duration,
	failureThreshold, successThreshold int,
) (<-chan models.HealthEvent, error) {

	key := watchKey(namespace, deploymentName)

	m.mu.Lock()
	// Stop any existing watch for this deployment.
	if existing, ok := m.watches[key]; ok {
		existing.cancel()
		delete(m.watches, key)
	}

	if interval <= 0 {
		interval = 5 * time.Second
	}
	if failureThreshold <= 0 {
		failureThreshold = 3
	}
	if successThreshold <= 0 {
		successThreshold = 1
	}

	watchCtx, cancel := context.WithCancel(ctx)
	state := &watchState{
		namespace:        namespace,
		deploymentName:   deploymentName,
		interval:         interval,
		cancel:           cancel,
		failThreshold:    failureThreshold,
		successThreshold: successThreshold,
		started:          time.Now(),
	}
	m.watches[key] = state
	m.mu.Unlock()

	events := make(chan models.HealthEvent, 32)

	m.logger.Info("starting health watch",
		zap.String("namespace", namespace),
		zap.String("deployment", deploymentName),
		zap.Duration("interval", interval),
		zap.Int("failure_threshold", failureThreshold),
		zap.Int("success_threshold", successThreshold),
	)

	go m.runWatch(watchCtx, state, events)

	return events, nil
}

// StopWatch stops the health watch for a specific deployment.
func (m *Monitor) StopWatch(namespace, deploymentName string) {
	key := watchKey(namespace, deploymentName)

	m.mu.Lock()
	defer m.mu.Unlock()

	if state, ok := m.watches[key]; ok {
		state.cancel()
		delete(m.watches, key)
		m.logger.Info("stopped health watch",
			zap.String("namespace", namespace),
			zap.String("deployment", deploymentName),
		)
	}
}

// StopAll stops all active health watches.
func (m *Monitor) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for key, state := range m.watches {
		state.cancel()
		delete(m.watches, key)
	}
	m.logger.Info("stopped all health watches")
}

// IsWatching returns true if there is an active health watch for the deployment.
func (m *Monitor) IsWatching(namespace, deploymentName string) bool {
	key := watchKey(namespace, deploymentName)
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.watches[key]
	return ok
}

// GetLastEvent returns the most recent HealthEvent for a watched deployment.
// Returns false if the deployment is not being watched or no event has been recorded.
func (m *Monitor) GetLastEvent(namespace, deploymentName string) (models.HealthEvent, bool) {
	key := watchKey(namespace, deploymentName)
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, ok := m.watches[key]
	if !ok || state.lastEvent.Timestamp.IsZero() {
		return models.HealthEvent{}, false
	}
	return state.lastEvent, true
}

// CheckNow performs an immediate, one-shot health check for a deployment
// without requiring an active watch. Returns a single HealthEvent with
// results from all registered checkers.
func (m *Monitor) CheckNow(ctx context.Context, namespace, deploymentName string) models.HealthEvent {
	m.mu.RLock()
	checkers := make([]Checker, len(m.checkers))
	copy(checkers, m.checkers)
	m.mu.RUnlock()

	return m.runChecks(ctx, namespace, deploymentName, checkers)
}

// ActiveWatchCount returns the number of active health watches.
func (m *Monitor) ActiveWatchCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.watches)
}

// runWatch is the main loop for a health watch goroutine. It runs health checks
// at the configured interval and sends events to the channel. It tracks
// consecutive success/failure counts and triggers unhealthy callbacks.
func (m *Monitor) runWatch(ctx context.Context, state *watchState, events chan<- models.HealthEvent) {
	defer close(events)

	ticker := time.NewTicker(state.interval)
	defer ticker.Stop()

	m.logger.Debug("health watch loop started",
		zap.String("namespace", state.namespace),
		zap.String("deployment", state.deploymentName),
	)

	for {
		select {
		case <-ctx.Done():
			m.logger.Debug("health watch context cancelled",
				zap.String("namespace", state.namespace),
				zap.String("deployment", state.deploymentName),
			)
			return

		case <-ticker.C:
			m.mu.RLock()
			checkers := make([]Checker, len(m.checkers))
			copy(checkers, m.checkers)
			m.mu.RUnlock()

			event := m.runChecks(ctx, state.namespace, state.deploymentName, checkers)

			// Update consecutive counters based on the overall status.
			m.mu.Lock()
			prevStatus := state.lastEvent.OverallStatus
			state.lastEvent = event

			switch event.OverallStatus {
			case models.HealthHealthy:
				state.consecutivePass++
				state.consecutiveFail = 0
			case models.HealthDegraded:
				// Degraded resets both counters — it's a transitional state.
				state.consecutivePass = 0
				state.consecutiveFail++
			case models.HealthUnhealthy:
				state.consecutiveFail++
				state.consecutivePass = 0
			default:
				// Unknown — don't change counters.
			}

			consecutiveFail := state.consecutiveFail
			failThreshold := state.failThreshold
			m.mu.Unlock()

			// Send the event to the channel (non-blocking to avoid deadlocks).
			select {
			case events <- event:
			default:
				m.logger.Warn("health event channel full, dropping event",
					zap.String("namespace", state.namespace),
					zap.String("deployment", state.deploymentName),
				)
			}

			// Fire unhealthy callbacks if we crossed the failure threshold.
			if consecutiveFail >= failThreshold &&
				(prevStatus == models.HealthHealthy || prevStatus == models.HealthDegraded || prevStatus == models.HealthUnknown || prevStatus == "") {
				m.logger.Warn("deployment crossed unhealthy threshold",
					zap.String("namespace", state.namespace),
					zap.String("deployment", state.deploymentName),
					zap.Int("consecutive_failures", consecutiveFail),
					zap.Int("threshold", failThreshold),
				)
				m.fireUnhealthyCallbacks(state.namespace, state.deploymentName, event)
			}
		}
	}
}

// runChecks executes all registered checkers against a deployment and returns
// an aggregated HealthEvent.
func (m *Monitor) runChecks(ctx context.Context, namespace, deploymentName string, checkers []Checker) models.HealthEvent {
	results := make([]models.HealthCheckResult, 0, len(checkers))

	for _, checker := range checkers {
		checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		result := checker.Check(checkCtx, namespace, deploymentName)
		cancel()
		results = append(results, result)
	}

	event := models.HealthEvent{
		Namespace:      namespace,
		DeploymentName: deploymentName,
		Results:        results,
		Timestamp:      time.Now(),
	}

	// Compute overall status from individual results.
	event.OverallStatus = event.ComputeOverallStatus()

	// Build a human-readable summary.
	healthy := 0
	unhealthy := 0
	degraded := 0
	for _, r := range results {
		switch r.Status {
		case models.HealthHealthy:
			healthy++
		case models.HealthUnhealthy:
			unhealthy++
		case models.HealthDegraded:
			degraded++
		}
	}

	event.Summary = fmt.Sprintf("%d/%d checks healthy", healthy, len(results))
	if unhealthy > 0 {
		event.Summary += fmt.Sprintf(", %d unhealthy", unhealthy)
	}
	if degraded > 0 {
		event.Summary += fmt.Sprintf(", %d degraded", degraded)
	}

	return event
}

// fireUnhealthyCallbacks invokes all registered unhealthy callbacks.
func (m *Monitor) fireUnhealthyCallbacks(namespace, deploymentName string, event models.HealthEvent) {
	m.mu.RLock()
	hooks := make([]UnhealthyCallback, len(m.onUnhealthyHooks))
	copy(hooks, m.onUnhealthyHooks)
	m.mu.RUnlock()

	for _, hook := range hooks {
		go func(h UnhealthyCallback) {
			defer func() {
				if r := recover(); r != nil {
					m.logger.Error("panic in unhealthy callback",
						zap.Any("recover", r),
						zap.String("namespace", namespace),
						zap.String("deployment", deploymentName),
					)
				}
			}()
			h(namespace, deploymentName, event)
		}(hook)
	}
}

// watchKey returns the canonical map key for a namespace/deployment pair.
func watchKey(namespace, deploymentName string) string {
	return namespace + "/" + deploymentName
}

// ============================================================================
// Built-in Health Checkers
// ============================================================================

// PodReadinessChecker verifies that all pods in a deployment are in a Ready
// condition. This is the most fundamental health check — if pods aren't ready,
// the deployment is not serving traffic.
type PodReadinessChecker struct {
	client *k8s.Client
	logger *zap.Logger
}

// NewPodReadinessChecker creates a new pod readiness checker.
func NewPodReadinessChecker(client *k8s.Client, logger *zap.Logger) *PodReadinessChecker {
	return &PodReadinessChecker{
		client: client,
		logger: logger.Named("check-readiness"),
	}
}

// Type returns the checker type identifier.
func (c *PodReadinessChecker) Type() models.HealthCheckType {
	return models.HealthCheckPodReadiness
}

// Check verifies pod readiness for the deployment.
func (c *PodReadinessChecker) Check(ctx context.Context, namespace, deploymentName string) models.HealthCheckResult {
	start := time.Now()

	result := models.HealthCheckResult{
		Type:      models.HealthCheckPodReadiness,
		Target:    fmt.Sprintf("%s/%s", namespace, deploymentName),
		CheckedAt: start,
		Metadata:  make(map[string]string),
	}

	status, err := c.client.GetDeploymentRolloutStatus(ctx, namespace, deploymentName)
	if err != nil {
		result.Status = models.HealthUnknown
		result.Message = fmt.Sprintf("failed to get deployment status: %v", err)
		result.Latency = time.Since(start)
		return result
	}

	result.Latency = time.Since(start)
	result.Metadata["ready_replicas"] = fmt.Sprintf("%d", status.ReadyReplicas)
	result.Metadata["desired_replicas"] = fmt.Sprintf("%d", status.DesiredReplicas)
	result.Metadata["available_replicas"] = fmt.Sprintf("%d", status.AvailableReplicas)
	result.Metadata["updated_replicas"] = fmt.Sprintf("%d", status.UpdatedReplicas)

	if status.Ready {
		result.Status = models.HealthHealthy
		result.Message = fmt.Sprintf("all %d/%d pods ready and available",
			status.ReadyReplicas, status.DesiredReplicas)
	} else if status.ReadyReplicas > 0 && status.ReadyReplicas < status.DesiredReplicas {
		result.Status = models.HealthDegraded
		result.Message = fmt.Sprintf("partially ready: %d/%d pods ready, %d/%d available",
			status.ReadyReplicas, status.DesiredReplicas,
			status.AvailableReplicas, status.DesiredReplicas)
	} else if status.ReadyReplicas == 0 && status.DesiredReplicas > 0 {
		result.Status = models.HealthUnhealthy
		result.Message = fmt.Sprintf("no pods ready: 0/%d desired", status.DesiredReplicas)
	} else {
		result.Status = models.HealthUnknown
		result.Message = status.Message
	}

	return result
}

// RestartCountChecker monitors the restart count of pods in a deployment.
// A spike in restart counts indicates CrashLoopBackOff or other instability.
type RestartCountChecker struct {
	client          *k8s.Client
	logger          *zap.Logger
	maxRestartCount int32

	mu            sync.RWMutex
	baselineCache map[string]int32
}

// NewRestartCountChecker creates a new restart count checker.
// maxRestartCount is the threshold above which a pod is considered unhealthy.
func NewRestartCountChecker(client *k8s.Client, logger *zap.Logger, maxRestartCount int32) *RestartCountChecker {
	return &RestartCountChecker{
		client:          client,
		logger:          logger.Named("check-restarts"),
		maxRestartCount: maxRestartCount,
		baselineCache:   make(map[string]int32),
	}
}

// Type returns the checker type identifier.
func (c *RestartCountChecker) Type() models.HealthCheckType {
	return models.HealthCheckRestartCount
}

// SetBaseline records the current restart count as the baseline for a deployment.
// Subsequent checks will report restart deltas from this baseline.
func (c *RestartCountChecker) SetBaseline(namespace, deploymentName string, count int32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := namespace + "/" + deploymentName
	c.baselineCache[key] = count
}

// Check verifies that pod restart counts haven't exceeded the threshold.
func (c *RestartCountChecker) Check(ctx context.Context, namespace, deploymentName string) models.HealthCheckResult {
	start := time.Now()
	key := namespace + "/" + deploymentName

	result := models.HealthCheckResult{
		Type:      models.HealthCheckRestartCount,
		Target:    key,
		CheckedAt: start,
		Metadata:  make(map[string]string),
	}

	podStatuses, err := c.client.GetPodStatuses(ctx, namespace, deploymentName)
	if err != nil {
		result.Status = models.HealthUnknown
		result.Message = fmt.Sprintf("failed to get pod statuses: %v", err)
		result.Latency = time.Since(start)
		return result
	}

	result.Latency = time.Since(start)

	// Get baseline.
	c.mu.RLock()
	baseline := c.baselineCache[key]
	c.mu.RUnlock()

	totalRestarts := int32(0)
	maxPodRestarts := int32(0)
	crashLooping := false
	var worstPod string

	for _, pod := range podStatuses {
		totalRestarts += pod.RestartCount
		if pod.RestartCount > maxPodRestarts {
			maxPodRestarts = pod.RestartCount
			worstPod = pod.Name
		}
		// Detect CrashLoopBackOff from the pod message.
		if pod.Message != "" && !pod.Ready {
			crashLooping = true
		}
	}

	delta := totalRestarts - baseline
	result.Metadata["total_restarts"] = fmt.Sprintf("%d", totalRestarts)
	result.Metadata["baseline"] = fmt.Sprintf("%d", baseline)
	result.Metadata["delta"] = fmt.Sprintf("%d", delta)
	result.Metadata["max_pod_restarts"] = fmt.Sprintf("%d", maxPodRestarts)
	result.Metadata["pod_count"] = fmt.Sprintf("%d", len(podStatuses))
	if worstPod != "" {
		result.Metadata["worst_pod"] = worstPod
	}

	if crashLooping {
		result.Status = models.HealthUnhealthy
		result.Message = fmt.Sprintf("CrashLoopBackOff detected: total restarts %d (delta: %d), worst pod: %s",
			totalRestarts, delta, worstPod)
	} else if maxPodRestarts > c.maxRestartCount {
		result.Status = models.HealthUnhealthy
		result.Message = fmt.Sprintf("pod %s has %d restarts (threshold: %d)",
			worstPod, maxPodRestarts, c.maxRestartCount)
	} else if delta > c.maxRestartCount {
		result.Status = models.HealthDegraded
		result.Message = fmt.Sprintf("restart count increased by %d since baseline (threshold: %d)",
			delta, c.maxRestartCount)
	} else {
		result.Status = models.HealthHealthy
		result.Message = fmt.Sprintf("restart counts normal: total %d, delta %d (threshold: %d)",
			totalRestarts, delta, c.maxRestartCount)
	}

	return result
}

// HTTPProbeChecker performs HTTP health checks against pod endpoints.
// It sends GET requests to a configurable health endpoint on each pod
// and expects a 2xx response.
type HTTPProbeChecker struct {
	client     *k8s.Client
	logger     *zap.Logger
	httpClient *http.Client
	endpoint   string
	port       int
}

// NewHTTPProbeChecker creates a new HTTP probe checker.
//   - endpoint: the HTTP path to probe (e.g., "/healthz")
//   - port: the container port to probe
//   - timeout: HTTP request timeout
func NewHTTPProbeChecker(client *k8s.Client, logger *zap.Logger, endpoint string, port int, timeout time.Duration) *HTTPProbeChecker {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &HTTPProbeChecker{
		client: client,
		logger: logger.Named("check-http"),
		httpClient: &http.Client{
			Timeout: timeout,
		},
		endpoint: endpoint,
		port:     port,
	}
}

// Type returns the checker type identifier.
func (c *HTTPProbeChecker) Type() models.HealthCheckType {
	return models.HealthCheckHTTPProbe
}

// Check performs HTTP health probes against the deployment's pods.
// It probes each pod's IP directly using the pod network.
// All pods must return 2xx for the check to pass.
func (c *HTTPProbeChecker) Check(ctx context.Context, namespace, deploymentName string) models.HealthCheckResult {
	start := time.Now()

	result := models.HealthCheckResult{
		Type:      models.HealthCheckHTTPProbe,
		Target:    fmt.Sprintf("%s/%s%s", namespace, deploymentName, c.endpoint),
		CheckedAt: start,
		Metadata:  make(map[string]string),
	}

	pods, err := c.client.GetDeploymentPods(ctx, namespace, deploymentName)
	if err != nil {
		result.Status = models.HealthUnknown
		result.Message = fmt.Sprintf("failed to list pods: %v", err)
		result.Latency = time.Since(start)
		return result
	}

	if len(pods) == 0 {
		result.Status = models.HealthUnknown
		result.Message = "no pods found for deployment"
		result.Latency = time.Since(start)
		return result
	}

	totalPods := len(pods)
	healthyPods := 0
	var lastError string

	for _, pod := range pods {
		podIP := pod.Status.PodIP
		if podIP == "" {
			lastError = fmt.Sprintf("pod %s has no IP", pod.Name)
			continue
		}

		url := fmt.Sprintf("http://%s:%d%s", podIP, c.port, c.endpoint)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			lastError = fmt.Sprintf("failed to create request for pod %s: %v", pod.Name, err)
			continue
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastError = fmt.Sprintf("pod %s probe failed: %v", pod.Name, err)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			healthyPods++
		} else {
			lastError = fmt.Sprintf("pod %s returned HTTP %d", pod.Name, resp.StatusCode)
		}
	}

	result.Latency = time.Since(start)
	result.Metadata["total_pods"] = fmt.Sprintf("%d", totalPods)
	result.Metadata["healthy_pods"] = fmt.Sprintf("%d", healthyPods)
	result.Metadata["endpoint"] = c.endpoint
	result.Metadata["port"] = fmt.Sprintf("%d", c.port)

	if healthyPods == totalPods {
		result.Status = models.HealthHealthy
		result.Message = fmt.Sprintf("all %d pods passed HTTP probe on %s", totalPods, c.endpoint)
	} else if healthyPods > 0 {
		result.Status = models.HealthDegraded
		result.Message = fmt.Sprintf("%d/%d pods passed HTTP probe: %s", healthyPods, totalPods, lastError)
	} else {
		result.Status = models.HealthUnhealthy
		result.Message = fmt.Sprintf("all %d pods failed HTTP probe: %s", totalPods, lastError)
	}

	return result
}

// ============================================================================
// Builder helpers for creating Monitor with common configurations
// ============================================================================

// NewMonitorWithHTTPProbe creates a Monitor pre-configured with pod readiness,
// restart count, and HTTP probe checkers. This is the recommended setup for
// applications that expose a health endpoint.
func NewMonitorWithHTTPProbe(
	client *k8s.Client,
	logger *zap.Logger,
	httpEndpoint string,
	httpPort int,
	httpTimeout time.Duration,
	maxRestarts int32,
	opts ...MonitorOption,
) *Monitor {
	checkers := []Checker{
		NewPodReadinessChecker(client, logger),
		NewRestartCountChecker(client, logger, maxRestarts),
		NewHTTPProbeChecker(client, logger, httpEndpoint, httpPort, httpTimeout),
	}

	allOpts := []MonitorOption{WithCheckers(checkers...)}
	allOpts = append(allOpts, opts...)

	m := &Monitor{
		client:           client,
		logger:           logger.Named("health-monitor"),
		checkers:         make([]Checker, 0),
		watches:          make(map[string]*watchState),
		onUnhealthyHooks: make([]UnhealthyCallback, 0),
	}

	for _, opt := range allOpts {
		opt(m)
	}

	return m
}
