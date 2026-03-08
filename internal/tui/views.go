package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ── Tab Bar ────────────────────────────────────────────────────────────────

// renderTabBar renders the horizontal tab navigation bar with numbered tabs.
func (m *Model) renderTabBar() string {
	var tabs []string
	for i, name := range tabNames {
		label := fmt.Sprintf(" %d %s ", i+1, name)
		if Tab(i) == m.activeTab {
			tabs = append(tabs, ActiveTabStyle.Render(label))
		} else {
			tabs = append(tabs, TabStyle.Render(label))
		}
	}
	row := lipgloss.JoinHorizontal(lipgloss.Top, tabs...)

	// Draw a subtle separator line under the tabs
	separator := lipgloss.NewStyle().
		Foreground(ColorBorder).
		Render(strings.Repeat(IconDash, m.width-2))

	return TabBarStyle.Render(row) + "\n" + separator
}

// ── Status Bar ─────────────────────────────────────────────────────────────

// renderStatusBar renders the bottom status bar with deployment info and
// keyboard hints.
func (m *Model) renderStatusBar() string {
	separator := lipgloss.NewStyle().
		Foreground(ColorBorder).
		Render(strings.Repeat(IconDash, m.width-2))

	left := ""
	if m.lastError != "" {
		left = lipgloss.NewStyle().Foreground(ColorDanger).Background(ColorBgAlt).
			Render(" " + IconCross + " " + Truncate(m.lastError, m.width/2))
	} else if m.statusData != nil && m.statusData.err == nil {
		left = StatusBarTextStyle.Render(fmt.Sprintf(" %s %s  %s  rev:%d",
			IconKube,
			m.statusData.deployment.Phase,
			m.statusData.deployment.Image,
			m.statusData.deployment.Revision,
		))
	}

	right := KeyHelp("tab", "switch") + "  " +
		KeyHelp("r", "refresh") + "  " +
		KeyHelp("?", "help") + "  " +
		KeyHelp("q", "quit")

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 0 {
		gap = 0
	}
	mid := StatusBarStyle.Render(spaces(gap))

	bar := lipgloss.JoinHorizontal(lipgloss.Top,
		StatusBarStyle.Render(left), mid, StatusBarStyle.Render(right),
	)

	return separator + "\n" + bar
}

// ── Help Overlay ───────────────────────────────────────────────────────────

// renderHelpOverlay renders a centered help panel showing all keybindings.
func (m *Model) renderHelpOverlay() string {
	title := PanelTitleStyle.Render(IconKube + "  Keyboard Shortcuts")

	sections := []struct {
		heading string
		keys    [][]string
	}{
		{
			heading: "Navigation",
			keys: [][]string{
				{"tab / shift+tab", "Switch between tabs"},
				{"1-6", "Jump to tab by number"},
				{"r", "Refresh current tab data"},
				{"?", "Toggle this help screen"},
				{"q / ctrl+c", "Quit kube-deploy"},
			},
		},
		{
			heading: "Deploy / Rollback Forms",
			keys: [][]string{
				{"up / down", "Move between form fields"},
				{"enter", "Next field / submit"},
				{"left / right", "Toggle strategy / dry-run"},
				{"space", "Toggle checkbox"},
			},
		},
		{
			heading: "History & Logs",
			keys: [][]string{
				{"up / down", "Scroll entries"},
				{"r", "Refresh data"},
			},
		},
		{
			heading: "Confirmation Dialogs",
			keys: [][]string{
				{"y / enter", "Confirm action"},
				{"n / esc", "Cancel action"},
			},
		},
	}

	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n\n")

	for _, s := range sections {
		b.WriteString(TextAccent.Render("  "+s.heading) + "\n")
		divider := TextMuted.Render("  " + strings.Repeat(string(IconDash), 40))
		b.WriteString(divider + "\n")
		for _, kv := range s.keys {
			key := StatusBarKeyStyle.Render(PadRight(kv[0], 22))
			desc := TextDim.Render(kv[1])
			b.WriteString(fmt.Sprintf("    %s %s\n", key, desc))
		}
		b.WriteString("\n")
	}

	b.WriteString(TextDim.Render("  Press ? or esc to close"))

	panelWidth := 58
	if m.width > 0 && panelWidth > m.width-4 {
		panelWidth = m.width - 4
	}

	panel := FocusedPanelStyle.Width(panelWidth).Render(b.String())

	// Center horizontally and vertically.
	hPad := (m.width - lipgloss.Width(panel)) / 2
	if hPad < 0 {
		hPad = 0
	}
	vPad := (m.height - lipgloss.Height(panel)) / 3
	if vPad < 0 {
		vPad = 0
	}

	return strings.Repeat("\n", vPad) +
		lipgloss.NewStyle().PaddingLeft(hPad).Render(panel)
}

