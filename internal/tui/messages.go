package tui

import (
	"time"

	"github.com/sonu/kube-deploy/pkg/models"
)

// ── Async Messages ─────────────────────────────────────────────────────────
// These message types are sent from async tea.Cmd functions back to the
// Bubble Tea Update loop. Each corresponds to a completed (or in-progress)
// background operation against the Kubernetes API.

// statusDataMsg is sent when a status fetch completes.
type statusDataMsg struct {
	deployment DeploymentInfo
	pods       []PodInfo
	conditions []string
	err        error
	fetchedAt  time.Time
}

// healthDataMsg is sent when a health check completes.
type healthDataMsg struct {
	overall   string
	ready     int
	desired   int
	pods      []PodInfo
	err       error
	fetchedAt time.Time
}

// historyDataMsg is sent when a history fetch completes.
type historyDataMsg struct {
	revisions []RevisionInfo
	err       error
}

// deployEventMsg is sent for each individual deployment progress event,
// forwarded from the engine's event channel via program.Send().
type deployEventMsg struct {
	event models.DeployEvent
}

// deployDoneMsg is sent when a deployment reaches a terminal state.
type deployDoneMsg struct {
	err error
}

// rollbackDoneMsg is sent when a rollback operation completes.
type rollbackDoneMsg struct {
	success  bool
	message  string
	revision int64
	oldImage string
	newImage string
	err      error
}

// tickMsg is sent on a periodic timer for auto-refresh.
type tickMsg time.Time

// errMsg is a generic error message.
type errMsg struct {
	err error
}

// confirmMsg is sent when the user responds to a confirmation modal.
type confirmMsg struct {
	confirmed bool
	action    string // identifies what action was being confirmed
}

// logStreamMsg is sent when a new log line is forwarded into the TUI.
type logStreamMsg struct {
	entry LogEntry
}

// windowFocusMsg is sent when the terminal regains focus (if supported).
type windowFocusMsg struct{}
