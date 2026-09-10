package tui

// debug_tools_prompt.go — small screen collecting the operator username
// before enabling headless-macs-debug's NOPASSWD sudo grant. Shown only
// when config.json's debug.sudo_nopasswd_enabled is true; disabling the
// grant needs no username and goes straight to the run screen. Reuses
// restore_confirm.go's confirm-screen shape with a text field in place
// of a yes/no choice — see PHASE_11_PLAN.md, Phase 11F.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// DebugToolsUsernameMsg is sent when the operator confirms a username.
type DebugToolsUsernameMsg struct{ Username string }

// DebugToolsPromptModel is the Bubble Tea model for the username-collection
// step of Install/Update Debugging Tools.
type DebugToolsPromptModel struct {
	textInput textinput.Model
	width     int
	height    int
}

func NewDebugToolsPromptModel() DebugToolsPromptModel {
	ti := textinput.New()
	ti.CharLimit = 64
	ti.Width = 32
	ti.Focus()
	return DebugToolsPromptModel{textInput: ti}
}

func (m DebugToolsPromptModel) Init() tea.Cmd { return textinput.Blink }

func (m DebugToolsPromptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			username := strings.TrimSpace(m.textInput.Value())
			return m, func() tea.Msg { return DebugToolsUsernameMsg{Username: username} }
		case "esc":
			return m, func() tea.Msg { return DiscardMsg{} }
		}
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		return m, cmd
	}
	return m, nil
}

// StatusHints is the shell-level status bar's content while this screen
// is the active content pane.
func (m DebugToolsPromptModel) StatusHints() string {
	return hint("enter", "confirm") + statusGap() + hint("esc", "cancel")
}

// View reassembles a full-screen render for tea.Model conformance — not
// how the shell actually renders this screen (it calls Body()/
// StatusHints() directly).
func (m DebugToolsPromptModel) View() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render(fmt.Sprintf(" headless-macs v%s — Debugging Tools ", Version)))
	b.WriteByte('\n')
	b.WriteString(styleDivider.Render(strings.Repeat("─", max(m.width, 40))))
	b.WriteByte('\n')
	b.WriteString(m.Body())
	b.WriteString(styleStatusBar.Render(m.StatusHints()))
	return b.String()
}

// Body renders the explanation and the username field.
func (m DebugToolsPromptModel) Body() string {
	var b strings.Builder
	b.WriteByte('\n')
	b.WriteString(styleFieldValue.Render("  debug.sudo_nopasswd_enabled is on in config."))
	b.WriteByte('\n')
	b.WriteString(styleFieldValue.Render("  This grants ONE user passwordless sudo for exactly"))
	b.WriteByte('\n')
	b.WriteString(styleFieldValue.Render("  headless-macs-debug, nothing else — checked to exist"))
	b.WriteByte('\n')
	b.WriteString(styleFieldValue.Render("  before anything is written."))
	b.WriteByte('\n')
	b.WriteByte('\n')
	b.WriteString(styleSelectedLabel.Render("  Username: ") + m.textInput.View())
	b.WriteByte('\n')
	return b.String()
}
