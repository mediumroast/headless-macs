package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mediumroast/headless-macs/internal/config"
	"github.com/mediumroast/headless-macs/internal/ops"
)

// PrecheckDoneMsg is sent when the precheck goroutine completes.
type PrecheckDoneMsg struct {
	Result *ops.PrecheckResult
	Err    error
}

// runPrecheckCmd runs the precheck in a goroutine and sends PrecheckDoneMsg.
func runPrecheckCmd(cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		result, err := ops.RunPrecheck(cfg)
		return PrecheckDoneMsg{Result: result, Err: err}
	}
}

// VerifyDoneMsg is sent when RunVerify completes.
type VerifyDoneMsg struct {
	Result *ops.VerifyResult
	Err    error
}

// runVerifyCmd runs the verify health check in a goroutine.
func runVerifyCmd(cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		result, err := ops.RunVerify(cfg)
		return VerifyDoneMsg{Result: result, Err: err}
	}
}

type precheckState int

const (
	precheckRunning precheckState = iota
	precheckDone
)

// PrecheckModel is the Bubble Tea model for the precheck and verify screens.
// The title field controls which stage name appears in the header.
type PrecheckModel struct {
	title        string
	state        precheckState
	spinner      spinner.Model
	result       *ops.PrecheckResult
	verifyResult *ops.VerifyResult
	err          error
	scroll       int
	width        int
	height       int

	// cachedRows is renderChecks()'s output, rebuilt only when the result
	// changes (PrecheckDoneMsg/VerifyDoneMsg) rather than recomputed ad
	// hoc. This is also what m.scroll must be bounded against — it has
	// more entries than the raw check count (section headers, blank
	// separators, and a second row per check with a Detail), and
	// bounding scroll against the smaller raw count instead let the
	// "N more above" indicator undercount and could make the tail of a
	// long list
	// unreachable. See issue #17.
	cachedRows []string
}

func newPrecheckSpinner() spinner.Model {
	s := spinner.New()
	s.Spinner = spinner.Points
	s.Style = lipgloss.NewStyle().Foreground(colCyan)
	return s
}

func NewPrecheckModel() PrecheckModel {
	return PrecheckModel{title: "Precheck", state: precheckRunning, spinner: newPrecheckSpinner()}
}

func NewVerifyModel() PrecheckModel {
	return PrecheckModel{title: "Verify", state: precheckRunning, spinner: newPrecheckSpinner()}
}

func (m PrecheckModel) Init() tea.Cmd { return nil }

func (m PrecheckModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// renderChecks() truncates message/detail text to m.width, so a
		// resize after the result is already loaded must rebuild the
		// cache too, not just leave it truncated to the old width.
		if m.state == precheckDone {
			m.cachedRows = m.renderChecks()
		}

	case spinner.TickMsg:
		if m.state == precheckRunning {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}

	case PrecheckDoneMsg:
		m.result = msg.Result
		m.err = msg.Err
		m.state = precheckDone
		m.cachedRows = m.renderChecks()

	case VerifyDoneMsg:
		m.verifyResult = msg.Result
		m.err = msg.Err
		m.state = precheckDone
		m.cachedRows = m.renderChecks()

	case tea.KeyMsg:
		if m.state != precheckDone {
			break
		}
		n := len(m.cachedRows)
		visible := m.visibleRows()
		switch msg.String() {
		case "up", "k":
			if m.scroll > 0 {
				m.scroll--
			}
		case "down", "j":
			if m.scroll < n-1 {
				m.scroll++
			}
		case "pgup", "ctrl+u":
			m.scroll -= visible / 2
			if m.scroll < 0 {
				m.scroll = 0
			}
		case "pgdn", "ctrl+d":
			m.scroll += visible / 2
			if m.scroll > n-1 {
				m.scroll = n - 1
			}
			if m.scroll < 0 {
				m.scroll = 0
			}
		case "q", "esc":
			return m, func() tea.Msg { return DiscardMsg{} }
		}
	}
	return m, nil
}

func (m PrecheckModel) visibleRows() int {
	v := m.height - 6 // indicator-above(1) + indicator-below(1) + divider(1) + summary(1) + log(1) + padding(1)
	if v < 1 {
		v = 1
	}
	return v
}

// StatusHints is the shell-level status bar's content while this screen
// is the active content pane.
func (m PrecheckModel) StatusHints() string {
	return hint("↑↓/PgUp/PgDn", "scroll") + statusGap() + hint("q", "back to menu")
}

// View reassembles a full-screen render (title + Body + status bar) for
// tea.Model conformance — not how the shell actually renders this screen
// (it calls Body()/StatusHints() directly), but kept correct so this type
// still stands alone if ever used outside the shell.
func (m PrecheckModel) View() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render(fmt.Sprintf(" headless-macs v%s — %s ", Version, m.title)))
	b.WriteByte('\n')
	b.WriteString(styleDivider.Render(strings.Repeat("─", max(m.width, 40))))
	b.WriteByte('\n')
	b.WriteString(m.Body())
	b.WriteString(styleStatusBar.Render(m.StatusHints()))
	return b.String()
}