// ── Confirmation Modal ─────────────────────────────────────────────────────

// renderConfirmModal renders a centered confirmation dialog for destructive
// actions like deploy and rollback.
func (m *Model) renderConfirmModal() string {
	var title, body string
	switch m.confirmAction {
	case ConfirmDeploy:
		image := strings.TrimSpace(m.deployInputs[0].Value())
		strategies := []string{"rolling", "canary"}
		strategy := strategies[m.deployStrategy]
		dryLabel := ""
		if m.deployDryRun {
			dryLabel = " (DRY RUN)"
		}
		title = TextWarning.Render(IconDeploy + "  Confirm Deployment")
		body = fmt.Sprintf(
			"  Deploy %s to %s/%s\n  Strategy: %s%s\n\n  %s",
			TextBold.Render(image),
			m.namespace, m.deployment,
			StrategyBadge(strategy),
			TextDim.Render(dryLabel),
			TextDim.Render("y/enter = confirm    n/esc = cancel"),
		)
	case ConfirmRollback:
		revStr := strings.TrimSpace(m.rollbackRevision.Value())
		if revStr == "" || revStr == "0" {
			revStr = "previous"
		}
		title = TextWarning.Render(IconRollback + "  Confirm Rollback")
		body = fmt.Sprintf(
			"  Rollback %s/%s to revision %s\n\n  %s",
			m.namespace, m.deployment,
			TextBold.Render(revStr),
			TextDim.Render("y/enter = confirm    n/esc = cancel"),
		)
	default:
		return ""
	}

	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n\n")
	b.WriteString(body)

	panelWidth := 55
	if m.width > 0 && panelWidth > m.width-4 {
		panelWidth = m.width - 4
	}

	panel := FocusedPanelStyle.
		BorderForeground(ColorWarning).
		Width(panelWidth).
		Render(b.String())

	hPad := (m.width - lipgloss.Width(panel)) / 2
	if hPad < 0 {
		hPad = 0
	}
	vPad := (m.height - lipgloss.Height(panel)) / 3
	if vPad < 0 {
		vPad = 0
	}

	return strings.Repeat("\n", vPad) +
		lipgloss.NewStyle().PaddingLeft(hPad).Render(panel)
}

// ── Status Tab ─────────────────────────────────────────────────────────────

// renderStatusTab renders the deployment status overview including pod table.
func (m *Model) renderStatusTab() string {
	if m.statusLoading && m.statusData == nil {
		return m.spinner.View() + " Fetching deployment status..."
	}
	if m.statusData == nil {
		return TextDim.Render("  No status data yet. Press r to refresh.")
	}
	if m.statusData.err != nil {
		return TextDanger.Render(fmt.Sprintf("  %s Error: %v", IconCross, m.statusData.err))
	}

	d := m.statusData.deployment
	var b strings.Builder

	// Section header
	b.WriteString(sectionHeader("Deployment Overview"))
	b.WriteString("\n")

	rows := [][]string{
		{"Name", d.Name},
		{"Namespace", d.Namespace},
		{"Image", d.Image},
		{"Strategy", d.Strategy},
		{"Phase", ""},
		{"Replicas", fmt.Sprintf("%d/%d ready   %d updated   %d available",
			d.ReadyReplicas, d.DesiredReplicas, d.UpdatedReplicas, d.AvailableReplicas)},
		{"Revision", fmt.Sprintf("%d", d.Revision)},
		{"Health", ""},
	}

	for _, row := range rows {
		label := LabelStyle.Render(row[0])
		var val string
		switch row[0] {
		case "Phase":
			val = PhaseBadge(d.Phase)
		case "Health":
			val = HealthBadge(d.HealthStatus)
		default:
			val = ValueStyle.Render(row[1])
		}
		b.WriteString(fmt.Sprintf("  %s %s\n", label, val))
	}

	if !d.LastUpdated.IsZero() {
		b.WriteString(fmt.Sprintf("  %s %s\n",
			LabelStyle.Render("Last Updated"),
			TextDim.Render(d.LastUpdated.Local().Format("2006-01-02 15:04:05")),
		))
	}

	// Conditions.
	if len(m.statusData.conditions) > 0 {
		b.WriteString("\n")
		b.WriteString(sectionHeader("Conditions"))
		b.WriteString("\n")
		for _, c := range m.statusData.conditions {
			b.WriteString(fmt.Sprintf("    %s %s\n", TextDim.Render(IconDot), c))
		}
	}

	// Pods table.
	if len(m.statusData.pods) > 0 {
		b.WriteString("\n")
		b.WriteString(sectionHeader("Pods"))
		b.WriteString("\n")
		b.WriteString(m.renderPodTable(m.statusData.pods))
	}

	return PanelStyle.Width(m.width - 6).Render(b.String())
}

