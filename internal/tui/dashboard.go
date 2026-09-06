package tui

// dashboard.go — the default landing content pane (Phase 10C). Shows
// daemon state + resource use, macmon's hardware telemetry when enabled,
// and the version-mismatch nudge from Phase 10G. The one content pane in
// the app that stays live via a periodic tea.Tick rather than being a
// static one-shot report.

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mediumroast/headless-macs/internal/config"
	"github.com/mediumroast/headless-macs/internal/ops"
)

const defaultDashboardRefresh = 2 * time.Second

// DashboardTickMsg fires on the refresh interval; DashboardDataMsg carries
// the result of the RunStatus() call it triggers.
type DashboardTickMsg time.Time
type DashboardDataMsg struct {
	Result *ops.StatusResult
	Err    error
}

func dashboardTickCmd(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(t time.Time) tea.Msg { return DashboardTickMsg(t) })
}

func dashboardFetchCmd(cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		r, err := ops.RunStatus(cfg)
		return DashboardDataMsg{Result: r, Err: err}
	}
}

// DashboardModel is the Dashboard content pane's Bubble Tea model.
type DashboardModel struct {
	cfg      *config.Config
	interval time.Duration
	result   *ops.StatusResult
	err      error

	versionMismatch bool
	versionMarker   *ops.VersionMarker

	width  int
	height int
}

func NewDashboard(cfg *config.Config) DashboardModel {
	interval := defaultDashboardRefresh
	if cfg != nil && cfg.TUI.DashboardRefreshMs > 0 {
		interval = time.Duration(cfg.TUI.DashboardRefreshMs) * time.Millisecond
	}
	mismatch, marker := ops.VersionMismatch()
	return DashboardModel{
		cfg:             cfg,
		interval:        interval,
		versionMismatch: mismatch,
		versionMarker:   marker,
	}
}

// Init starts the fetch/tick loop. Called once at app startup regardless
// of whether Dashboard is the initially-visible screen, so data is warm
// by the time the user switches to it.
func (m DashboardModel) Init() tea.Cmd {
	return tea.Batch(dashboardFetchCmd(m.cfg), dashboardTickCmd(m.interval))
}

func (m DashboardModel) Update(msg tea.Msg) (DashboardModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case DashboardTickMsg:
		return m, tea.Batch(dashboardFetchCmd(m.cfg), dashboardTickCmd(m.interval))
	case DashboardDataMsg:
		m.result = msg.Result
		m.err = msg.Err
	}
	return m, nil
}

// View satisfies the same "has a View" convenience the other screens keep
// for compat; the shell renders via Body() directly.
func (m DashboardModel) View() string { return m.Body() }

// StatusHints is the shell-level status bar's content while Dashboard is
// the active content pane.
func (m DashboardModel) StatusHints() string {
	return hint("↑↓", "navigate") + statusGap() + hint("enter", "select") + statusGap() + hint("q", "quit")
}

// Body renders the SERVICES table, HARDWARE block, and version-nudge line,
// using its own stored width/height (set via the content-pane-sized
// WindowSizeMsg the shell sends it — see app.go).
func (m DashboardModel) Body() string {
	var b strings.Builder

	// MaxWidth, not just Width: confirmed by direct experiment that a long
	// unwrapped line here overflows the content pane and visually breaks
	// alignment with the sidebar — this isn't hypothetical, it reproduced
	// immediately in a smoke test. See PHASE_10_PLAN.md, Phase 10-Paint.
	maxW := m.width
	if maxW <= 0 {
		maxW = 200 // no wrap constraint until the first WindowSizeMsg arrives
	}

	if m.versionMismatch {
		if m.versionMarker == nil {
			b.WriteString(styleFieldModified.MaxWidth(maxW).Render(fmt.Sprintf(
				"  This box has never had baseline/install-tools run under v%s — run them to apply current fixes.", Version)))
		} else {
			b.WriteString(styleFieldModified.MaxWidth(maxW).Render(fmt.Sprintf(
				"  Running v%s, but this box was last configured by v%s — re-run baseline/install-tools.",
				Version, m.versionMarker.Version)))
		}
		b.WriteString("\n\n")
	}

	if m.err != nil {
		b.WriteString(styleError.MaxWidth(maxW).Render(fmt.Sprintf("  ERROR: %v", m.err)))
		b.WriteByte('\n')
	}

	b.WriteString("  " + styleSectionHeader.Render("SERVICES"))
	b.WriteByte('\n')
	if m.result == nil {
		b.WriteString(styleKeyHint.Render("  loading…"))
		b.WriteByte('\n')
	} else {
		for _, d := range m.result.Daemons {
			b.WriteString(renderDaemonRow(d))
			b.WriteByte('\n')
		}
	}

	b.WriteByte('\n')
	b.WriteString("  " + styleSectionHeader.Render("HARDWARE"))
	b.WriteByte('\n')
	if m.result == nil {
		b.WriteString(styleKeyHint.Render("  loading…"))
	} else if m.result.Hardware == nil {
		b.WriteString(styleKeyHint.Render("  macmon disabled or unreachable — enable tools.macmon for CPU/GPU/temp"))
	} else {
		h := m.result.Hardware
		b.WriteString(styleFieldValue.Render(fmt.Sprintf("  cpu power  %5.1f W    cpu temp  %5.1f C", h.CPUPowerW, h.CPUTempC)))
		b.WriteByte('\n')
		b.WriteString(styleFieldValue.Render(fmt.Sprintf("  gpu power  %5.1f W    gpu temp  %5.1f C", h.GPUPowerW, h.GPUTempC)))
		b.WriteByte('\n')
		if h.RAMTotalB > 0 {
			b.WriteString(styleFieldValue.Render(fmt.Sprintf("  memory     %s / %s", formatBytesTUI(h.RAMUsageB), formatBytesTUI(h.RAMTotalB))))
		}
	}
	b.WriteByte('\n')

	return b.String()
}

func renderDaemonRow(d ops.DaemonStatus) string {
	if d.Running {
		return styleStatusSaved.Render(fmt.Sprintf("  [UP]   %-30s", d.Label)) +
			styleFieldValue.Render(fmt.Sprintf("PID %-8d %8s  %5.1f%%", d.PID, formatBytesTUI(d.RSSBytes), d.CPUPercent))
	}
	return styleKeyHint.Render(fmt.Sprintf("  [--]   %-30s disabled/not running", d.Label))
}

func formatBytesTUI(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
