package tui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"go.uber.org/zap"

	"github.com/sonu/kube-deploy/internal/config"
	"github.com/sonu/kube-deploy/pkg/deployer"
	"github.com/sonu/kube-deploy/pkg/health"
	"github.com/sonu/kube-deploy/pkg/k8s"
	"github.com/sonu/kube-deploy/pkg/models"
	"github.com/sonu/kube-deploy/pkg/rollback"
)

// ── Main Model ─────────────────────────────────────────────────────────────

// Model is the top-level Bubble Tea model for the kube-deploy TUI.
// It owns all state: Kubernetes clients, tab data, form inputs, and UI chrome.
type Model struct {
	// Kubernetes backend
	k8sClient *k8s.Client
	engine    *deployer.Engine
	monitor   *health.Monitor
	rollbackC *rollback.Controller
	cfg       *config.Config
	logger    *zap.Logger

	// Reference to the running tea.Program so async goroutines can
	// forward messages (e.g. deploy events) via program.Send().
	program *tea.Program

	// Target deployment
	namespace  string
	deployment string

	// Window dimensions
	width  int
	height int

	// Tab navigation
	activeTab Tab

	// Shared widgets
	spinner  spinner.Model
	viewport viewport.Model

	// Status tab data
	statusData    *statusDataMsg
	statusLoading bool

	// Health tab data
	healthData    *healthDataMsg
	healthLoading bool
	healthWatch   bool

	// Deploy tab
	deployInputs     []textinput.Model
	deployStrategy   int // 0=rolling, 1=canary
	deployDryRun     bool
	deployFocusField DeployFormField
	deploying        bool
	deployEvents     []models.DeployEvent
	deployResult     string
	deployViewport   viewport.Model

	// Rollback tab
	rollbackRevision   textinput.Model
	rollbackReason     textinput.Model
	rollbackFocusField int // 0=revision, 1=reason, 2=submit
	rollingBack        bool
	rollbackResult     string

	// History tab data
	historyData    *historyDataMsg
	historyLoading bool
	historyScroll  int

	// Logs
	logs       []LogEntry
	logsMu     sync.Mutex
	logsScroll int

	// Auto refresh
	autoRefresh   bool
	refreshTicker *time.Ticker
	lastRefresh   time.Time

	// Help overlay
	showHelp bool

	// Confirmation modal
	confirmAction ConfirmAction

	// Error state
	lastError string

	// Quitting
	quitting bool
}

// NewModel creates a new TUI model with all Kubernetes components wired up.
// The logger may be nil (a no-op logger is used). The tea.Program reference
// is set later via SetProgram() after the program is created.
func NewModel(namespace, deployment string, cfg *config.Config, logger *zap.Logger) (*Model, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	// Build kubernetes client config from app config.
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

	k8sClient, err := k8s.NewClient(k8sCfg, logger)
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes client: %w", err)
	}

	engine := deployer.NewEngine(k8sClient, cfg, logger)
	mon := health.NewMonitor(k8sClient, logger)
	rc := rollback.NewController(k8sClient, mon, logger)

	// Create spinner.
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = SpinnerStyle

	// Create deploy form inputs.
	imageInput := textinput.New()
	imageInput.Placeholder = "e.g. goserver:v2"
	imageInput.CharLimit = 256
	imageInput.Width = 40

	containerInput := textinput.New()
	containerInput.Placeholder = "(optional) container name"
	containerInput.CharLimit = 128
	containerInput.Width = 40

	maxUnavailInput := textinput.New()
	maxUnavailInput.Placeholder = "0"
	maxUnavailInput.CharLimit = 5
	maxUnavailInput.Width = 10

	maxSurgeInput := textinput.New()
	maxSurgeInput.Placeholder = "1"
	maxSurgeInput.CharLimit = 5
	maxSurgeInput.Width = 10

	canaryReplicasInput := textinput.New()
	canaryReplicasInput.Placeholder = "1"
	canaryReplicasInput.CharLimit = 5
	canaryReplicasInput.Width = 10

	deployInputs := []textinput.Model{
		imageInput,
		containerInput,
		maxUnavailInput,
		maxSurgeInput,
		canaryReplicasInput,
	}
	deployInputs[0].Focus()

	// Rollback inputs.
	revInput := textinput.New()
	revInput.Placeholder = "revision number (0 = previous)"
	revInput.CharLimit = 10
	revInput.Width = 30

	reasonInput := textinput.New()
	reasonInput.Placeholder = "reason for rollback"
	reasonInput.CharLimit = 256
	reasonInput.Width = 40

	// Deploy viewport for event log.
	dvp := viewport.New(80, 10)

	m := &Model{
		k8sClient: k8sClient,
		engine:    engine,
		monitor:   mon,
		rollbackC: rc,
		cfg:       cfg,
		logger:    logger,

		namespace:  namespace,
		deployment: deployment,

		activeTab: TabStatus,
		spinner:   sp,

		deployInputs:     deployInputs,
		deployFocusField: FieldImage,
		deployViewport:   dvp,

		rollbackRevision:   revInput,
		rollbackReason:     reasonInput,
		rollbackFocusField: 0,

		autoRefresh:   true,
		logs:          make([]LogEntry, 0, 256),
		confirmAction: ConfirmNone,
	}

	m.addLog("info", "kube-deploy TUI started - target: %s/%s", namespace, deployment)

	return m, nil
}

