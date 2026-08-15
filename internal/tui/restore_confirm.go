package tui

// restore_confirm.go — confirmation screen shown before running RunRestore.
// Displays the list of destructive changes and waits for 'y' to confirm or
// any other key to cancel. Sends RestoreConfirmedMsg or DiscardMsg.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// RestoreConfirmedMsg is sent when the user presses 'y' on the confirm screen.
type RestoreConfirmedMsg struct{}

// RestoreConfirmModel is the Bubble Tea model for the restore confirmation screen.
type RestoreConfirmModel struct {
	width  int
	height int
}

func NewRestoreConfirmModel() RestoreConfirmModel {
	return RestoreConfirmModel{}
}

func (m RestoreConfirmModel) Init() tea.Cmd { return nil }

func (m RestoreConfirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "y", "Y":
			return m, func() tea.Msg { return RestoreConfirmedMsg{} }
		default:
			return m, func() tea.Msg { return DiscardMsg{} }
		}
	}
	return m, nil
}

func (m RestoreConfirmModel) View() string {
	var b strings.Builder

	b.WriteString(styleTitle.Render(fmt.Sprintf(" headless-macs v%s — Restore ", appVersion)))
	b.WriteByte('\n')
	b.WriteString(styleDivider.Render(strings.Repeat("─", max(m.width, 40))))
	b.WriteByte('\n')
	b.WriteByte('\n')

	b.WriteString(styleError.Render("  ⚠  This will undo all changes made by headless-macs:"))
	b.WriteByte('\n')
	b.WriteByte('\n')

	changes := []string{
		"Remove all LLM server LaunchDaemons (Ollama, Rapid-MLX, mlx-lm, Infinity, Exo)",
		"Remove infrastructure LaunchDaemons (caffeinate, sysctl-tuning, maxfiles, pmset-heal)",
		"Remove _llmserver service account and /Library/LLMServer",
		"Restore pmset to safe defaults",
		"Re-enable suppressed system services (from snapshot if available)",
		"Restore Spotlight indexing",
		"Remove sshd drop-in (/etc/ssh/sshd_config.d/100-headless.conf)",
		"Restore system defaults (AirDrop, App Nap, animations, software update, Time Machine)",
		"Remove mac-llm sysctl entries from /etc/sysctl.conf (if present)",
		"Re-enable Application Firewall",
	}

	for _, c := range changes {
		b.WriteString(styleKeyHint.Render("    • " + c))
		b.WriteByte('\n')
	}

	b.WriteByte('\n')
	b.WriteString(styleFieldModified.Render("  A reboot is recommended after restore completes."))
	b.WriteByte('\n')
	b.WriteByte('\n')
	b.WriteString(styleDivider.Render(strings.Repeat("─", max(m.width, 40))))
	b.WriteByte('\n')
	b.WriteString(styleStatusBar.Render(
		hint("y", "confirm and run restore") + "  " + hint("any other key", "cancel"),
	))

	return b.String()
}
