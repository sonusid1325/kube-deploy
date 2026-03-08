package models

import (
	"fmt"
	"reflect"
	"time"
)

// DeployStrategy defines the deployment strategy to use.
type DeployStrategy string

const (
	StrategyRolling   DeployStrategy = "rolling"
	StrategyCanary    DeployStrategy = "canary"
	StrategyBlueGreen DeployStrategy = "blue-green"
)

// DeploymentPhase represents the current phase of a deployment.
type DeploymentPhase string

const (
	PhasePending     DeploymentPhase = "pending"
	PhaseInProgress  DeploymentPhase = "in-progress"
	PhaseHealthCheck DeploymentPhase = "health-check"
	PhasePromoting   DeploymentPhase = "promoting"
	PhaseRollingBack DeploymentPhase = "rolling-back"
	PhaseCompleted   DeploymentPhase = "completed"
	PhaseFailed      DeploymentPhase = "failed"
	PhaseRolledBack  DeploymentPhase = "rolled-back"
)

// IsTerminal returns true if the phase represents a final state.
func (p DeploymentPhase) IsTerminal() bool {
	return p == PhaseCompleted || p == PhaseFailed || p == PhaseRolledBack
}

// HealthStatus represents the health state of a deployment or pod.
type HealthStatus string

const (
	HealthHealthy   HealthStatus = "healthy"
	HealthDegraded  HealthStatus = "degraded"
	HealthUnhealthy HealthStatus = "unhealthy"
	HealthUnknown   HealthStatus = "unknown"
)

// HealthCheckType identifies the kind of health check being performed.
type HealthCheckType string

const (
	HealthCheckPodReadiness HealthCheckType = "pod-readiness"
	HealthCheckRestartCount HealthCheckType = "restart-count"
	HealthCheckHTTPProbe    HealthCheckType = "http-probe"
	HealthCheckCustomMetric HealthCheckType = "custom-metric"
)

// --------------------------------------------------------------------------
// Deployment Target & Request
// --------------------------------------------------------------------------

// DeploymentTarget identifies the Kubernetes resource to deploy to.
type DeploymentTarget struct {
	Namespace      string `json:"namespace" yaml:"namespace"`
	DeploymentName string `json:"deploymentName" yaml:"deploymentName"`
	ContainerName  string `json:"containerName,omitempty" yaml:"containerName,omitempty"`
	Image          string `json:"image" yaml:"image"`
}

// Validate checks that all required fields are set.
func (t *DeploymentTarget) Validate() error {
	if t.Namespace == "" {
		return fmt.Errorf("namespace is required")
	}
	if t.DeploymentName == "" {
		return fmt.Errorf("deployment name is required")
	}
	if t.Image == "" {
		return fmt.Errorf("image is required")
	}
	return nil
}

// RollingUpdateConfig holds configuration for a rolling update strategy.
type RollingUpdateConfig struct {
	MaxUnavailable int `json:"maxUnavailable" yaml:"maxUnavailable"`
	MaxSurge       int `json:"maxSurge" yaml:"maxSurge"`
}

// DefaultRollingUpdateConfig returns a zero-downtime rolling update config.
func DefaultRollingUpdateConfig() RollingUpdateConfig {
	return RollingUpdateConfig{
		MaxUnavailable: 0,
		MaxSurge:       1,
	}
}

// CanaryConfig holds configuration for a canary deployment strategy.
type CanaryConfig struct {
	CanaryReplicas   int           `json:"canaryReplicas" yaml:"canaryReplicas"`
	CanaryPercent    int           `json:"canaryPercent" yaml:"canaryPercent"`
	AnalysisDuration time.Duration `json:"analysisDuration" yaml:"analysisDuration"`
	SuccessThreshold int           `json:"successThreshold" yaml:"successThreshold"`
	Steps            int           `json:"steps" yaml:"steps"`
	StepWeights      []int         `json:"stepWeights,omitempty" yaml:"stepWeights,omitempty"`
}

// DefaultCanaryConfig returns sensible defaults for canary deployments.
func DefaultCanaryConfig() CanaryConfig {
	return CanaryConfig{
		CanaryReplicas:   1,
		CanaryPercent:    10,
		AnalysisDuration: 60 * time.Second,
		SuccessThreshold: 3,
		Steps:            5,
		StepWeights:      []int{10, 25, 50, 75, 100},
	}
}

