package tui

// app.go — the shell. Owns a persistent sidebar (always visible) and one
// active content pane, replacing the pre-Phase-10 "full-screen swap"
// navigation model. See docs/planning/PHASE_10_PLAN.md, Phase 10-Shell.
//
// Layout, top to bottom:
//   title bar (full width)
//   divider (full width)
//   sidebar | divider | content pane   (fills remaining height)
//   divider (full width)
//   status bar (full width, content pane decides what it says)
// The whole thing is wrapped in stylePage() for full-screen background
// painting (Phase 10-Paint).

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/mediumroast/headless-macs/internal/config"
)

// Version is set by main.go via NewApp — no hardcoded string here.
var Version = "dev"

// narrowThreshold: below this terminal width, the sidebar collapses to an
// icon-only rail (sidebarCompactWidth, defined in menu.go) instead of
// full labels. Resolved 2026-09-06 — see PHASE_10_PLAN.md.
const narrowThreshold = 70

type screen int

const (
	screenConfigEditor screen = iota
	screenDashboard
	screenPrecheck
	screenBaseline
	screenStorage
	screenTools
	screenVerify
	screenRestoreConfirm
	screenRestore
	screenUpdate
)

// App is the top-level Bubble Tea model. It owns the sidebar, the active
// content pane, and routes messages between them.
type App struct {
	screen         screen
	configEditor   ConfigEditorModel
	sidebar        MenuModel
	dashboard      DashboardModel
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
func NewApp(cfg *config.Config, firstRun bool) App {
	startScreen := screenDashboard
	if firstRun {
		startScreen = screenConfigEditor
	}
	return App{
		screen:       startScreen,
		configEditor: NewConfigEditor(cfg),
		sidebar:      NewMenu(),
		dashboard:    NewDashboard(cfg),
		cfg:          cfg,
	}
}

func (a App) Init() tea.Cmd {
	return tea.Batch(tea.EnterAltScreen, a.dashboard.Init())
}

// contentDims returns the content pane's width/height and the sidebar's
// width, given the full terminal size — the shared layout math both
// Update (for propagating a content-pane-sized WindowSizeMsg to children)
// and View (for actually joining the panes) need to agree on.
func (a App) contentDims() (sidebarW, contentW, paneH int) {
	if a.width <= narrowThreshold {
		sidebarW = sidebarCompactWidth
	} else {
		sidebarW = sidebarWidth()
	}
	contentW = a.width - sidebarW - 1 // 1 column for the vertical divider
	if contentW < 1 {
		contentW = 1
	}
	paneH = a.height - 4 // title(1) + top divider(1) + bottom divider(1) + status bar(1)
	if paneH < 1 {
		paneH = 1
	}
	return sidebarW, contentW, paneH
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		sidebarW, contentW, paneH := a.contentDims()

		sideMsg := tea.WindowSizeMsg{Width: sidebarW, Height: paneH}
		sm, _ := a.sidebar.Update(sideMsg)
		a.sidebar = sm.(MenuModel)

		contentMsg := tea.WindowSizeMsg{Width: contentW, Height: paneH}
		ce, _ := a.configEditor.Update(contentMsg)
		a.configEditor = ce.(ConfigEditorModel)
		db, _ := a.dashboard.Update(contentMsg)
		a.dashboard = db
		pc, _ := a.precheck.Update(contentMsg)
		a.precheck = pc.(PrecheckModel)
		rs, _ := a.runScreen.Update(contentMsg)
		a.runScreen = rs.(RunScreenModel)
		rc, _ := a.restoreConfirm.Update(contentMsg)
		a.restoreConfirm = rc.(RestoreConfirmModel)
		return a, nil

	case SavedMsg:
		a.cfg = msg.Cfg
		a.configEditor = NewConfigEditor(msg.Cfg)
		_, contentW, paneH := a.contentDims()
		ce, _ := a.configEditor.Update(tea.WindowSizeMsg{Width: contentW, Height: paneH})
		a.configEditor = ce.(ConfigEditorModel)
		a.screen = screenDashboard
		return a, nil

	case DiscardMsg:
		a.screen = screenDashboard
		return a, nil

	case DashboardTickMsg, DashboardDataMsg:
		updated, cmd := a.dashboard.Update(msg)
		a.dashboard = updated
		return a, cmd

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
		_, contentW, paneH := a.contentDims()
		a.runScreen = NewRunScreen("Restore")
		a.runScreen.width, a.runScreen.height = contentW, paneH
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
		// Dashboard has no interactive content of its own — arrow
		// keys/shortcuts there navigate the sidebar. Every other screen
		// owns its own keys (scrolling, editing, confirm) until it sends
		// DiscardMsg to return to Dashboard.
		switch a.screen {
		case screenConfigEditor:
			updated, cmd := a.configEditor.Update(msg)
			a.configEditor = updated.(ConfigEditorModel)
			return a, cmd
		case screenDashboard:
			updated, cmd := a.sidebar.Update(msg)
			a.sidebar = updated.(MenuModel)
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
	_, contentW, paneH := a.contentDims()
	switch item.Key {
	case "q":
		return a, tea.Quit
	case "d":
		a.screen = screenDashboard
		return a, nil
	case "c":
		a.screen = screenConfigEditor
		return a, nil
	case "p":
		a.precheck = NewPrecheckModel()
		a.precheck.width, a.precheck.height = contentW, paneH
		a.screen = screenPrecheck
		return a, tea.Batch(runPrecheckCmd(a.cfg), a.precheck.spinner.Tick)
	case "b":
		a.runScreen = NewRunScreen("System Baseline")
		a.runScreen.width, a.runScreen.height = contentW, paneH
		a.screen = screenBaseline
		return a, tea.Batch(runBaselineCmd(a.cfg), a.runScreen.spinner.Tick)
	case "t":
		a.runScreen = NewRunScreen("Storage Setup")
		a.runScreen.width, a.runScreen.height = contentW, paneH
		a.screen = screenStorage
		return a, tea.Batch(runStorageCmd(a.cfg), a.runScreen.spinner.Tick)
	case "i":
		a.runScreen = NewRunScreen("Install Tools")
		a.runScreen.width, a.runScreen.height = contentW, paneH
		a.screen = screenTools
		return a, tea.Batch(runToolsCmd(a.cfg), a.runScreen.spinner.Tick)
	case "v":
		a.precheck = NewVerifyModel()
		a.precheck.width, a.precheck.height = contentW, paneH
		a.screen = screenVerify
		return a, tea.Batch(runVerifyCmd(a.cfg), a.precheck.spinner.Tick)
	case "r":
		a.restoreConfirm = NewRestoreConfirmModel()
		a.restoreConfirm.width, a.restoreConfirm.height = contentW, paneH
		a.screen = screenRestoreConfirm
		return a, nil
	case "u":
		a.runScreen = NewRunScreen("Update Tools")
		a.runScreen.width, a.runScreen.height = contentW, paneH
		a.screen = screenUpdate
		return a, tea.Batch(runUpdateCmd(a.cfg), a.runScreen.spinner.Tick)
	default:
		a.errMsg = fmt.Sprintf("%s is not yet implemented (coming in a future phase).", item.Label)
		return a, nil
	}
}

// activeBody and activeStatusHints return the current content pane's
// rendered body and status-bar text.
func (a App) activeBody() string {
	switch a.screen {
	case screenConfigEditor:
		return a.configEditor.Body()
	case screenDashboard:
		return a.dashboard.Body()
	case screenPrecheck, screenVerify:
		return a.precheck.Body()
	case screenBaseline, screenStorage, screenTools, screenRestore, screenUpdate:
		return a.runScreen.Body()
	case screenRestoreConfirm:
		return a.restoreConfirm.Body()
	}
	return ""
}

func (a App) activeStatusHints() string {
	switch a.screen {
	case screenConfigEditor:
		return a.configEditor.StatusHints()
	case screenDashboard:
		return a.dashboard.StatusHints()
	case screenPrecheck, screenVerify:
		return a.precheck.StatusHints()
	case screenBaseline, screenStorage, screenTools, screenRestore, screenUpdate:
		return a.runScreen.StatusHints()
	case screenRestoreConfirm:
		return a.restoreConfirm.StatusHints()
	}
	return ""
}

// screenName is shown in the title bar in place of the old per-screen
// title — resolved 2026-09-06: one shell-level title bar, no per-pane
// header repeating the screen name, but the title bar still names what's
// active.
func (a App) screenName() string {
	switch a.screen {
	case screenConfigEditor:
		return "Configuration Editor"
	case screenDashboard:
		return "Dashboard"
	case screenPrecheck:
		return "Precheck"
	case screenVerify:
		return "Verify"
	case screenBaseline:
		return "System Baseline"
	case screenStorage:
		return "Storage Setup"
	case screenTools:
		return "Install Tools"
	case screenRestoreConfirm, screenRestore:
		return "Restore"
	case screenUpdate:
		return "Update Tools"
	}
	return a.sidebar.currentLabel()
}

func (a App) View() string {
	if a.width == 0 {
		return "loading..."
	}
	sidebarW, contentW, paneH := a.contentDims()

	var b strings.Builder
	b.WriteString(styleTitle.Render(fmt.Sprintf(" headless-macs v%s — %s ", Version, a.screenName())))
	b.WriteByte('\n')
	b.WriteString(styleDivider.Render(strings.Repeat("─", max(a.width, 40))))
	b.WriteByte('\n')

	sideBody := a.sidebar.Body()
	content := a.activeBody()

	sideLines := strings.Split(sideBody, "\n")
	contentLines := strings.Split(content, "\n")
	for i := 0; i < paneH; i++ {
		var left, right string
		if i < len(sideLines) {
			left = sideLines[i]
		}
		if i < len(contentLines) {
			right = contentLines[i]
		}
		b.WriteString(left)
		b.WriteString(styleDivider.Render("│"))
		b.WriteString(padVisible(right, contentW))
		b.WriteByte('\n')
	}

	b.WriteString(styleDivider.Render(strings.Repeat("─", max(a.width, 40))))
	b.WriteByte('\n')
	b.WriteString(styleStatusBar.Width(a.width).Render(a.activeStatusHints()))

	if a.errMsg != "" {
		b.WriteByte('\n')
		b.WriteString(styleError.Render("  ⚠  "+a.errMsg) + "\n" +
			styleKeyHint.Render("  Press any key to continue..."))
	}

	_ = sidebarW // used above via a.sidebar's own stored width
	return stylePage(a.width, a.height).Render(b.String())
}

// padVisible pads s with trailing spaces so its background fills the
// content column even where the rendered line is shorter than the pane's
// width — cheap approximation using rune count rather than true ANSI-aware
// display width, which is good enough for this project's ASCII+box-drawing
// content but would need revisiting if wide Unicode is ever used here.
func padVisible(s string, width int) string {
	visible := visibleLen(s)
	if visible >= width {
		return s
	}
	return s + styleFieldValue.Render(strings.Repeat(" ", width-visible))
}

// visibleLen approximates a styled string's on-screen width by stripping
// ANSI escape sequences and counting runes.
func visibleLen(s string) int {
	n := 0
	inEscape := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		n++
	}
	return n
}
