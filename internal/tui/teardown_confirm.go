package tui

// teardown_confirm.go — confirmation screen shown when the config editor
// detects a tool's `enabled` flag transitioned true -> false, before
// anything is written to disk or touched on the system. Reuses
// restore_confirm.go's confirm-screen pattern. See issue #13.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// teardownChoice identifies which of the three options the operator picked.
type teardownChoice int

const (
	teardownCancel teardownChoice = iota
	teardownSaveOnly
	teardownStopAndUninstall
)

// TeardownChoiceMsg is sent when the operator confirms a choice on this screen.
type TeardownChoiceMsg struct {
	Choice teardownChoice
	Tools  []string
}

// TeardownConfirmModel is the Bubble Tea model for the tool-teardown
// confirmation screen.
type TeardownConfirmModel struct {
	tools  []string
	cursor teardownChoice
	width  int
	height int
}

// NewTeardownConfirmModel creates the confirm screen for the given list of
// tools whose `enabled` flag is transitioning to false. Defaults the
// cursor to "save config only" — the least destructive of the three
// options — not "stop and uninstall," which should require a deliberate
// move to reach.
func NewTeardownConfirmModel(tools []string) TeardownConfirmModel {
	return TeardownConfirmModel{tools: tools, cursor: teardownSaveOnly}
}

func (m TeardownConfirmModel) Init() tea.Cmd { return nil }

func (m TeardownConfirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > teardownCancel {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < teardownStopAndUninstall {
				m.cursor++
			}
		case "enter":
			choice := m.cursor
			tools := m.tools
			return m, func() tea.Msg { return TeardownChoiceMsg{Choice: choice, Tools: tools} }
		case "esc", "q":
			tools := m.tools
			return m, func() tea.Msg { return TeardownChoiceMsg{Choice: teardownCancel, Tools: tools} }
		}
	}
	return m, nil
}

// StatusHints is the shell-level status bar's content while this screen
// is the active content pane.
func (m TeardownConfirmModel) StatusHints() string {
	return hint("↑↓", "select") + statusGap() + hint("enter", "confirm") + statusGap() + hint("esc", "cancel")
}

// View reassembles a full-screen render for tea.Model conformance — not
// how the shell actually renders this screen (it calls Body()/
// StatusHints() directly).
func (m TeardownConfirmModel) View() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render(fmt.Sprintf(" headless-macs v%s — Confirm ", Version)))
	b.WriteByte('\n')
	b.WriteString(styleDivider.Render(strings.Repeat("─", max(m.width, 40))))
	b.WriteByte('\n')
	b.WriteString(m.Body())
	b.WriteString(styleStatusBar.Render(m.StatusHints()))
	return b.String()
}

// Body renders the tool list and the three options.
func (m TeardownConfirmModel) Body() string {
	var b strings.Builder
	b.WriteByte('\n')

	b.WriteString(styleFieldModified.Render("  You disabled: " + strings.Join(m.tools, ", ")))
	b.WriteByte('\n')
	b.WriteByte('\n')
	b.WriteString(styleKeyHint.Render("  Disabling a tool in config doesn't stop it by itself —"))
	b.WriteByte('\n')
	b.WriteString(styleKeyHint.Render("  choose what should happen to the running daemon:"))
	b.WriteByte('\n')
	b.WriteByte('\n')

	options := []struct {
		choice teardownChoice
		label  string
	}{
		{teardownStopAndUninstall, "Stop and uninstall now (daemon + plist removed; models/data left in place)"},
		{teardownSaveOnly, "Save config only (daemon keeps running until next Install Tools or reboot)"},
		{teardownCancel, "Cancel (leave enabled as it was, discard this change)"},
	}
	for _, o := range options {
		if o.choice == m.cursor {
			b.WriteString(styleCursor.Render("  ▶ ") + styleSelectedValue.Render(o.label))
		} else {
			b.WriteString("    " + styleFieldValue.Render(o.label))
		}
		b.WriteByte('\n')
	}

	return b.String()
}
