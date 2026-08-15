package tui

// run_screen.go — generic "running an ops stage" screen.
// Used by System Baseline (and future stages) to stream action results
// as they come in and display a scrollable log when done.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

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

type runState int

const (
	runStateRunning runState = iota
	runStateDone
)

// RunScreenModel is the Bubble Tea model for an in-progress ops stage.
type RunScreenModel struct {
	title  string
	state  runState
	result *ops.BaselineResult
	err    error
	scroll int
	width  int
	height int
}

func NewRunScreen(title string) RunScreenModel {
	return RunScreenModel{title: title, state: runStateRunning}
}

func (m RunScreenModel) Init() tea.Cmd { return nil }

func (m RunScreenModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case BaselineDoneMsg:
		m.result = msg.Result
		m.err = msg.Err
		m.state = runStateDone

	case tea.KeyMsg:
		if m.state != runStateDone {
			break
		}
		switch msg.String() {
		case "up", "k":
			if m.scroll > 0 {
				m.scroll--
			}
		case "down", "j":
			if m.result != nil && m.scroll < len(m.result.Actions)-1 {
				m.scroll++
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
		b.WriteString("\n  Running… (requires sudo — changes are being applied)\n")
		return b.String()
	}

	if m.err != nil {
		b.WriteString(styleError.Render(fmt.Sprintf("  ERROR: %v", m.err)))
		b.WriteByte('\n')
	}

	if m.result != nil {
		rows := m.renderActions()
		visible := m.height - 5
		if visible < 1 {
			visible = 1
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

		r := m.result
		b.WriteString(styleDivider.Render(strings.Repeat("─", max(m.width, 40))))
		b.WriteByte('\n')
		summary := fmt.Sprintf("  %d applied  %d skipped  %d warnings  %d failures",
			r.Sets, r.Skips, r.Warnings, r.Failures)
		if r.Failures == 0 {
			b.WriteString(styleStatusSaved.Render(summary))
		} else {
			b.WriteString(styleError.Render(summary))
		}
		b.WriteByte('\n')
		if r.LogPath != "" {
			b.WriteString(styleKeyHint.Render(fmt.Sprintf("  Log: %s", r.LogPath)))
			b.WriteByte('\n')
		}
	}

	b.WriteString(styleStatusBar.Render(
		hint("↑↓", "scroll") + "  " + hint("q", "back to menu"),
	))
	return b.String()
}

func (m RunScreenModel) renderActions() []string {
	if m.result == nil {
		return nil
	}
	rows := make([]string, 0, len(m.result.Actions))
	currentSection := ""

	for _, a := range m.result.Actions {
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
