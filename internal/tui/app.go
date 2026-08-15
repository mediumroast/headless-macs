package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mediumroast/headless-macs/internal/config"
)

const appVersion = "2.0.0-dev"

type screen int

const (
	screenConfigEditor screen = iota
	screenMenu
)

// App is the top-level Bubble Tea model. It owns the active screen and
// routes messages between child models.
type App struct {
	screen       screen
	configEditor ConfigEditorModel
	menu         MenuModel
	width        int
	height       int
	errMsg       string
}

// NewApp creates the App. cfg is the loaded config (or nil on first run,
// in which case Bootstrap should already have created the file before NewApp
// is called).
func NewApp(cfg *config.Config) App {
	return App{
		screen:       screenConfigEditor,
		configEditor: NewConfigEditor(cfg),
		menu:         NewMenu(),
	}
}

func (a App) Init() tea.Cmd {
	return tea.EnterAltScreen
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		// Forward to active child
		switch a.screen {
		case screenConfigEditor:
			updated, cmd := a.configEditor.Update(msg)
			a.configEditor = updated.(ConfigEditorModel)
			return a, cmd
		case screenMenu:
			updated, cmd := a.menu.Update(msg)
			a.menu = updated.(MenuModel)
			return a, cmd
		}

	case SavedMsg:
		// Config was saved — rebuild config editor with fresh state, go to menu
		a.configEditor = NewConfigEditor(msg.Cfg)
		a.screen = screenMenu
		return a, nil

	case DiscardMsg:
		// User cancelled config editor — go to menu
		a.screen = screenMenu
		return a, nil

	case MenuSelectMsg:
		return a.handleMenuSelect(msg.Index)

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return a, tea.Quit
		}
		// Delegate to active screen
		switch a.screen {
		case screenConfigEditor:
			updated, cmd := a.configEditor.Update(msg)
			a.configEditor = updated.(ConfigEditorModel)
			return a, cmd
		case screenMenu:
			updated, cmd := a.menu.Update(msg)
			a.menu = updated.(MenuModel)
			return a, cmd
		}
	}

	return a, nil
}

func (a App) handleMenuSelect(idx int) (tea.Model, tea.Cmd) {
	item := menuItems[idx]
	switch item.Key {
	case "q":
		return a, tea.Quit
	case "c":
		a.screen = screenConfigEditor
		return a, nil
	default:
		// Not yet implemented — show a brief message and stay on menu
		a.errMsg = fmt.Sprintf("%s is not yet implemented (coming in a future phase).", item.Label)
		return a, nil
	}
}

func (a App) View() string {
	var content string
	switch a.screen {
	case screenConfigEditor:
		content = a.configEditor.View()
	case screenMenu:
		content = a.menu.View()
	}

	if a.errMsg != "" {
		content += "\n" + styleError.Render("  ⚠  "+a.errMsg) + "\n" +
			styleKeyHint.Render("  Press any key to continue...")
	}
	return content
}
