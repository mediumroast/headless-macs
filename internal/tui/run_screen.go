package tui

// run_screen.go — generic "running an ops stage" screen.
// Used by System Baseline (and future stages) to stream action results
// as they come in and display a scrollable log when done.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mediumroast/headless-macs/internal/config"
	"github.com/mediumroast/headless-macs/internal/ops"
)

// BaselineDoneMsg is sent when RunBaseline completes.
type BaselineDoneMsg struct {
	Result *ops.BaselineResult
	Err    error
}

func runBaselineCmd(cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		result, err := ops.RunBaseline(cfg, ops.BaselineOptions{})
		return BaselineDoneMsg{Result: result, Err: err}
	}
}

// StorageDoneMsg is sent when RunStorage completes.
type StorageDoneMsg struct {
	Result *ops.StorageResult
	Err    error
}

func runStorageCmd(cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		result, err := ops.RunStorage(cfg)
		return StorageDoneMsg{Result: result, Err: err}
	}
}

// ToolsDoneMsg is sent when RunTools completes.
type ToolsDoneMsg struct {
	Result *ops.ToolsResult
	Err    error
}

func runToolsCmd(cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		result, err := ops.RunTools(cfg)
		return ToolsDoneMsg{Result: result, Err: err}
	}
}

// RestoreDoneMsg is sent when RunRestore completes.
type RestoreDoneMsg struct {
	Result *ops.RestoreResult
	Err    error
}

func runRestoreCmd() tea.Cmd {
	return func() tea.Msg {
		result, err := ops.RunRestore()
		return RestoreDoneMsg{Result: result, Err: err}
	}
}

// UpdateDoneMsg is sent when RunUpdateTools completes.
type UpdateDoneMsg struct {
	Result *ops.UpdateResult
	Err    error
}

func runUpdateCmd(cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		result, err := ops.RunUpdateTools(cfg)
		return UpdateDoneMsg{Result: result, Err: err}
	}
}

// DebugToolsDoneMsg is sent when RunDebugTools completes.
type DebugToolsDoneMsg struct {
	Result *ops.DebugToolsResult
	Err    error
}

func runDebugToolsCmd(sudoNopasswdEnabled bool, username string) tea.Cmd {
	return func() tea.Msg {
		result, err := ops.RunDebugTools(sudoNopasswdEnabled, username)
		return DebugToolsDoneMsg{Result: result, Err: err}
	}
}

type runState int

const (
	runStateRunning runState = iota
	runStateDone
)

// RunScreenModel is the Bubble Tea model for an in-progress ops stage.
// It handles BaselineResult, StorageResult, ToolsResult, RestoreResult, and
// UpdateResult by normalising them via stageActions()/stageSummary().
type RunScreenModel struct {
	title            string
	state            runState
	spinner          spinner.Model
	result           *ops.BaselineResult
	storageResult    *ops.StorageResult
	toolsResult      *ops.ToolsResult
	restoreResult    *ops.RestoreResult
	updateResult     *ops.UpdateResult
	debugToolsResult *ops.DebugToolsResult
	err              error
	scroll           int
	width            int
	height           int

	// cachedRows is renderActions()'s output, rebuilt whenever a result or
	// the width changes. Scroll bounds must be checked against this, not
	// actionCount() — rows include section headers, blank separators, and
	// a second row per action with a Detail, so bounding against the
	// smaller raw action count undercounts and can strand the tail of a
	// long list off-screen (the title-bar-disappears symptom). Same root
	// cause issue #17 fixed in precheck_screen.go; this screen is a
	// separate implementation that had the identical bug and was missed
	// the first time — found live re-running Baseline after that fix.
	cachedRows []string
}

func NewRunScreen(title string) RunScreenModel {
	s := spinner.New()
	s.Spinner = spinner.Points
	s.Style = lipgloss.NewStyle().Foreground(colCyan)
	return RunScreenModel{title: title, state: runStateRunning, spinner: s}
}

func (m RunScreenModel) Init() tea.Cmd { return nil }

func (m RunScreenModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.state == runStateDone {
			m.cachedRows = m.renderActions()
		}

	case spinner.TickMsg:
		if m.state == runStateRunning {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}

	case BaselineDoneMsg:
		m.result = msg.Result
		m.err = msg.Err
		m.state = runStateDone
		m.cachedRows = m.renderActions()

	case StorageDoneMsg:
		m.storageResult = msg.Result
		m.err = msg.Err
		m.state = runStateDone
		m.cachedRows = m.renderActions()

	case ToolsDoneMsg:
		m.toolsResult = msg.Result
		m.err = msg.Err
		m.state = runStateDone
		m.cachedRows = m.renderActions()

	case RestoreDoneMsg:
		m.restoreResult = msg.Result
		m.err = msg.Err
		m.state = runStateDone
		m.cachedRows = m.renderActions()

	case UpdateDoneMsg:
		m.updateResult = msg.Result
		m.err = msg.Err
		m.state = runStateDone
		m.cachedRows = m.renderActions()

	case DebugToolsDoneMsg:
		m.debugToolsResult = msg.Result
		m.err = msg.Err
		m.state = runStateDone
		m.cachedRows = m.renderActions()

	case tea.KeyMsg:
		if m.state != runStateDone {
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

// StatusHints is the shell-level status bar's content while this screen
// is the active content pane.
func (m RunScreenModel) StatusHints() string {
	return hint("↑↓/PgUp/PgDn", "scroll") + statusGap() + hint("q", "back to menu")
}

// View reassembles a full-screen render for tea.Model conformance — not
// how the shell actually renders this screen (it calls Body()/
// StatusHints() directly).
func (m RunScreenModel) View() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render(fmt.Sprintf(" headless-macs v%s — %s ", Version, m.title)))
	b.WriteByte('\n')
	b.WriteString(styleDivider.Render(strings.Repeat("─", max(m.width, 40))))
	b.WriteByte('\n')
	b.WriteString(m.Body())
	b.WriteString(styleStatusBar.Render(m.StatusHints()))
	return b.String()
}