// renderPodTable renders a tabular view of pods with columns for name,
// status, readiness, restart count, and image.
func (m *Model) renderPodTable(pods []PodInfo) string {
	var b strings.Builder

	nameW, phaseW, imgW := 42, 12, 35
	header := fmt.Sprintf("    %s %s %s %s %s",
		TableHeaderStyle.Render(PadRight("NAME", nameW)),
		TableHeaderStyle.Render(PadRight("STATUS", phaseW)),
		TableHeaderStyle.Render(PadRight("READY", 7)),
		TableHeaderStyle.Render(PadRight("RESTARTS", 10)),
		TableHeaderStyle.Render(PadRight("IMAGE", imgW)),
	)
	b.WriteString(header + "\n")

	for i, pod := range pods {
		style := TableRowStyle
		if i%2 == 1 {
			style = TableRowAltStyle
		}

		ready := ReadyIcon(pod.Ready)
		img := Truncate(pod.Image, imgW)

		row := fmt.Sprintf("    %s %s %s %s %s",
			style.Render(PadRight(Truncate(pod.Name, nameW), nameW)),
			style.Render(PadRight(pod.Phase, phaseW)),
			ready+spaces(5),
			style.Render(PadRight(fmt.Sprintf("%d", pod.RestartCount), 10)),
			style.Render(PadRight(img, imgW)),
		)
		b.WriteString(row + "\n")

		if pod.Message != "" {
			b.WriteString(fmt.Sprintf("      %s %s\n",
				TextDim.Render(IconCorner+IconDash),
				TextWarning.Render(pod.Message),
			))
		}
	}

	return b.String()
}

// ── Health Tab ─────────────────────────────────────────────────────────────

// renderHealthTab renders the health overview with pod-level health icons.
func (m *Model) renderHealthTab() string {
	if m.healthLoading && m.healthData == nil {
		return m.spinner.View() + " Checking health..."
	}
	if m.healthData == nil {
		return TextDim.Render("  No health data yet. Press r to refresh.")
	}
	if m.healthData.err != nil {
		return TextDanger.Render(fmt.Sprintf("  %s Error: %v", IconCross, m.healthData.err))
	}

	h := m.healthData
	var b strings.Builder

	b.WriteString(sectionHeader("Health Overview"))
	b.WriteString("\n")

	b.WriteString(fmt.Sprintf("  %s %s\n", LabelStyle.Render("Overall"), HealthBadge(h.overall)))
	b.WriteString(fmt.Sprintf("  %s %s\n", LabelStyle.Render("Ready Pods"),
		ValueStyle.Render(fmt.Sprintf("%d / %d", h.ready, h.desired))))

	// Progress bar.
	if h.desired > 0 {
		b.WriteString(fmt.Sprintf("  %s %s\n", LabelStyle.Render("Progress"),
			renderProgressBar(h.ready, h.desired, 30)))
	}

	if !h.fetchedAt.IsZero() {
		b.WriteString(fmt.Sprintf("  %s %s\n", LabelStyle.Render("Checked At"),
			TextDim.Render(h.fetchedAt.Local().Format("15:04:05"))))
	}

	if len(h.pods) > 0 {
		b.WriteString("\n")
		b.WriteString(sectionHeader("Pod Health"))
		b.WriteString("\n")

		for _, pod := range h.pods {
			icon := HealthIcon("Healthy")
			if !pod.Ready {
				icon = HealthIcon("Unhealthy")
			}
			line := fmt.Sprintf("    %s %s  restarts=%-4d %s",
				icon,
				PadRight(Truncate(pod.Name, 40), 40),
				pod.RestartCount,
				TextDim.Render(pod.Image),
			)
			b.WriteString(line + "\n")
			if pod.Message != "" {
				b.WriteString(fmt.Sprintf("      %s %s\n",
					TextDim.Render(IconCorner+IconDash),
					TextWarning.Render(pod.Message),
				))
			}
		}
	}

	return PanelStyle.Width(m.width - 6).Render(b.String())
}

