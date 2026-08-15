package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

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

type precheckState int

const (
	precheckRunning precheckState = iota
	precheckDone
)

// PrecheckModel is the Bubble Tea model for the precheck screen.
type PrecheckModel struct {
	state   precheckState
	result  *ops.PrecheckResult
	err     error
	scroll  int
	width   int
	height  int
}

func NewPrecheckModel() PrecheckModel {
	return PrecheckModel{state: precheckRunning}
}

func (m PrecheckModel) Init() tea.Cmd { return nil }

func (m PrecheckModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case PrecheckDoneMsg:
		m.result = msg.Result
		m.err = msg.Err
		m.state = precheckDone

	case tea.KeyMsg:
		if m.state != precheckDone {
			break
		}
		switch msg.String() {
		case "up", "k":
			if m.scroll > 0 {
				m.scroll--
			}
		case "down", "j":
			if m.result != nil && m.scroll < len(m.result.Checks)-1 {
				m.scroll++
			}
		case "q", "esc":
			return m, func() tea.Msg { return DiscardMsg{} }
		}
	}
	return m, nil
}

func (m PrecheckModel) View() string {
	var b strings.Builder

	b.WriteString(styleTitle.Render(fmt.Sprintf(" headless-macs v%s — Precheck ", appVersion)))
	b.WriteByte('\n')
	b.WriteString(styleDivider.Render(strings.Repeat("─", max(m.width, 40))))
	b.WriteByte('\n')

	if m.state == precheckRunning {
		b.WriteString("\n  Running system audit… (read-only, no changes made)\n")
		return b.String()
	}

	if m.err != nil {
		b.WriteString(styleError.Render(fmt.Sprintf("  ERROR: %v", m.err)))
		b.WriteByte('\n')
	}

	if m.result != nil {
		rows := m.renderChecks()
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

		r := m.result.Readiness
		b.WriteString(styleDivider.Render(strings.Repeat("─", max(m.width, 40))))
		b.WriteByte('\n')
		summary := fmt.Sprintf("  %d blocker(s)  %d warning(s)", r.Blockers, r.Warnings)
		if r.CanProceed {
			b.WriteString(styleStatusSaved.Render(summary + "  — ready to proceed"))
		} else {
			b.WriteString(styleError.Render(summary + "  — resolve blockers before continuing"))
		}
		b.WriteByte('\n')
	}

	b.WriteString(styleStatusBar.Render(
		hint("↑↓", "scroll") + "  " + hint("q", "back to menu"),
	))
	return b.String()
}

func (m PrecheckModel) renderChecks() []string {
	if m.result == nil {
		return nil
	}
	rows := make([]string, 0, len(m.result.Checks))
	currentSection := ""

	for _, c := range m.result.Checks {
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
			valStyle = func(s string) string { return styleFieldValue.Render(s) }
		case ops.StatusWarn:
			prefix = "  [WARN] "
			valStyle = func(s string) string { return styleStatusBar.Render(s) }
		case ops.StatusBlocker:
			prefix = "  [BLOK] "
			valStyle = func(s string) string { return styleError.Render(s) }
		default:
			prefix = "         "
			valStyle = func(s string) string { return styleKeyHint.Render(s) }
		}

		rows = append(rows, styleKeyHint.Render(prefix)+valStyle(c.Message))
		if c.Detail != "" {
			rows = append(rows, styleKeyHint.Render("         Fix: ")+styleFieldModified.Render(c.Detail))
		}
	}
	return rows
}
