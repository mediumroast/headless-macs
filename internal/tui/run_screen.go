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

type runState int

const (
	runStateRunning runState = iota
	runStateDone
)

// RunScreenModel is the Bubble Tea model for an in-progress ops stage.
// It handles BaselineResult, StorageResult, ToolsResult, RestoreResult, and
// UpdateResult by normalising them via stageActions()/stageSummary().
type RunScreenModel struct {
	title         string
	state         runState
	spinner       spinner.Model
	result        *ops.BaselineResult
	storageResult *ops.StorageResult
	toolsResult   *ops.ToolsResult
	restoreResult *ops.RestoreResult
	updateResult  *ops.UpdateResult
	err           error
	scroll        int
	width         int
	height        int
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

	case StorageDoneMsg:
		m.storageResult = msg.Result
		m.err = msg.Err
		m.state = runStateDone

	case ToolsDoneMsg:
		m.toolsResult = msg.Result
		m.err = msg.Err
		m.state = runStateDone

	case RestoreDoneMsg:
		m.restoreResult = msg.Result
		m.err = msg.Err
		m.state = runStateDone

	case UpdateDoneMsg:
		m.updateResult = msg.Result
		m.err = msg.Err
		m.state = runStateDone

	case tea.KeyMsg:
		if m.state != runStateDone {
			break
		}
		n := m.actionCount()
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

func (m RunScreenModel) View() string {
	var b strings.Builder

	b.WriteString(styleTitle.Render(fmt.Sprintf(" headless-macs v%s — %s ", appVersion, m.title)))
	b.WriteByte('\n')
	b.WriteString(styleDivider.Render(strings.Repeat("─", max(m.width, 40))))
	b.WriteByte('\n')

	if m.state == runStateRunning {
		b.WriteString("\n  " + m.spinner.View() + "  Running… (requires sudo — changes are being applied)\n")
		return b.String()
	}

	if m.err != nil {
		b.WriteString(styleError.Render(fmt.Sprintf("  ERROR: %v", m.err)))
		b.WriteByte('\n')
	}

	if m.stageActions() != nil {
		rows := m.renderActions()
		visible := m.visibleRows()

		// Scroll-above indicator
		if m.scroll > 0 {
			b.WriteString(styleKeyHint.Render(fmt.Sprintf("  ↑  %d more above\n", m.scroll)))
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
			b.WriteString(styleKeyHint.Render(fmt.Sprintf("  ↓  %d more below\n", remaining)))
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

	b.WriteString(styleStatusBar.Render(
		hint("↑↓/PgUp/PgDn", "scroll") + "  " + hint("q", "back to menu"),
	))
	return b.String()
}

func (m RunScreenModel) visibleRows() int {
	v := m.height - 9 // title(2) + indicator(1) + summary(3) + log(1) + status(1) + padding(1)
	if v < 1 {
		v = 1
	}
	return v
}

func (m RunScreenModel) actionCount() int {
	return len(m.stageActions())
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
	}
	return nil
}

func (m RunScreenModel) renderActions() []string {
	actions := m.stageActions()
	if actions == nil {
		return nil
	}
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

		prefix, render := actionStyle(a.Status)
		rows = append(rows, styleKeyHint.Render(prefix)+render(a.Message))
		if a.Detail != "" {
			rows = append(rows, styleKeyHint.Render("         ")+styleFieldModified.Render(a.Detail))
		}
	}
	return rows
}

func actionStyle(s ops.ActionStatus) (prefix string, render func(string) string) {
	switch s {
	case ops.ActionSet:
		return "  [SET]  ", func(m string) string { return styleStatusSaved.Render(m) }
	case ops.ActionSkip:
		return "  [SKIP] ", func(m string) string { return styleKeyHint.Render(m) }
	case ops.ActionWarn:
		return "  [WARN] ", func(m string) string { return styleFieldModified.Render(m) }
	case ops.ActionFail:
		return "  [FAIL] ", func(m string) string { return styleError.Render(m) }
	default:
		return "         ", func(m string) string { return styleFieldValue.Render(m) }
	}
}