// SetProgram stores a reference to the running tea.Program so that
// background goroutines (deploy events, rollback progress) can send
// messages into the Bubble Tea event loop via program.Send().
func (m *Model) SetProgram(p *tea.Program) {
	m.program = p
}

// ── Tea Interface ──────────────────────────────────────────────────────────

// Init returns the initial commands: start the spinner, fetch data for all
// tabs, and kick off the auto-refresh timer.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		m.fetchStatusCmd(),
		m.fetchHealthCmd(),
		m.fetchHistoryCmd(),
		tickCmd(),
	)
}

// Update processes incoming messages and returns updated model + commands.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport = viewport.New(msg.Width-4, msg.Height-8)
		m.deployViewport = viewport.New(msg.Width-8, maxInt(msg.Height-25, 5))
		return m, nil

	case tea.KeyMsg:
		// ── Confirmation modal takes priority ──────────────────────
		if m.confirmAction != ConfirmNone {
			return m.handleConfirmKey(msg)
		}

		// ── Help overlay takes priority ────────────────────────────
		if m.showHelp {
			switch msg.String() {
			case "?", "esc", "q", "enter":
				m.showHelp = false
				return m, nil
			}
			return m, nil
		}

		// ── Global key bindings ────────────────────────────────────
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "q":
			// In text input fields, 'q' should type into the input.
			if m.isInputFocused() {
				break // fall through to tab-specific handling
			}
			m.quitting = true
			return m, tea.Quit
		case "?":
			if !m.isInputFocused() {
				m.showHelp = !m.showHelp
				return m, nil
			}
		case "tab":
			if !m.isInputFocused() || m.activeTab != TabDeploy && m.activeTab != TabRollback {
				m.activeTab = (m.activeTab + 1) % Tab(len(tabNames))
				return m, m.onTabSwitch()
			}
		case "shift+tab":
			if !m.isInputFocused() || m.activeTab != TabDeploy && m.activeTab != TabRollback {
				m.activeTab = (m.activeTab - 1 + Tab(len(tabNames))) % Tab(len(tabNames))
				return m, m.onTabSwitch()
			}
		case "1":
			if !m.isInputFocused() {
				m.activeTab = TabStatus
				return m, m.onTabSwitch()
			}
		case "2":
			if !m.isInputFocused() {
				m.activeTab = TabHealth
				return m, m.onTabSwitch()
			}
		case "3":
			if !m.isInputFocused() {
				m.activeTab = TabDeploy
				return m, m.onTabSwitch()
			}
		case "4":
			if !m.isInputFocused() {
				m.activeTab = TabRollback
				return m, m.onTabSwitch()
			}
		case "5":
			if !m.isInputFocused() {
				m.activeTab = TabHistory
				return m, m.onTabSwitch()
			}
		case "6":
			if !m.isInputFocused() {
				m.activeTab = TabLogs
				return m, m.onTabSwitch()
			}
		case "r":
			if !m.isInputFocused() {
				return m, m.refreshCurrentTab()
			}
		}

		// ── Tab-specific key handling ──────────────────────────────
		switch m.activeTab {
		case TabDeploy:
			return m.updateDeployTab(msg)
		case TabRollback:
			return m.updateRollbackTab(msg)
		case TabStatus:
			return m.updateStatusTab(msg)
		case TabHealth:
			return m.updateHealthTab(msg)
		case TabHistory:
			return m.updateHistoryTab(msg)
		case TabLogs:
			return m.updateLogsTab(msg)
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)

	case tickMsg:
		if m.autoRefresh && time.Since(m.lastRefresh) > 5*time.Second {
			m.lastRefresh = time.Now()
			switch m.activeTab {
			case TabStatus:
				cmds = append(cmds, m.fetchStatusCmd())
			case TabHealth:
				cmds = append(cmds, m.fetchHealthCmd())
			case TabHistory:
				cmds = append(cmds, m.fetchHistoryCmd())
			}
		}
		cmds = append(cmds, tickCmd())

	case statusDataMsg:
		m.statusLoading = false
		m.statusData = &msg
		if msg.err != nil {
			m.lastError = msg.err.Error()
			m.addLog("error", "status fetch failed: %v", msg.err)
		}

	case healthDataMsg:
		m.healthLoading = false
		m.healthData = &msg
		if msg.err != nil {
			m.lastError = msg.err.Error()
			m.addLog("error", "health fetch failed: %v", msg.err)
		}

	case historyDataMsg:
		m.historyLoading = false
		m.historyData = &msg
		if msg.err != nil {
			m.lastError = msg.err.Error()
			m.addLog("error", "history fetch failed: %v", msg.err)
		}

	case deployEventMsg:
		m.deployEvents = append(m.deployEvents, msg.event)
		m.addLog("info", "[deploy] %s: %s", msg.event.Phase, msg.event.Message)
		// Auto-scroll deploy viewport.
		content := m.renderDeployEvents()
		m.deployViewport.SetContent(content)
		m.deployViewport.GotoBottom()

	case deployDoneMsg:
		m.deploying = false
		if msg.err != nil {
			m.deployResult = fmt.Sprintf("%s Deployment failed: %v", IconCross, msg.err)
			m.addLog("error", "deployment failed: %v", msg.err)
		} else {
			m.deployResult = IconCheck + " Deployment completed successfully!"
			m.addLog("success", "deployment completed successfully")
		}
		// Refresh status and history after deploy.
		cmds = append(cmds, m.fetchStatusCmd(), m.fetchHistoryCmd())

	case rollbackDoneMsg:
		m.rollingBack = false
		if msg.err != nil {
			m.rollbackResult = fmt.Sprintf("%s Rollback failed: %v", IconCross, msg.err)
			m.addLog("error", "rollback failed: %v", msg.err)
		} else {
			m.rollbackResult = fmt.Sprintf("%s Rolled back to revision %d (%s -> %s)",
				IconCheck, msg.revision, msg.oldImage, msg.newImage)
			m.addLog("success", "rollback to revision %d succeeded", msg.revision)
		}
		cmds = append(cmds, m.fetchStatusCmd(), m.fetchHistoryCmd())

	case logStreamMsg:
		m.logsMu.Lock()
		m.logs = append(m.logs, msg.entry)
		if len(m.logs) > 500 {
			m.logs = m.logs[len(m.logs)-500:]
		}
		m.logsMu.Unlock()

	case errMsg:
		m.lastError = msg.err.Error()
		m.addLog("error", "%s", msg.err.Error())
	}

	return m, tea.Batch(cmds...)
}