// renderProgressBar renders a simple text-based progress bar.
func renderProgressBar(current, total, width int) string {
	if total <= 0 {
		return ""
	}
	filled := (current * width) / total
	if filled > width {
		filled = width
	}
	empty := width - filled

	bar := ProgressBarFilled.Render(strings.Repeat("█", filled)) +
		ProgressBarEmpty.Render(strings.Repeat("░", empty))
	pct := fmt.Sprintf(" %d%%", (current*100)/total)
	return bar + TextDim.Render(pct)
}

// ── Deploy Tab ─────────────────────────────────────────────────────────────

// renderDeployTab renders the deployment form with strategy selection,
// inputs, and the event log viewport.
func (m *Model) renderDeployTab() string {
	var b strings.Builder

	b.WriteString(sectionHeader(IconDeploy + " New Deployment"))
	b.WriteString("\n")

	strategies := []string{"rolling", "canary"}
	strategyDisplay := strategies[m.deployStrategy]

	type formField struct {
		label  string
		field  DeployFormField
		render string
	}

	fields := []formField{
		{"Image", FieldImage, m.deployInputs[0].View()},
		{"Strategy", FieldStrategy, m.renderStrategySelector(strategyDisplay)},
		{"Container", FieldContainer, m.deployInputs[1].View()},
	}

	if m.deployStrategy == 0 { // rolling
		fields = append(fields,
			formField{"Max Unavailable", FieldMaxUnavailable, m.deployInputs[2].View()},
			formField{"Max Surge", FieldMaxSurge, m.deployInputs[3].View()},
		)
	} else { // canary
		fields = append(fields,
			formField{"Canary Replicas", FieldCanaryReplicas, m.deployInputs[4].View()},
		)
	}

	fields = append(fields,
		formField{"Dry Run", FieldDryRun, m.renderCheckbox(m.deployDryRun)},
	)

	for _, f := range fields {
		cursor := "  "
		if f.field == m.deployFocusField {
			cursor = TextAccent.Render(IconArrowR + " ")
		}
		label := LabelStyle.Render(f.label)
		b.WriteString(fmt.Sprintf("  %s%s %s\n", cursor, label, f.render))
	}

	// Submit button.
	b.WriteString("\n")
	submitStyle := BadgeMuted
	submitLabel := " Deploy "
	if m.deployDryRun {
		submitLabel = " Dry Run "
	}
	if m.deployFocusField == FieldSubmit {
		submitStyle = BadgeSuccess
	}
	cursor := "  "
	if m.deployFocusField == FieldSubmit {
		cursor = TextAccent.Render(IconArrowR + " ")
	}
	b.WriteString(fmt.Sprintf("  %s%s  %s\n",
		cursor,
		spaces(18),
		submitStyle.Render(submitLabel),
	))

	// Deploy in progress / result.
	if m.deploying {
		b.WriteString(fmt.Sprintf("\n  %s Deploying...\n", m.spinner.View()))
	}
	if m.deployResult != "" {
		style := TextSuccess
		if strings.HasPrefix(m.deployResult, IconCross) {
			style = TextDanger
		}
		b.WriteString(fmt.Sprintf("\n  %s\n", style.Render(m.deployResult)))
	}

	// Deploy event log.
	if len(m.deployEvents) > 0 {
		b.WriteString("\n")
		b.WriteString(sectionHeader("Events"))
		b.WriteString("\n")
		b.WriteString(m.deployViewport.View())
	}

	return PanelStyle.Width(m.width - 6).Render(b.String())
}

