package tui

import "github.com/charmbracelet/lipgloss"

// ── Color Palette ──────────────────────────────────────────────────────────

var (
	ColorPrimary   = lipgloss.Color("#7C3AED") // violet-600
	ColorSecondary = lipgloss.Color("#06B6D4") // cyan-500
	ColorSuccess   = lipgloss.Color("#10B981") // emerald-500
	ColorWarning   = lipgloss.Color("#F59E0B") // amber-500
	ColorDanger    = lipgloss.Color("#EF4444") // red-500
	ColorInfo      = lipgloss.Color("#3B82F6") // blue-500
	ColorMuted     = lipgloss.Color("#6B7280") // gray-500
	ColorSubtle    = lipgloss.Color("#374151") // gray-700
	ColorBorder    = lipgloss.Color("#4B5563") // gray-600
	ColorBg        = lipgloss.Color("#111827") // gray-900
	ColorBgAlt     = lipgloss.Color("#1F2937") // gray-800
	ColorText      = lipgloss.Color("#F9FAFB") // gray-50
	ColorTextDim   = lipgloss.Color("#9CA3AF") // gray-400
	ColorAccent    = lipgloss.Color("#A78BFA") // violet-400
	ColorHighlight = lipgloss.Color("#818CF8") // indigo-400
	ColorCanary    = lipgloss.Color("#FBBF24") // amber-400
	ColorRollback  = lipgloss.Color("#FB923C") // orange-400
	ColorHealthy   = lipgloss.Color("#34D399") // emerald-400
	ColorUnhealthy = lipgloss.Color("#F87171") // red-400
	ColorDegraded  = lipgloss.Color("#FBBF24") // amber-400
)

// ── Base Styles ────────────────────────────────────────────────────────────

var (
	// App chrome
	AppStyle = lipgloss.NewStyle().
			Padding(0, 1)

	// Title bar at the very top
	TitleBarStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorText).
			Background(ColorPrimary).
			Padding(0, 2).
			MarginBottom(1)

	// Status bar at the very bottom
	StatusBarStyle = lipgloss.NewStyle().
			Foreground(ColorTextDim).
			Background(ColorBgAlt).
			Padding(0, 1)

	StatusBarKeyStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorAccent).
				Background(ColorBgAlt)

	StatusBarTextStyle = lipgloss.NewStyle().
				Foreground(ColorTextDim).
				Background(ColorBgAlt)
)

// ── Tab Styles ─────────────────────────────────────────────────────────────

var (
	TabStyle = lipgloss.NewStyle().
			Foreground(ColorTextDim).
			Padding(0, 2)

	ActiveTabStyle = lipgloss.NewStyle().
			Foreground(ColorText).
			Background(ColorPrimary).
			Bold(true).
			Padding(0, 2)

	TabGapStyle = lipgloss.NewStyle().
			Foreground(ColorBorder)

	TabBarStyle = lipgloss.NewStyle().
			MarginBottom(1)
)

// ── Panel / Box Styles ─────────────────────────────────────────────────────

var (
	PanelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder).
			Padding(1, 2)

	PanelTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorAccent).
			MarginBottom(1)

	FocusedPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorPrimary).
				Padding(1, 2)
)

// ── Table Styles ───────────────────────────────────────────────────────────

var (
	TableHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorSecondary).
				BorderBottom(true).
				BorderStyle(lipgloss.NormalBorder()).
				BorderForeground(ColorSubtle).
				PaddingRight(2)

	TableRowStyle = lipgloss.NewStyle().
			Foreground(ColorText).
			PaddingRight(2)

	TableRowAltStyle = lipgloss.NewStyle().
				Foreground(ColorText).
				Background(ColorBgAlt).
				PaddingRight(2)

	TableSelectedStyle = lipgloss.NewStyle().
				Foreground(ColorText).
				Background(ColorPrimary).
				Bold(true).
				PaddingRight(2)
)

