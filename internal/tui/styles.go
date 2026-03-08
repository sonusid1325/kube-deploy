package tui

import "github.com/charmbracelet/lipgloss"

// ── Color Palette ──────────────────────────────────────────────────────────
// A refined, muted palette inspired by modern terminal UIs.
// Dark background tones with carefully chosen accent colors.

var (
	ColorPrimary   = lipgloss.Color("#5B6EE1") // soft indigo
	ColorSecondary = lipgloss.Color("#59C2CF") // muted cyan
	ColorSuccess   = lipgloss.Color("#5FCC7A") // soft green
	ColorWarning   = lipgloss.Color("#E0A458") // warm amber
	ColorDanger    = lipgloss.Color("#E06070") // muted red
	ColorInfo      = lipgloss.Color("#6A9FD9") // calm blue
	ColorMuted     = lipgloss.Color("#5C6370") // dim gray
	ColorSubtle    = lipgloss.Color("#3E4452") // dark gray
	ColorBorder    = lipgloss.Color("#4B5263") // border gray
	ColorBg        = lipgloss.Color("#1E2127") // deep background
	ColorBgAlt     = lipgloss.Color("#282C34") // alt background
	ColorText      = lipgloss.Color("#E5E9F0") // near-white
	ColorTextDim   = lipgloss.Color("#8B929E") // medium gray
	ColorAccent    = lipgloss.Color("#7C8CDB") // lighter indigo
	ColorHighlight = lipgloss.Color("#8990B3") // soft highlight
	ColorCanary    = lipgloss.Color("#D4A94F") // gold
	ColorRollback  = lipgloss.Color("#D08850") // copper
	ColorHealthy   = lipgloss.Color("#5FCC7A") // green
	ColorUnhealthy = lipgloss.Color("#E06070") // red
	ColorDegraded  = lipgloss.Color("#E0A458") // amber
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
			MarginBottom(0)

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
			MarginBottom(0)
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
			Foreground(lipgloss.Color("#1E2127")).
			Background(ColorSuccess).
			Bold(true).
			Padding(0, 1)

	BadgeWarning = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#1E2127")).
			Background(ColorWarning).
			Bold(true).
			Padding(0, 1)

	BadgeDanger = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#1E2127")).
			Background(ColorDanger).
			Bold(true).
			Padding(0, 1)

	BadgeInfo = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#1E2127")).
			Background(ColorInfo).
			Bold(true).
			Padding(0, 1)

	BadgeMuted = lipgloss.NewStyle().
			Foreground(ColorText).
			Background(ColorSubtle).
			Padding(0, 1)

	BadgeCanary = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#1E2127")).
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
// Clean Unicode glyphs only — no emoji.
// The Kubernetes helm icon (⎈) is used as the app/brand symbol.

const (
	IconKube      = "⎈"  // Kubernetes helm
	IconHealthy   = "●"  // filled circle — healthy
	IconUnhealthy = "●"  // filled circle — unhealthy (colored red)
	IconDegraded  = "●"  // filled circle — degraded (colored amber)
	IconUnknown   = "○"  // open circle
	IconCheck     = "✓"  // checkmark
	IconCross     = "✗"  // cross
	IconWarning   = "!"  // simple exclamation
	IconArrowR    = "→"  // right arrow
	IconArrowUp   = "↑"  // up arrow
	IconArrowDown = "↓"  // down arrow
	IconDot       = "·"  // middle dot
	IconPipe      = "│"  // box drawing vertical
	IconCorner    = "└"  // box drawing corner
	IconTee       = "├"  // box drawing tee
	IconDash      = "─"  // box drawing horizontal
	IconSpinner   = "◐"  // half circle
	IconDeploy    = "▲"  // triangle up — deploy
	IconRollback  = "◀"  // triangle left — rollback
	IconCanary    = "◆"  // diamond — canary
	IconPod       = "◉"  // circled dot — pod
	IconReady     = "✓"  // ready
	IconNotReady  = "✗"  // not ready
	IconSection   = "──" // section divider
)

// ── Helper Functions ───────────────────────────────────────────────────────

// HealthBadge returns a styled badge for a health status string.
func HealthBadge(status string) string {
	switch status {
	case "Healthy", "HEALTHY":
		return BadgeSuccess.Render(" HEALTHY ")
	case "Degraded", "DEGRADED":
		return BadgeWarning.Render(" DEGRADED ")
	case "Unhealthy", "UNHEALTHY":
		return BadgeDanger.Render(" UNHEALTHY ")
	default:
		return BadgeMuted.Render(" UNKNOWN ")
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
		return BadgeInfo.Render(" ROLLING ")
	case "canary":
		return BadgeCanary.Render(" CANARY ")
	case "blue-green":
		return BadgeSuccess.Render(" BLUE/GREEN ")
	default:
		return BadgeMuted.Render(" " + strategy + " ")
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

// KeyHelp renders a key binding hint (e.g. "tab" -> "switch tab").
func KeyHelp(key, description string) string {
	return StatusBarKeyStyle.Render(key) + StatusBarTextStyle.Render(" "+description)
}

// Truncate shortens a string to maxLen, appending "..." if truncated.
func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
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
