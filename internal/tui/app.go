package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/spinner"
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
	screenTools
	screenVerify
	screenRestoreConfirm
	screenRestore
	screenUpdate
)

// App is the top-level Bubble Tea model. It owns the active screen and
// routes messages between child models.
type App struct {
	screen         screen
	configEditor   ConfigEditorModel
	menu           MenuModel
	precheck       PrecheckModel
	runScreen      RunScreenModel
	restoreConfirm RestoreConfirmModel
	cfg            *config.Config
	width          int
	height         int
	errMsg         string
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
		rc, _ := a.restoreConfirm.Update(msg)
		a.restoreConfirm = rc.(RestoreConfirmModel)
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

	case VerifyDoneMsg:
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

	case ToolsDoneMsg:
		updated, cmd := a.runScreen.Update(msg)
		a.runScreen = updated.(RunScreenModel)
		return a, cmd

	case RestoreConfirmedMsg:
		a.runScreen = NewRunScreen("Restore")
		a.screen = screenRestore
		return a, tea.Batch(runRestoreCmd(), a.runScreen.spinner.Tick)

	case RestoreDoneMsg:
		updated, cmd := a.runScreen.Update(msg)
		a.runScreen = updated.(RunScreenModel)
		return a, cmd

	case UpdateDoneMsg:
		updated, cmd := a.runScreen.Update(msg)
		a.runScreen = updated.(RunScreenModel)
		return a, cmd

	case spinner.TickMsg:
		switch a.screen {
		case screenBaseline, screenStorage, screenTools, screenRestore, screenUpdate:
			if a.runScreen.state == runStateRunning {
				updated, cmd := a.runScreen.Update(msg)
				a.runScreen = updated.(RunScreenModel)
				return a, cmd
			}
		case screenPrecheck, screenVerify:
			if a.precheck.state == precheckRunning {
				updated, cmd := a.precheck.Update(msg)
				a.precheck = updated.(PrecheckModel)
				return a, cmd
			}
		}

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
		case screenPrecheck, screenVerify:
			updated, cmd := a.precheck.Update(msg)
			a.precheck = updated.(PrecheckModel)
			return a, cmd
		case screenBaseline, screenStorage, screenTools, screenRestore, screenUpdate:
			updated, cmd := a.runScreen.Update(msg)
			a.runScreen = updated.(RunScreenModel)
			return a, cmd
		case screenRestoreConfirm:
			updated, cmd := a.restoreConfirm.Update(msg)
			a.restoreConfirm = updated.(RestoreConfirmModel)
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
		return a, tea.Batch(runPrecheckCmd(a.cfg), a.precheck.spinner.Tick)
	case "b":
		a.runScreen = NewRunScreen("System Baseline")
		a.screen = screenBaseline
		return a, tea.Batch(runBaselineCmd(a.cfg), a.runScreen.spinner.Tick)
	case "t":
		a.runScreen = NewRunScreen("Storage Setup")
		a.screen = screenStorage
		return a, tea.Batch(runStorageCmd(a.cfg), a.runScreen.spinner.Tick)
	case "i":
		a.runScreen = NewRunScreen("Install Tools")
		a.screen = screenTools
		return a, tea.Batch(runToolsCmd(a.cfg), a.runScreen.spinner.Tick)
	case "v":
		a.precheck = NewVerifyModel()
		a.screen = screenVerify
		return a, tea.Batch(runVerifyCmd(a.cfg), a.precheck.spinner.Tick)
	case "r":
		a.restoreConfirm = NewRestoreConfirmModel()
		a.screen = screenRestoreConfirm
		return a, nil
	case "u":
		a.runScreen = NewRunScreen("Update Tools")
		a.screen = screenUpdate
		return a, tea.Batch(runUpdateCmd(a.cfg), a.runScreen.spinner.Tick)
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
	case screenPrecheck, screenVerify:
		content = a.precheck.View()
	case screenBaseline, screenStorage, screenTools, screenRestore, screenUpdate:
		content = a.runScreen.View()
	case screenRestoreConfirm:
		content = a.restoreConfirm.View()
	}

	if a.errMsg != "" {
		content += "\n" + styleError.Render("  ⚠  "+a.errMsg) + "\n" +
			styleKeyHint.Render("  Press any key to continue...")
	}
	return content
}