// RollbackPolicy defines when and how automated rollback should occur.
type RollbackPolicy struct {
	Enabled             bool          `json:"enabled" yaml:"enabled"`
	MaxRetries          int           `json:"maxRetries" yaml:"maxRetries"`
	HealthCheckInterval time.Duration `json:"healthCheckInterval" yaml:"healthCheckInterval"`
	HealthCheckTimeout  time.Duration `json:"healthCheckTimeout" yaml:"healthCheckTimeout"`
	FailureThreshold    int           `json:"failureThreshold" yaml:"failureThreshold"`
	SuccessThreshold    int           `json:"successThreshold" yaml:"successThreshold"`
}

// DefaultRollbackPolicy returns a sensible default rollback policy.
func DefaultRollbackPolicy() RollbackPolicy {
	return RollbackPolicy{
		Enabled:             true,
		MaxRetries:          2,
		HealthCheckInterval: 10 * time.Second,
		HealthCheckTimeout:  120 * time.Second,
		FailureThreshold:    3,
		SuccessThreshold:    2,
	}
}

// HealthCheckConfig describes a single health check to run against a deployment.
type HealthCheckConfig struct {
	Type             HealthCheckType `json:"type" yaml:"type"`
	HTTPEndpoint     string          `json:"httpEndpoint,omitempty" yaml:"httpEndpoint,omitempty"`
	Interval         time.Duration   `json:"interval" yaml:"interval"`
	Timeout          time.Duration   `json:"timeout" yaml:"timeout"`
	FailureThreshold int             `json:"failureThreshold" yaml:"failureThreshold"`
	SuccessThreshold int             `json:"successThreshold" yaml:"successThreshold"`
	MaxRestartCount  int             `json:"maxRestartCount,omitempty" yaml:"maxRestartCount,omitempty"`
}

// DefaultHealthCheckConfig returns a default pod-readiness health check config.
func DefaultHealthCheckConfig() HealthCheckConfig {
	return HealthCheckConfig{
		Type:             HealthCheckPodReadiness,
		Interval:         5 * time.Second,
		Timeout:          30 * time.Second,
		FailureThreshold: 3,
		SuccessThreshold: 1,
	}
}

// --------------------------------------------------------------------------
// Deployment Request
// --------------------------------------------------------------------------

// DeploymentRequest is the full specification for initiating a deployment.
type DeploymentRequest struct {
	DeployID       string              `json:"deployId" yaml:"deployId"`
	Target         DeploymentTarget    `json:"target" yaml:"target"`
	Strategy       DeployStrategy      `json:"strategy" yaml:"strategy"`
	RollingConfig  RollingUpdateConfig `json:"rollingConfig,omitempty" yaml:"rollingConfig,omitempty"`
	CanaryConfig   CanaryConfig        `json:"canaryConfig,omitempty" yaml:"canaryConfig,omitempty"`
	RollbackPolicy RollbackPolicy      `json:"rollbackPolicy" yaml:"rollbackPolicy"`
	HealthChecks   []HealthCheckConfig `json:"healthChecks,omitempty" yaml:"healthChecks,omitempty"`
	Labels         map[string]string   `json:"labels,omitempty" yaml:"labels,omitempty"`
	Annotations    map[string]string   `json:"annotations,omitempty" yaml:"annotations,omitempty"`
	DryRun         bool                `json:"dryRun,omitempty" yaml:"dryRun,omitempty"`
}

// Validate ensures the deployment request has all required fields and valid configuration.
func (r *DeploymentRequest) Validate() error {
	if r.DeployID == "" {
		return fmt.Errorf("deploy ID is required")
	}
	if err := r.Target.Validate(); err != nil {
		return fmt.Errorf("invalid target: %w", err)
	}
	switch r.Strategy {
	case StrategyRolling, StrategyCanary, StrategyBlueGreen:
		// valid
	default:
		return fmt.Errorf("unsupported deploy strategy: %q", r.Strategy)
	}
	if r.Strategy == StrategyCanary && r.CanaryConfig.CanaryReplicas <= 0 {
		return fmt.Errorf("canary strategy requires canaryReplicas > 0")
	}
	return nil
}

// ApplyDefaults fills in zero-value fields with sensible defaults.
func (r *DeploymentRequest) ApplyDefaults() {
	if r.Strategy == "" {
		r.Strategy = StrategyRolling
	}
	if r.Strategy == StrategyRolling && r.RollingConfig == (RollingUpdateConfig{}) {
		r.RollingConfig = DefaultRollingUpdateConfig()
	}
	if r.Strategy == StrategyCanary && reflect.DeepEqual(r.CanaryConfig, CanaryConfig{}) {
		r.CanaryConfig = DefaultCanaryConfig()
	}
	if r.RollbackPolicy == (RollbackPolicy{}) {
		r.RollbackPolicy = DefaultRollbackPolicy()
	}
	if len(r.HealthChecks) == 0 {
		r.HealthChecks = []HealthCheckConfig{DefaultHealthCheckConfig()}
	}
}