// Body renders the scrollable action list, using its own stored
// width/height (set via the content-pane-sized WindowSizeMsg the shell
// sends it — see app.go).
func (m RunScreenModel) Body() string {
	var b strings.Builder

	if m.state == runStateRunning {
		b.WriteString("\n  " + m.spinner.View() + styleFieldValue.Render("  Running… (requires sudo — changes are being applied)") + "\n")
		return b.String()
	}

	if m.err != nil {
		b.WriteString(styleError.Render(fmt.Sprintf("  ERROR: %v", m.err)))
		b.WriteByte('\n')
	}

	if m.stageActions() != nil {
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

		sets, skips, warns, fails, logPath := m.stageSummary()
		b.WriteString(styleDivider.Render(strings.Repeat("─", max(m.width, 40))))
		b.WriteByte('\n')
		summary := fmt.Sprintf("  %d applied  %d skipped  %d warnings  %d failures",
			sets, skips, warns, fails)
		if fails == 0 {
			b.WriteString(styleStatusSaved.Render(summary))
		} else {
			b.WriteString(styleError.Render(summary))
		}
		b.WriteByte('\n')
		if logPath != "" {
			b.WriteString(styleKeyHint.Render(fmt.Sprintf("  Log: %s", logPath)))
			b.WriteByte('\n')
		}
	}

	return b.String()
}

func (m RunScreenModel) visibleRows() int {
	v := m.height - 6 // indicator-above(1) + indicator-below(1) + divider(1) + summary(1) + log(1) + padding(1)
	if v < 1 {
		v = 1
	}
	return v
}

func (m RunScreenModel) stageSummary() (sets, skips, warns, fails int, logPath string) {
	switch {
	case m.result != nil:
		return m.result.Sets, m.result.Skips, m.result.Warnings, m.result.Failures, m.result.LogPath
	case m.storageResult != nil:
		return m.storageResult.Sets, m.storageResult.Skips, m.storageResult.Warnings, m.storageResult.Failures, m.storageResult.LogPath
	case m.toolsResult != nil:
		return m.toolsResult.Sets, m.toolsResult.Skips, m.toolsResult.Warnings, m.toolsResult.Failures, m.toolsResult.LogPath
	case m.restoreResult != nil:
		return m.restoreResult.Sets, m.restoreResult.Skips, m.restoreResult.Warnings, m.restoreResult.Failures, m.restoreResult.LogPath
	case m.updateResult != nil:
		return m.updateResult.Sets, m.updateResult.Skips, m.updateResult.Warnings, m.updateResult.Failures, m.updateResult.LogPath
	case m.debugToolsResult != nil:
		return m.debugToolsResult.Sets, m.debugToolsResult.Skips, m.debugToolsResult.Warnings, m.debugToolsResult.Failures, m.debugToolsResult.LogPath
	}
	return
}

func (m RunScreenModel) stageActions() []ops.BaselineAction {
	switch {
	case m.result != nil:
		return m.result.Actions
	case m.storageResult != nil:
		return m.storageResult.Actions
	case m.toolsResult != nil:
		return m.toolsResult.Actions
	case m.restoreResult != nil:
		return m.restoreResult.Actions
	case m.updateResult != nil:
		return m.updateResult.Actions
	case m.debugToolsResult != nil:
		return m.debugToolsResult.Actions
	}
	return nil
}

func (m RunScreenModel) renderActions() []string {
	actions := m.stageActions()
	if actions == nil {
		return nil
	}
	// MaxWidth budgeted for each line's prefix — see the identical
	// reasoning in precheck_screen.go's renderChecks().
	maxW := m.width
	if maxW <= 0 {
		maxW = 200
	}
	msgW := maxW - 9
	detailW := maxW - 9

	rows := make([]string, 0, len(actions))
	currentSection := ""

	for _, a := range actions {
		if a.Section != currentSection {
			if currentSection != "" {
				rows = append(rows, "")
			}
			rows = append(rows, "  "+styleSectionHeader.Render(a.Section))
			currentSection = a.Section
		}

		prefix, render := actionStyle(a.Status, msgW)
		rows = append(rows, styleKeyHint.Render(prefix)+render(a.Message))
		if a.Detail != "" {
			rows = append(rows, styleKeyHint.Render("         ")+styleFieldModified.MaxWidth(detailW).Render(a.Detail))
		}
	}
	return rows
}

func actionStyle(s ops.ActionStatus, maxW int) (prefix string, render func(string) string) {
	switch s {
	case ops.ActionSet:
		return "  [SET]  ", func(m string) string { return styleStatusSaved.MaxWidth(maxW).Render(m) }
	case ops.ActionSkip:
		return "  [SKIP] ", func(m string) string { return styleKeyHint.MaxWidth(maxW).Render(m) }
	case ops.ActionWarn:
		return "  [WARN] ", func(m string) string { return styleFieldModified.MaxWidth(maxW).Render(m) }
	case ops.ActionFail:
		return "  [FAIL] ", func(m string) string { return styleError.MaxWidth(maxW).Render(m) }
	default:
		return "         ", func(m string) string { return styleFieldValue.MaxWidth(maxW).Render(m) }
	}
}