// ── Badge / Tag Styles ─────────────────────────────────────────────────────

var (
	BadgeSuccess = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#000000")).
			Background(ColorSuccess).
			Bold(true).
			Padding(0, 1)

	BadgeWarning = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#000000")).
			Background(ColorWarning).
			Bold(true).
			Padding(0, 1)

	BadgeDanger = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#000000")).
			Background(ColorDanger).
			Bold(true).
			Padding(0, 1)

	BadgeInfo = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#000000")).
			Background(ColorInfo).
			Bold(true).
			Padding(0, 1)

	BadgeMuted = lipgloss.NewStyle().
			Foreground(ColorText).
			Background(ColorSubtle).
			Padding(0, 1)

	BadgeCanary = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#000000")).
			Background(ColorCanary).
			Bold(true).
			Padding(0, 1)
)

// ── Text Styles ────────────────────────────────────────────────────────────

var (
	TextBold = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorText)

	TextDim = lipgloss.NewStyle().
		Foreground(ColorTextDim)

	TextMuted = lipgloss.NewStyle().
			Foreground(ColorMuted)

	TextSuccess = lipgloss.NewStyle().
			Foreground(ColorSuccess)

	TextWarning = lipgloss.NewStyle().
			Foreground(ColorWarning)

	TextDanger = lipgloss.NewStyle().
			Foreground(ColorDanger)

	TextInfo = lipgloss.NewStyle().
			Foreground(ColorInfo)

	TextAccent = lipgloss.NewStyle().
			Foreground(ColorAccent)

	TextHighlight = lipgloss.NewStyle().
			Foreground(ColorHighlight)
)

// ── Label/Value pair ───────────────────────────────────────────────────────

var (
	LabelStyle = lipgloss.NewStyle().
			Foreground(ColorTextDim).
			Width(18).
			Align(lipgloss.Right).
			PaddingRight(1)

	ValueStyle = lipgloss.NewStyle().
			Foreground(ColorText)
)

// ── Input / Form Styles ────────────────────────────────────────────────────

var (
	InputStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder).
			Padding(0, 1)

	InputFocusedStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorPrimary).
				Padding(0, 1)

	InputLabelStyle = lipgloss.NewStyle().
			Foreground(ColorAccent).
			Bold(true).
			MarginRight(1)

	PlaceholderStyle = lipgloss.NewStyle().
				Foreground(ColorMuted)
)

// ── Spinner / Progress ─────────────────────────────────────────────────────

var (
	SpinnerStyle = lipgloss.NewStyle().
			Foreground(ColorPrimary)

	ProgressBarFilled = lipgloss.NewStyle().
				Foreground(ColorSuccess)

	ProgressBarEmpty = lipgloss.NewStyle().
				Foreground(ColorSubtle)
)

// ── Scrollbar ──────────────────────────────────────────────────────────────

var (
	ScrollThumbStyle = lipgloss.NewStyle().
				Foreground(ColorPrimary)

	ScrollTrackStyle = lipgloss.NewStyle().
				Foreground(ColorSubtle)
)

// ── Log / Event styles ─────────────────────────────────────────────────────

var (
	LogTimestampStyle = lipgloss.NewStyle().
				Foreground(ColorMuted)

	LogMessageStyle = lipgloss.NewStyle().
			Foreground(ColorText)

	LogErrorStyle = lipgloss.NewStyle().
			Foreground(ColorDanger)

	LogSuccessStyle = lipgloss.NewStyle().
			Foreground(ColorSuccess)

	LogWarningStyle = lipgloss.NewStyle().
			Foreground(ColorWarning)
)

// ── Icons ──────────────────────────────────────────────────────────────────