// renderStrategySelector renders the strategy toggle (rolling / canary)
// with a hint when focused.
func (m *Model) renderStrategySelector(current string) string {
	options := []string{"rolling", "canary"}
	var parts []string
	for _, opt := range options {
		if opt == current {
			parts = append(parts, BadgeInfo.Render(" "+opt+" "))
		} else {
			parts = append(parts, BadgeMuted.Render(" "+opt+" "))
		}
	}
	hint := ""
	if m.deployFocusField == FieldStrategy {
		hint = TextDim.Render("  left/right to switch")
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, append(parts, hint)...)
}

// renderCheckbox renders a simple on/off checkbox.
func (m *Model) renderCheckbox(checked bool) string {
	if checked {
		return TextSuccess.Render("["+IconCheck+"]") + TextDim.Render(" enabled")
	}
	return TextDim.Render("[ ]") + TextDim.Render(" disabled")
}

// renderDeployEvents renders the event log lines for the deploy viewport.
func (m *Model) renderDeployEvents() string {
	var b strings.Builder
	for _, e := range m.deployEvents {
		ts := e.Timestamp.Local().Format("15:04:05")
		phase := PhaseBadge(string(e.Phase))
		replicaInfo := ""
		if e.DesiredReplicas > 0 {
			replicaInfo = TextDim.Render(fmt.Sprintf(" [%d/%d]", e.ReadyReplicas, e.DesiredReplicas))
		}
		b.WriteString(fmt.Sprintf("  %s %s %s%s\n",
			LogTimestampStyle.Render(ts), phase, e.Message, replicaInfo))
	}
	return b.String()
}

// ── Rollback Tab ───────────────────────────────────────────────────────────

// renderRollbackTab renders the rollback form with revision input, reason,
// and a summary of available revisions.
func (m *Model) renderRollbackTab() string {
	var b strings.Builder

	b.WriteString(sectionHeader(IconRollback + " Manual Rollback"))
	b.WriteString("\n")

	type formField struct {
		label string
		idx   int
		view  string
	}

	fields := []formField{
		{"Revision", 0, m.rollbackRevision.View()},
		{"Reason", 1, m.rollbackReason.View()},
	}

	for _, f := range fields {
		cursor := "  "
		if f.idx == m.rollbackFocusField {
			cursor = TextAccent.Render(IconArrowR + " ")
		}
		label := LabelStyle.Render(f.label)
		b.WriteString(fmt.Sprintf("  %s%s %s\n", cursor, label, f.view))
	}

	b.WriteString("\n")
	submitStyle := BadgeMuted
	if m.rollbackFocusField == 2 {
		submitStyle = BadgeWarning
	}
	cursor := "  "
	if m.rollbackFocusField == 2 {
		cursor = TextAccent.Render(IconArrowR + " ")
	}
	b.WriteString(fmt.Sprintf("  %s%s  %s\n",
		cursor,
		spaces(18),
		submitStyle.Render(" Rollback "),
	))

	if m.rollingBack {
		b.WriteString(fmt.Sprintf("\n  %s Rolling back...\n", m.spinner.View()))
	}
	if m.rollbackResult != "" {
		style := TextSuccess
		if strings.HasPrefix(m.rollbackResult, IconCross) {
			style = TextDanger
		}
		b.WriteString(fmt.Sprintf("\n  %s\n", style.Render(m.rollbackResult)))
	}

	// Show recent history for reference.
	if m.historyData != nil && m.historyData.err == nil && len(m.historyData.revisions) > 0 {
		b.WriteString("\n")
		b.WriteString(sectionHeader("Available Revisions"))
		b.WriteString("\n")
		b.WriteString(m.renderHistoryTable(minInt(5, len(m.historyData.revisions))))
	}

	return PanelStyle.Width(m.width - 6).Render(b.String())
}

// ── History Tab ────────────────────────────────────────────────────────────

// renderHistoryTab renders the full deployment revision history.
func (m *Model) renderHistoryTab() string {
	if m.historyLoading && m.historyData == nil {
		return m.spinner.View() + " Fetching history..."
	}
	if m.historyData == nil {
		return TextDim.Render("  No history data yet. Press r to refresh.")
	}
	if m.historyData.err != nil {
		return TextDanger.Render(fmt.Sprintf("  %s Error: %v", IconCross, m.historyData.err))
	}

	var b strings.Builder
	b.WriteString(sectionHeader("Deployment History"))
	b.WriteString("\n")

	if len(m.historyData.revisions) == 0 {
		b.WriteString(TextDim.Render("  No revision history found."))
	} else {
		total := len(m.historyData.revisions)
		b.WriteString(TextDim.Render(fmt.Sprintf("  %d revision(s)\n\n", total)))
		b.WriteString(m.renderHistoryTable(total))
	}

	return PanelStyle.Width(m.width - 6).Render(b.String())
}