// Body renders the scrollable check list, using its own stored
// width/height (set via the content-pane-sized WindowSizeMsg the shell
// sends it — see app.go).
func (m PrecheckModel) Body() string {
	var b strings.Builder

	if m.state == precheckRunning {
		running := "Running system audit… (read-only, no changes made)"
		if m.title == "Verify" {
			running = "Running health check… (read-only, requires sudo)"
		}
		b.WriteString("\n  " + m.spinner.View() + styleFieldValue.Render("  "+running) + "\n")
		return b.String()
	}

	if m.err != nil {
		b.WriteString(styleError.Render(fmt.Sprintf("  ERROR: %v", m.err)))
		b.WriteByte('\n')
	}

	if m.result != nil || m.verifyResult != nil {
		rows := m.cachedRows
		visible := m.visibleRows()

		// Scroll-above indicator
		if m.scroll > 0 {
			b.WriteString(styleKeyHint.Render(fmt.Sprintf("  ↑  %d more above", m.scroll)))
			b.WriteByte('\n')
		} else {
			b.WriteByte('\n')
		}

		end := m.scroll + visible
		if end > len(rows) {
			end = len(rows)
		}
		start := m.scroll
		if start > end {
			start = end
		}
		for _, row := range rows[start:end] {
			b.WriteString(row)
			b.WriteByte('\n')
		}
		for i := end - start; i < visible; i++ {
			b.WriteByte('\n')
		}

		// Scroll-below indicator
		remaining := len(rows) - end
		if remaining > 0 {
			b.WriteString(styleKeyHint.Render(fmt.Sprintf("  ↓  %d more below", remaining)))
			b.WriteByte('\n')
		} else {
			b.WriteByte('\n')
		}

		b.WriteString(styleDivider.Render(strings.Repeat("─", max(m.width, 40))))
		b.WriteByte('\n')
		if m.result != nil {
			r := m.result.Readiness
			summary := fmt.Sprintf("  %d blocker(s)  %d warning(s)", r.Blockers, r.Warnings)
			if r.CanProceed {
				b.WriteString(styleStatusSaved.Render(summary + "  — ready to proceed"))
			} else {
				b.WriteString(styleError.Render(summary + "  — resolve blockers before continuing"))
			}
			b.WriteByte('\n')
		} else if m.verifyResult != nil {
			v := m.verifyResult
			summary := fmt.Sprintf("  %d passed  %d warning(s)  %d failure(s)", v.Passes, v.Warnings, v.Failures)
			if v.Failures == 0 {
				b.WriteString(styleStatusSaved.Render(summary))
			} else {
				b.WriteString(styleError.Render(summary))
			}
			b.WriteByte('\n')
			if v.LogPath != "" {
				b.WriteString(styleKeyHint.Render(fmt.Sprintf("  Log: %s", v.LogPath)))
				b.WriteByte('\n')
			}
		}
	}

	return b.String()
}

func (m PrecheckModel) checks() []ops.CheckItem {
	if m.result != nil {
		return m.result.Checks
	}
	if m.verifyResult != nil {
		return m.verifyResult.Checks
	}
	return nil
}

func (m PrecheckModel) renderChecks() []string {
	items := m.checks()
	if items == nil {
		return nil
	}
	// MaxWidth on the message/detail text specifically (budgeted for their
	// prefix's width) — confirmed by direct experiment (see dashboard.go)
	// that an unconstrained long line, like the long ssh fix commands this
	// project's own Verify output produces, overflows the content pane and
	// breaks alignment with the sidebar. maxW<=0 (no WindowSizeMsg yet)
	// falls back to a generous width rather than truncating to nothing.
	maxW := m.width
	if maxW <= 0 {
		maxW = 200
	}
	msgW := maxW - 9     // "  [PASS] " etc.
	detailW := maxW - 14 // "         Fix: "

	rows := make([]string, 0, len(items))
	currentSection := ""

	for _, c := range items {
		if c.Section != currentSection {
			if currentSection != "" {
				rows = append(rows, "")
			}
			rows = append(rows, "  "+styleSectionHeader.Render(c.Section))
			currentSection = c.Section
		}

		var prefix string
		var valStyle func(string) string

		switch c.Status {
		case ops.StatusOK:
			prefix = "  [PASS] "
			valStyle = func(s string) string { return styleFieldValue.MaxWidth(msgW).Render(s) }
		case ops.StatusWarn:
			prefix = "  [WARN] "
			valStyle = func(s string) string { return styleFieldModified.MaxWidth(msgW).Render(s) }
		case ops.StatusBlocker:
			if m.title == "Verify" {
				prefix = "  [FAIL] "
			} else {
				prefix = "  [BLOK] "
			}
			valStyle = func(s string) string { return styleError.MaxWidth(msgW).Render(s) }
		default:
			prefix = "  [INFO] "
			valStyle = func(s string) string { return styleKeyHint.MaxWidth(msgW).Render(s) }
		}

		rows = append(rows, styleKeyHint.Render(prefix)+valStyle(c.Message))
		if c.Detail != "" {
			rows = append(rows, styleKeyHint.Render("         Fix: ")+styleFieldModified.MaxWidth(detailW).Render(c.Detail))
		}
	}
	return rows
}
