package tui

// menu.go — the sidebar. Was the full-screen main menu before Phase 10;
// repurposed as a persistent, fixed-width left column per
// docs/planning/PHASE_10_PLAN.md, Phase 10-Shell. Same items, same
// single-key shortcuts, same MenuSelectMsg — only the render target and
// layout changed. Type name kept as MenuModel to minimize churn; think of
// it as "the sidebar model."

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type MenuItem struct {
	Label string
	Key   string // single character shortcut
	Ready bool   // false = dims item (not yet implemented)
}

// MenuSelectMsg is sent when the user picks a sidebar item.
type MenuSelectMsg struct{ Index int }

// MenuModel is the sidebar's Bubble Tea model.
type MenuModel struct {
	items  []MenuItem
	cursor int
	width  int
	height int
}

var menuItems = []MenuItem{
	{Label: "Dashboard", Key: "d", Ready: true},
	{Label: "Edit Config", Key: "c", Ready: true},
	{Label: "Precheck", Key: "p", Ready: true},
	{Label: "Storage Setup", Key: "t", Ready: true},
	{Label: "System Baseline", Key: "b", Ready: true},
	{Label: "Install Tools", Key: "i", Ready: true},
	{Label: "Verify", Key: "v", Ready: true},
	{Label: "Restore", Key: "r", Ready: true},
	{Label: "Update Tools", Key: "u", Ready: true},
	{Label: "Debugging Tools", Key: "x", Ready: true},
	{Label: "Quit", Key: "q", Ready: true},
}

// sidebarLabelWidth is the widest "[k] Label" string among menuItems —
// used to size the sidebar column so every row is padded consistently.
func sidebarLabelWidth() int {
	w := 0
	for _, it := range menuItems {
		l := len(fmt.Sprintf("[%s] %s", it.Key, it.Label))
		if l > w {
			w = l
		}
	}
	return w
}

// sidebarWidth is the full (non-compact) sidebar column width: cursor
// glyph (2) + left padding already baked into the item styles + the
// longest label + a little breathing room.
func sidebarWidth() int {
	return sidebarLabelWidth() + 6
}

// sidebarCompactWidth is the icon-only rail width used below
// narrowThreshold — see app.go.
const sidebarCompactWidth = 5

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

// View satisfies tea.Model (required for Update's return type) but is not
// how the sidebar is actually rendered — the shell calls Body() directly.
// Kept trivial.
func (m MenuModel) View() string { return m.Body() }

// currentLabel returns the currently-highlighted item's label — used by
// the shell to show "what screen am I on" without a per-screen title bar.
func (m MenuModel) currentLabel() string {
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return ""
	}
	return m.items[m.cursor].Label
}

// Body renders the sidebar column using its own stored width/height (set
// via the content-pane-sized WindowSizeMsg the shell sends it — see
// app.go). Below sidebarCompactWidth, renders an icon-only rail.
func (m MenuModel) Body() string {
	compact := m.width <= sidebarCompactWidth
	var rows []string
	for i, item := range m.items {
		cursor := "  "
		if i == m.cursor {
			cursor = styleCursor.Render("▶ ")
		}

		var label string
		if compact {
			label = fmt.Sprintf("[%s]", item.Key)
		} else {
			label = fmt.Sprintf("[%s] %s", item.Key, item.Label)
			if !item.Ready {
				label += "  (coming soon)"
			}
		}

		var line string
		switch {
		case !item.Ready:
			line = cursor + styleMenuItemDisabled.Render(padTo(label, m.width-2))
		case i == m.cursor:
			line = cursor + styleMenuItemSelected.Render(padTo(label, m.width-2))
		default:
			line = cursor + styleMenuItem.Render(padTo(label, m.width-2))
		}
		rows = append(rows, line)
	}
	for len(rows) < m.height {
		rows = append(rows, styleFieldValue.Render(strings.Repeat(" ", max(0, m.width))))
	}
	return strings.Join(rows, "\n")
}

// padTo pads s with spaces to width so the row's background fills the
// full sidebar column, not just the label's own character count — the
// item styles already carry PaddingLeft, so this only needs trailing pad.
func padTo(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