// View renders the full TUI screen.
func (m *Model) View() string {
	if m.quitting {
		return "\n  " + IconKube + " kube-deploy exited.\n\n"
	}

	if m.width == 0 {
		return "  Initializing..."
	}

	// ── Help overlay ───────────────────────────────────────────────
	if m.showHelp {
		return m.renderHelpOverlay()
	}

	// ── Confirmation modal ─────────────────────────────────────────
	if m.confirmAction != ConfirmNone {
		return m.renderConfirmModal()
	}

	var sections []string

	// Title bar.
	title := TitleBarStyle.Width(m.width).Render(
		fmt.Sprintf(" %s kube-deploy   %s/%s", IconKube, m.namespace, m.deployment),
	)
	sections = append(sections, title)

	// Tab bar.
	sections = append(sections, m.renderTabBar())

	// Tab content.
	contentHeight := m.height - 6 // title + tabs + status bar + margins
	content := ""
	switch m.activeTab {
	case TabStatus:
		content = m.renderStatusTab()
	case TabHealth:
		content = m.renderHealthTab()
	case TabDeploy:
		content = m.renderDeployTab()
	case TabRollback:
		content = m.renderRollbackTab()
	case TabHistory:
		content = m.renderHistoryTab()
	case TabLogs:
		content = m.renderLogsTab()
	}
	content = lipgloss.NewStyle().
		Height(maxInt(contentHeight, 5)).
		MaxHeight(maxInt(contentHeight, 5)).
		Width(m.width - 2).
		Render(content)

	sections = append(sections, content)

	// Status bar.
	sections = append(sections, m.renderStatusBar())

	return AppStyle.Width(m.width).Render(
		lipgloss.JoinVertical(lipgloss.Left, sections...),
	)
}