// --------------------------------------------------------------------------
// Deployment Events & Status
// --------------------------------------------------------------------------

// DeployEvent represents a single progress event during a deployment.
type DeployEvent struct {
	DeployID          string          `json:"deployId"`
	Phase             DeploymentPhase `json:"phase"`
	Message           string          `json:"message"`
	Timestamp         time.Time       `json:"timestamp"`
	ReadyReplicas     int32           `json:"readyReplicas"`
	DesiredReplicas   int32           `json:"desiredReplicas"`
	UpdatedReplicas   int32           `json:"updatedReplicas"`
	AvailableReplicas int32           `json:"availableReplicas"`
	CurrentImage      string          `json:"currentImage"`
	TargetImage       string          `json:"targetImage"`
	Revision          int64           `json:"revision"`
	IsTerminal        bool            `json:"isTerminal"`
	ErrorDetail       string          `json:"errorDetail,omitempty"`
}

// NewDeployEvent creates a new event with the timestamp set to now.
func NewDeployEvent(deployID string, phase DeploymentPhase, message string) DeployEvent {
	return DeployEvent{
		DeployID:   deployID,
		Phase:      phase,
		Message:    message,
		Timestamp:  time.Now(),
		IsTerminal: phase.IsTerminal(),
	}
}

// PodStatus represents the current state of a single pod.
type PodStatus struct {
	Name         string    `json:"name"`
	Phase        string    `json:"phase"`
	Ready        bool      `json:"ready"`
	RestartCount int32     `json:"restartCount"`
	NodeName     string    `json:"nodeName"`
	Image        string    `json:"image"`
	StartTime    time.Time `json:"startTime,omitempty"`
	Message      string    `json:"message,omitempty"`
}

// DeploymentStatus represents the current overall state of a tracked deployment.
type DeploymentStatus struct {
	Namespace         string          `json:"namespace"`
	DeploymentName    string          `json:"deploymentName"`
	Phase             DeploymentPhase `json:"phase"`
	CurrentImage      string          `json:"currentImage"`
	ReadyReplicas     int32           `json:"readyReplicas"`
	DesiredReplicas   int32           `json:"desiredReplicas"`
	UpdatedReplicas   int32           `json:"updatedReplicas"`
	AvailableReplicas int32           `json:"availableReplicas"`
	CurrentRevision   int64           `json:"currentRevision"`
	HealthStatus      HealthStatus    `json:"healthStatus"`
	LastUpdated       time.Time       `json:"lastUpdated"`
	Pods              []PodStatus     `json:"pods,omitempty"`
	Conditions        []string        `json:"conditions,omitempty"`
}

// IsReady returns true when all desired replicas are ready and available.
func (s *DeploymentStatus) IsReady() bool {
	return s.ReadyReplicas == s.DesiredReplicas &&
		s.AvailableReplicas == s.DesiredReplicas &&
		s.UpdatedReplicas == s.DesiredReplicas
}

// --------------------------------------------------------------------------
// Health Check Results
// --------------------------------------------------------------------------

// HealthCheckResult is the outcome of a single health check execution.
type HealthCheckResult struct {
	Type      HealthCheckType   `json:"type"`
	Status    HealthStatus      `json:"status"`
	Message   string            `json:"message"`
	Target    string            `json:"target"`
	Latency   time.Duration     `json:"latency"`
	CheckedAt time.Time         `json:"checkedAt"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// HealthEvent aggregates multiple health check results for a deployment.
type HealthEvent struct {
	Namespace      string              `json:"namespace"`
	DeploymentName string              `json:"deploymentName"`
	OverallStatus  HealthStatus        `json:"overallStatus"`
	Results        []HealthCheckResult `json:"results"`
	Timestamp      time.Time           `json:"timestamp"`
	Summary        string              `json:"summary"`
}

// ComputeOverallStatus derives the worst health status from all results.
func (e *HealthEvent) ComputeOverallStatus() HealthStatus {
	if len(e.Results) == 0 {
		return HealthUnknown
	}
	worst := HealthHealthy
	for _, r := range e.Results {
		if statusSeverity(r.Status) > statusSeverity(worst) {
			worst = r.Status
		}
	}
	return worst
}

func statusSeverity(s HealthStatus) int {
	switch s {
	case HealthHealthy:
		return 0
	case HealthDegraded:
		return 1
	case HealthUnhealthy:
		return 2
	case HealthUnknown:
		return 3
	default:
		return 4
	}
}

// --------------------------------------------------------------------------
// Deployment History / Revisions
// --------------------------------------------------------------------------

// DeploymentRevision represents a single revision in deployment history.
type DeploymentRevision struct {
	Revision       int64             `json:"revision"`
	Image          string            `json:"image"`
	Result         DeploymentPhase   `json:"result"`
	Strategy       DeployStrategy    `json:"strategy"`
	DeployedAt     time.Time         `json:"deployedAt"`
	CompletedAt    time.Time         `json:"completedAt,omitempty"`
	DeployedBy     string            `json:"deployedBy,omitempty"`
	RollbackReason string            `json:"rollbackReason,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	Replicas       int32             `json:"replicas"`
}

