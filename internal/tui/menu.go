package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type MenuItem struct {
	Label    string
	Key      string // single character shortcut
	Ready    bool   // false = dims item (not yet implemented)
}

// MenuSelectMsg is sent when the user picks a menu item.
type MenuSelectMsg struct{ Index int }

// MenuModel is the main menu Bubble Tea model.
type MenuModel struct {
	items  []MenuItem
	cursor int
	width  int
	height int
}

var menuItems = []MenuItem{
	{Label: "Edit Config", Key: "c", Ready: true},
	{Label: "Precheck", Key: "p", Ready: false},
	{Label: "Storage Setup", Key: "t", Ready: false},
	{Label: "System Baseline", Key: "b", Ready: false},
	{Label: "Install Tools", Key: "i", Ready: false},
	{Label: "Verify", Key: "v", Ready: false},
	{Label: "Restore", Key: "r", Ready: false},
	{Label: "Update Tools", Key: "u", Ready: false},
	{Label: "Quit", Key: "q", Ready: true},
}

func NewMenu() MenuModel {
	return MenuModel{items: menuItems}
}

func (m MenuModel) Init() tea.Cmd { return nil }

func (m MenuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "enter", " ":
			idx := m.cursor
			return m, func() tea.Msg { return MenuSelectMsg{Index: idx} }
		default:
			// Single-key shortcuts
			for i, item := range m.items {
				if msg.String() == item.Key {
					return m, func() tea.Msg { return MenuSelectMsg{Index: i} }
				}
			}
		}
	}
	return m, nil
}

func (m MenuModel) View() string {
	var b strings.Builder

	b.WriteString(styleTitle.Render(fmt.Sprintf(" headless-macs v%s ", appVersion)))
	b.WriteByte('\n')
	b.WriteString(styleDivider.Render(strings.Repeat("─", max(m.width, 40))))
	b.WriteByte('\n')
	b.WriteByte('\n')

	b.WriteString(styleMenuTitle.Render("What would you like to do?"))
	b.WriteByte('\n')
	b.WriteByte('\n')

	for i, item := range m.items {
		cursor := "  "
		var line string
		if i == m.cursor {
			cursor = styleKeyName.Render("▶ ")
		}
		label := fmt.Sprintf("[%s] %s", item.Key, item.Label)
		if !item.Ready {
			label += "  (coming soon)"
			line = cursor + styleMenuItemDisabled.Render(label)
		} else if i == m.cursor {
			line = cursor + styleMenuItemSelected.Render(label)
		} else {
			line = cursor + styleMenuItem.Render(label)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}

	b.WriteByte('\n')
	b.WriteString(styleDivider.Render(strings.Repeat("─", max(m.width, 40))))
	b.WriteByte('\n')
	b.WriteString(styleStatusBar.Render(
		hint("↑↓", "navigate") + "  " + hint("enter", "select") + "  " + hint("q", "quit"),
	))

	return b.String()
}
