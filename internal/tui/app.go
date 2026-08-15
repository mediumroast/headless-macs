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
	screenPrecheck
	screenBaseline
	screenStorage
)

// App is the top-level Bubble Tea model. It owns the active screen and
// routes messages between child models.
type App struct {
	screen       screen
	configEditor ConfigEditorModel
	menu         MenuModel
	precheck     PrecheckModel
	runScreen    RunScreenModel
	cfg          *config.Config
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
		cfg:          cfg,
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
		// Forward to all children so they resize correctly when switching screens
		ce, _ := a.configEditor.Update(msg)
		a.configEditor = ce.(ConfigEditorModel)
		mn, _ := a.menu.Update(msg)
		a.menu = mn.(MenuModel)
		pc, _ := a.precheck.Update(msg)
		a.precheck = pc.(PrecheckModel)
		rs, _ := a.runScreen.Update(msg)
		a.runScreen = rs.(RunScreenModel)
		return a, nil

	case SavedMsg:
		a.cfg = msg.Cfg
		a.configEditor = NewConfigEditor(msg.Cfg)
		a.screen = screenMenu
		return a, nil

	case DiscardMsg:
		a.screen = screenMenu
		return a, nil

	case PrecheckDoneMsg:
		updated, cmd := a.precheck.Update(msg)
		a.precheck = updated.(PrecheckModel)
		return a, cmd

	case BaselineDoneMsg:
		updated, cmd := a.runScreen.Update(msg)
		a.runScreen = updated.(RunScreenModel)
		return a, cmd

	case StorageDoneMsg:
		updated, cmd := a.runScreen.Update(msg)
		a.runScreen = updated.(RunScreenModel)
		return a, cmd

	case MenuSelectMsg:
		return a.handleMenuSelect(msg.Index)

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return a, tea.Quit
		}
		a.errMsg = "" // clear any previous error on keypress
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
		case screenPrecheck:
			updated, cmd := a.precheck.Update(msg)
			a.precheck = updated.(PrecheckModel)
			return a, cmd
		case screenBaseline, screenStorage:
			updated, cmd := a.runScreen.Update(msg)
			a.runScreen = updated.(RunScreenModel)
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
	case "p":
		a.precheck = NewPrecheckModel()
		a.screen = screenPrecheck
		return a, runPrecheckCmd(a.cfg)
	case "b":
		a.runScreen = NewRunScreen("System Baseline")
		a.screen = screenBaseline
		return a, runBaselineCmd(a.cfg)
	case "t":
		a.runScreen = NewRunScreen("Storage Setup")
		a.screen = screenStorage
		return a, runStorageCmd(a.cfg)
	default:
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
	case screenPrecheck:
		content = a.precheck.View()
	case screenBaseline, screenStorage:
		content = a.runScreen.View()
	}

	if a.errMsg != "" {
		content += "\n" + styleError.Render("  ⚠  "+a.errMsg) + "\n" +
			styleKeyHint.Render("  Press any key to continue...")
	}
	return content
}