// Duration returns the time taken from deployment start to completion.
// Returns zero if the deployment has not completed.
func (r *DeploymentRevision) Duration() time.Duration {
	if r.CompletedAt.IsZero() {
		return 0
	}
	return r.CompletedAt.Sub(r.DeployedAt)
}

// --------------------------------------------------------------------------
// Rollback Request & Response
// --------------------------------------------------------------------------

// RollbackRequest specifies a manual rollback operation.
type RollbackRequest struct {
	Namespace      string `json:"namespace"`
	DeploymentName string `json:"deploymentName"`
	TargetRevision int64  `json:"targetRevision"`
	Reason         string `json:"reason"`
}

// Validate ensures the rollback request is valid.
func (r *RollbackRequest) Validate() error {
	if r.Namespace == "" {
		return fmt.Errorf("namespace is required")
	}
	if r.DeploymentName == "" {
		return fmt.Errorf("deployment name is required")
	}
	if r.TargetRevision < 0 {
		return fmt.Errorf("target revision must be >= 0 (0 means previous)")
	}
	return nil
}

// RollbackResult is the outcome of a rollback operation.
type RollbackResult struct {
	Success              bool      `json:"success"`
	Message              string    `json:"message"`
	RolledBackToRevision int64     `json:"rolledBackToRevision"`
	PreviousImage        string    `json:"previousImage"`
	RestoredImage        string    `json:"restoredImage"`
	Timestamp            time.Time `json:"timestamp"`
}

// --------------------------------------------------------------------------
// Deployment Tracker (in-memory state)
// --------------------------------------------------------------------------

// DeploymentTracker holds the full state of a tracked deployment lifecycle.
type DeploymentTracker struct {
	Request    DeploymentRequest    `json:"request"`
	Status     DeploymentStatus     `json:"status"`
	Events     []DeployEvent        `json:"events"`
	History    []DeploymentRevision `json:"history"`
	StartedAt  time.Time            `json:"startedAt"`
	FinishedAt time.Time            `json:"finishedAt,omitempty"`
}

// NewDeploymentTracker creates a new tracker for the given request.
func NewDeploymentTracker(req DeploymentRequest) *DeploymentTracker {
	now := time.Now()
	return &DeploymentTracker{
		Request: req,
		Status: DeploymentStatus{
			Namespace:      req.Target.Namespace,
			DeploymentName: req.Target.DeploymentName,
			Phase:          PhasePending,
			HealthStatus:   HealthUnknown,
			LastUpdated:    now,
		},
		Events:    make([]DeployEvent, 0),
		History:   make([]DeploymentRevision, 0),
		StartedAt: now,
	}
}

// AddEvent appends a deploy event and updates the tracker's status phase.
func (t *DeploymentTracker) AddEvent(event DeployEvent) {
	t.Events = append(t.Events, event)
	t.Status.Phase = event.Phase
	t.Status.ReadyReplicas = event.ReadyReplicas
	t.Status.DesiredReplicas = event.DesiredReplicas
	t.Status.UpdatedReplicas = event.UpdatedReplicas
	t.Status.AvailableReplicas = event.AvailableReplicas
	t.Status.CurrentRevision = event.Revision
	t.Status.LastUpdated = event.Timestamp

	if event.IsTerminal {
		t.FinishedAt = event.Timestamp
	}
}

// IsFinished returns true if the deployment has reached a terminal state.
func (t *DeploymentTracker) IsFinished() bool {
	return t.Status.Phase.IsTerminal()
}

// ElapsedTime returns how long the deployment has been running.
func (t *DeploymentTracker) ElapsedTime() time.Duration {
	if t.IsFinished() {
		return t.FinishedAt.Sub(t.StartedAt)
	}
	return time.Since(t.StartedAt)
}