// renderHistoryTable renders a table of revision history entries up to limit.
func (m *Model) renderHistoryTable(limit int) string {
	if m.historyData == nil || len(m.historyData.revisions) == 0 {
		return ""
	}

	var b strings.Builder

	revW, imgW, repW, dateW := 10, 40, 10, 22
	header := fmt.Sprintf("    %s %s %s %s %s",
		TableHeaderStyle.Render(PadRight("REVISION", revW)),
		TableHeaderStyle.Render(PadRight("IMAGE", imgW)),
		TableHeaderStyle.Render(PadRight("REPLICAS", repW)),
		TableHeaderStyle.Render(PadRight("DEPLOYED AT", dateW)),
		TableHeaderStyle.Render("NOTES"),
	)
	b.WriteString(header + "\n")

	revisions := m.historyData.revisions
	if limit < len(revisions) {
		revisions = revisions[:limit]
	}

	for i, rev := range revisions {
		style := TableRowStyle
		if i%2 == 1 {
			style = TableRowAltStyle
		}

		deployedAt := ""
		if !rev.DeployedAt.IsZero() {
			deployedAt = rev.DeployedAt.Local().Format("2006-01-02 15:04:05")
		}

		notes := ""
		if rev.RollbackReason != "" {
			notes = TextWarning.Render("ROLLBACK: " + rev.RollbackReason)
		}

		row := fmt.Sprintf("    %s %s %s %s %s",
			style.Render(PadRight(fmt.Sprintf("%d", rev.Revision), revW)),
			style.Render(PadRight(Truncate(rev.Image, imgW), imgW)),
			style.Render(PadRight(fmt.Sprintf("%d", rev.Replicas), repW)),
			style.Render(PadRight(deployedAt, dateW)),
			notes,
		)
		b.WriteString(row + "\n")
	}

	return b.String()
}

// ── Logs Tab ───────────────────────────────────────────────────────────────

// renderLogsTab renders the activity log with timestamped, colored entries.
func (m *Model) renderLogsTab() string {
	m.logsMu.Lock()
	logs := make([]LogEntry, len(m.logs))
	copy(logs, m.logs)
	m.logsMu.Unlock()

	var b strings.Builder
	b.WriteString(sectionHeader("Activity Log"))
	b.WriteString("\n")

	if len(logs) == 0 {
		b.WriteString(TextDim.Render("  No activity yet."))
		return PanelStyle.Width(m.width - 6).Render(b.String())
	}

	// Show last N entries that fit in the viewport.
	maxEntries := maxInt(m.height-12, 10)
	start := 0
	if len(logs) > maxEntries {
		start = len(logs) - maxEntries
	}

	for _, entry := range logs[start:] {
		ts := LogTimestampStyle.Render(entry.Timestamp.Local().Format("15:04:05"))
		var lvl string
		var msgStyle lipgloss.Style
		switch entry.Level {
		case "success":
			lvl = TextSuccess.Render(" " + IconCheck + " ")
			msgStyle = LogSuccessStyle
		case "warning":
			lvl = TextWarning.Render(" " + IconWarning + " ")
			msgStyle = LogWarningStyle
		case "error":
			lvl = TextDanger.Render(" " + IconCross + " ")
			msgStyle = LogErrorStyle
		default:
			lvl = TextInfo.Render(" " + IconDot + " ")
			msgStyle = LogMessageStyle
		}
		b.WriteString(fmt.Sprintf("  %s%s%s\n", ts, lvl, msgStyle.Render(entry.Message)))
	}

	// Show entry count.
	if len(logs) > maxEntries {
		b.WriteString(fmt.Sprintf("\n  %s",
			TextDim.Render(fmt.Sprintf("  ... and %d more entries (showing last %d)", len(logs)-maxEntries, maxEntries))))
	}

	return PanelStyle.Width(m.width - 6).Render(b.String())
}

// ── Section Header Helper ──────────────────────────────────────────────────

// sectionHeader renders a consistent section title with a subtle divider.
func sectionHeader(title string) string {
	styled := PanelTitleStyle.Render("  " + title)
	return styled
}
