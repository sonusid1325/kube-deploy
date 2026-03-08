package tui

import "time"

// ── Data Types ─────────────────────────────────────────────────────────────
// These types represent the TUI-local view of Kubernetes resources.
// They are populated from the raw K8s API objects in the fetch commands
// and consumed by the render functions for display.

// DeploymentInfo holds the summarised state of a Kubernetes Deployment
// as presented in the Status tab.
type DeploymentInfo struct {
	Name              string
	Namespace         string
	Image             string
	ReadyReplicas     int32
	DesiredReplicas   int32
	UpdatedReplicas   int32
	AvailableReplicas int32
	Revision          int64
	Strategy          string
	Phase             string
	HealthStatus      string
	LastUpdated       time.Time
}

// PodInfo holds per-pod details rendered in the Status and Health tabs.
type PodInfo struct {
	Name         string
	Phase        string
	Ready        bool
	RestartCount int32
	NodeName     string
	Image        string
	StartTime    time.Time
	Message      string
}

// RevisionInfo holds a single entry in the deployment revision history.
type RevisionInfo struct {
	Revision       int64
	Image          string
	Replicas       int32
	DeployedAt     time.Time
	Strategy       string
	RollbackReason string
}

// LogEntry represents a single line in the TUI activity log.
type LogEntry struct {
	Timestamp time.Time
	Level     string // "info", "success", "warning", "error"
	Message   string
}

// ── Tab Identifiers ────────────────────────────────────────────────────────

// Tab identifies one of the top-level TUI tabs.
type Tab int

const (
	TabStatus Tab = iota
	TabHealth
	TabDeploy
	TabRollback
	TabHistory
	TabLogs
)

// tabNames maps Tab values to their display labels.
var tabNames = []string{
	"Status",
	"Health",
	"Deploy",
	"Rollback",
	"History",
	"Logs",
}

// ── Deploy Form Field Identifiers ──────────────────────────────────────────

// DeployFormField identifies a single field in the Deploy tab form.
type DeployFormField int

const (
	FieldImage DeployFormField = iota
	FieldStrategy
	FieldContainer
	FieldMaxUnavailable
	FieldMaxSurge
	FieldCanaryReplicas
	FieldDryRun
	FieldSubmit
)

// ── Confirmation Modal ─────────────────────────────────────────────────────

// ConfirmAction identifies the destructive action being confirmed.
type ConfirmAction int

const (
	ConfirmNone ConfirmAction = iota
	ConfirmDeploy
	ConfirmRollback
)