// ── Confirmation Handling ──────────────────────────────────────────────────

// handleConfirmKey processes key presses while a confirmation modal is shown.
func (m *Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		action := m.confirmAction
		m.confirmAction = ConfirmNone
		switch action {
		case ConfirmDeploy:
			return m, m.submitDeploy()
		case ConfirmRollback:
			return m, m.submitRollback()
		}
	case "n", "N", "esc", "ctrl+c":
		m.confirmAction = ConfirmNone
		m.addLog("info", "action cancelled by user")
	}
	return m, nil
}

// ── Tab-specific Update Handlers ───────────────────────────────────────────

func (m *Model) updateStatusTab(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "r":
		return m, m.fetchStatusCmd()
	}
	return m, nil
}

func (m *Model) updateHealthTab(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "r":
		return m, m.fetchHealthCmd()
	}
	return m, nil
}

func (m *Model) updateHistoryTab(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "r":
		return m, m.fetchHistoryCmd()
	}
	return m, nil
}

func (m *Model) updateLogsTab(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	return m, nil
}

func (m *Model) updateDeployTab(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up":
		m.deployFocusPrev()
		m.updateDeployInputFocus()
		return m, nil
	case "down":
		m.deployFocusNext()
		m.updateDeployInputFocus()
		return m, nil
	case "enter":
		if m.deployFocusField == FieldSubmit {
			// Show confirmation modal instead of submitting directly.
			image := strings.TrimSpace(m.deployInputs[0].Value())
			if image == "" {
				m.deployResult = IconCross + " Image is required"
				return m, nil
			}
			m.confirmAction = ConfirmDeploy
			return m, nil
		}
		m.deployFocusNext()
		m.updateDeployInputFocus()
		return m, nil
	case "left", "right":
		if m.deployFocusField == FieldStrategy {
			m.deployStrategy = 1 - m.deployStrategy
			return m, nil
		}
		if m.deployFocusField == FieldDryRun {
			m.deployDryRun = !m.deployDryRun
			return m, nil
		}
	case " ":
		if m.deployFocusField == FieldDryRun {
			m.deployDryRun = !m.deployDryRun
			return m, nil
		}
	}

	// Forward to the focused text input.
	switch m.deployFocusField {
	case FieldImage:
		var cmd tea.Cmd
		m.deployInputs[0], cmd = m.deployInputs[0].Update(msg)
		return m, cmd
	case FieldContainer:
		var cmd tea.Cmd
		m.deployInputs[1], cmd = m.deployInputs[1].Update(msg)
		return m, cmd
	case FieldMaxUnavailable:
		var cmd tea.Cmd
		m.deployInputs[2], cmd = m.deployInputs[2].Update(msg)
		return m, cmd
	case FieldMaxSurge:
		var cmd tea.Cmd
		m.deployInputs[3], cmd = m.deployInputs[3].Update(msg)
		return m, cmd
	case FieldCanaryReplicas:
		var cmd tea.Cmd
		m.deployInputs[4], cmd = m.deployInputs[4].Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m *Model) updateRollbackTab(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up":
		if m.rollbackFocusField > 0 {
			m.rollbackFocusField--
		}
		m.updateRollbackInputFocus()
		return m, nil
	case "down":
		if m.rollbackFocusField < 2 {
			m.rollbackFocusField++
		}
		m.updateRollbackInputFocus()
		return m, nil
	case "enter":
		if m.rollbackFocusField == 2 {
			// Show confirmation modal.
			m.confirmAction = ConfirmRollback
			return m, nil
		}
		if m.rollbackFocusField < 2 {
			m.rollbackFocusField++
			m.updateRollbackInputFocus()
		}
		return m, nil
	}

	// Forward to the focused input.
	switch m.rollbackFocusField {
	case 0:
		var cmd tea.Cmd
		m.rollbackRevision, cmd = m.rollbackRevision.Update(msg)
		return m, cmd
	case 1:
		var cmd tea.Cmd
		m.rollbackReason, cmd = m.rollbackReason.Update(msg)
		return m, cmd
	}

	return m, nil
}