const (
	IconHealthy   = "●"
	IconUnhealthy = "●"
	IconDegraded  = "●"
	IconUnknown   = "○"
	IconCheck     = "✓"
	IconCross     = "✗"
	IconWarning   = "⚠"
	IconArrowR    = "→"
	IconArrowUp   = "↑"
	IconArrowDown = "↓"
	IconDot       = "·"
	IconPipe      = "│"
	IconCorner    = "└"
	IconTee       = "├"
	IconDash      = "─"
	IconSpinner   = "◐"
	IconRocket    = "🚀"
	IconRollback  = "⏪"
	IconCanary    = "🐤"
	IconPod       = "◉"
	IconReady     = "✔"
	IconNotReady  = "✘"
)

// ── Helper Functions ───────────────────────────────────────────────────────

// HealthBadge returns a styled badge for a health status string.
func HealthBadge(status string) string {
	switch status {
	case "Healthy", "HEALTHY":
		return BadgeSuccess.Render("HEALTHY")
	case "Degraded", "DEGRADED":
		return BadgeWarning.Render("DEGRADED")
	case "Unhealthy", "UNHEALTHY":
		return BadgeDanger.Render("UNHEALTHY")
	default:
		return BadgeMuted.Render("UNKNOWN")
	}
}

// PhaseBadge returns a styled badge for a deployment phase.
func PhaseBadge(phase string) string {
	switch phase {
	case "COMPLETED":
		return BadgeSuccess.Render(" COMPLETED ")
	case "IN_PROGRESS", "IN PROGRESS":
		return BadgeInfo.Render(" IN PROGRESS ")
	case "PENDING":
		return BadgeMuted.Render(" PENDING ")
	case "HEALTH_CHECK", "HEALTH CHECK":
		return BadgeInfo.Render(" HEALTH CHECK ")
	case "PROMOTING":
		return BadgeCanary.Render(" PROMOTING ")
	case "ROLLING_BACK", "ROLLING BACK":
		return BadgeWarning.Render(" ROLLING BACK ")
	case "FAILED":
		return BadgeDanger.Render(" FAILED ")
	case "ROLLED_BACK", "ROLLED BACK":
		return BadgeWarning.Render(" ROLLED BACK ")
	default:
		return BadgeMuted.Render(" " + phase + " ")
	}
}

// StrategyBadge returns a styled badge for a deployment strategy.
func StrategyBadge(strategy string) string {
	switch strategy {
	case "rolling":
		return BadgeInfo.Render("ROLLING")
	case "canary":
		return BadgeCanary.Render("CANARY")
	case "blue-green":
		return BadgeSuccess.Render("BLUE/GREEN")
	default:
		return BadgeMuted.Render(strategy)
	}
}

// HealthIcon returns a colored icon for a health status.
func HealthIcon(status string) string {
	switch status {
	case "Healthy", "HEALTHY":
		return lipgloss.NewStyle().Foreground(ColorHealthy).Render(IconHealthy)
	case "Degraded", "DEGRADED":
		return lipgloss.NewStyle().Foreground(ColorDegraded).Render(IconDegraded)
	case "Unhealthy", "UNHEALTHY":
		return lipgloss.NewStyle().Foreground(ColorUnhealthy).Render(IconUnhealthy)
	default:
		return lipgloss.NewStyle().Foreground(ColorMuted).Render(IconUnknown)
	}
}

// ReadyIcon returns a styled ready/not-ready icon.
func ReadyIcon(ready bool) string {
	if ready {
		return lipgloss.NewStyle().Foreground(ColorHealthy).Render(IconReady)
	}
	return lipgloss.NewStyle().Foreground(ColorUnhealthy).Render(IconNotReady)
}

// KeyHelp renders a key binding hint (e.g. "tab" → "switch tab").
func KeyHelp(key, description string) string {
	return StatusBarKeyStyle.Render(key) + StatusBarTextStyle.Render(" "+description)
}

// Truncate shortens a string to maxLen, appending "…" if truncated.
func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return s[:maxLen]
	}
	return s[:maxLen-1] + "…"
}

// PadRight pads a string to the given width with spaces.
func PadRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + spaces(width-len(s))
}

func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}