// ── Deploy Form Navigation ────────────────────────────────────────────────

// deployFieldOrder returns the ordered list of deploy form fields based on
// the currently selected strategy.
func (m *Model) deployFieldOrder() []DeployFormField {
	fields := []DeployFormField{FieldImage, FieldStrategy, FieldContainer}
	if m.deployStrategy == 0 {
		fields = append(fields, FieldMaxUnavailable, FieldMaxSurge)
	} else {
		fields = append(fields, FieldCanaryReplicas)
	}
	fields = append(fields, FieldDryRun, FieldSubmit)
	return fields
}

func (m *Model) deployFocusNext() {
	order := m.deployFieldOrder()
	for i, f := range order {
		if f == m.deployFocusField && i < len(order)-1 {
			m.deployFocusField = order[i+1]
			return
		}
	}
}

func (m *Model) deployFocusPrev() {
	order := m.deployFieldOrder()
	for i, f := range order {
		if f == m.deployFocusField && i > 0 {
			m.deployFocusField = order[i-1]
			return
		}
	}
}

func (m *Model) updateDeployInputFocus() {
	for i := range m.deployInputs {
		m.deployInputs[i].Blur()
	}
	switch m.deployFocusField {
	case FieldImage:
		m.deployInputs[0].Focus()
	case FieldContainer:
		m.deployInputs[1].Focus()
	case FieldMaxUnavailable:
		m.deployInputs[2].Focus()
	case FieldMaxSurge:
		m.deployInputs[3].Focus()
	case FieldCanaryReplicas:
		m.deployInputs[4].Focus()
	}
}

func (m *Model) updateRollbackInputFocus() {
	m.rollbackRevision.Blur()
	m.rollbackReason.Blur()
	switch m.rollbackFocusField {
	case 0:
		m.rollbackRevision.Focus()
	case 1:
		m.rollbackReason.Focus()
	}
}

// isInputFocused returns true if a text input is currently focused,
// meaning single-character key bindings (q, r, 1-6) should be suppressed.
func (m *Model) isInputFocused() bool {
	if m.activeTab == TabDeploy {
		switch m.deployFocusField {
		case FieldImage, FieldContainer, FieldMaxUnavailable, FieldMaxSurge, FieldCanaryReplicas:
			return true
		}
	}
	if m.activeTab == TabRollback && m.rollbackFocusField < 2 {
		return true
	}
	return false
}

// ── Logging ────────────────────────────────────────────────────────────────

// addLog appends a formatted log entry to the activity log. Thread-safe.
func (m *Model) addLog(level, format string, args ...interface{}) {
	m.logsMu.Lock()
	defer m.logsMu.Unlock()
	entry := LogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Message:   fmt.Sprintf(format, args...),
	}
	m.logs = append(m.logs, entry)
	// Cap at 500 entries.
	if len(m.logs) > 500 {
		m.logs = m.logs[len(m.logs)-500:]
	}
}
