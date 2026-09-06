package tui

import "github.com/charmbracelet/lipgloss"

// Dark-only color palette
var (
	colBg        = lipgloss.Color("#0F1117")
	colAmber     = lipgloss.Color("#D97706")
	colGrey      = lipgloss.Color("#9CA3AF")
	colWhite     = lipgloss.Color("#F9FAFB")
	colCyan      = lipgloss.Color("#06B6D4")
	colSlate     = lipgloss.Color("#1E293B")
	colBorder    = lipgloss.Color("#374151")
	colStatusBg  = lipgloss.Color("#1F2937")
	colGreen     = lipgloss.Color("#10B981")
	colDimmed    = lipgloss.Color("#4B5563")
	colRed       = lipgloss.Color("#EF4444")
	colSubHeader = lipgloss.Color("#6B7280")
)

var (
	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colWhite).
			Background(colBg)

	styleDivider = lipgloss.NewStyle().
			Foreground(colBorder).
			Background(colBg)

	styleSectionHeader = lipgloss.NewStyle().
				Bold(true).
				Foreground(colAmber).
				Background(colBg)

	styleToolHeader = lipgloss.NewStyle().
			Foreground(colSubHeader).
			Background(colBg)

	styleFieldLabel = lipgloss.NewStyle().
			Foreground(colGrey).
			Background(colBg).
			Width(34)

	styleFieldValue = lipgloss.NewStyle().
			Foreground(colWhite).
			Background(colBg)

	styleFieldModified = lipgloss.NewStyle().
				Foreground(colCyan).
				Background(colBg)

	styleSelectedLabel = lipgloss.NewStyle().
				Background(colSlate).
				Foreground(colWhite).
				Width(34)

	styleSelectedValue = lipgloss.NewStyle().
				Background(colSlate).
				Foreground(colWhite)

	styleSelectedModified = lipgloss.NewStyle().
				Background(colSlate).
				Foreground(colCyan)

	styleCursor = lipgloss.NewStyle().
			Foreground(colCyan).
			Background(colBg).
			Bold(true)

	styleStatusBar = lipgloss.NewStyle().
			Background(colStatusBg).
			Foreground(colGrey).
			PaddingLeft(1)

	// styleStatusModified/styleStatusSaved are body-content styles (the
	// config editor's "[modified]"/"[saved]" indicator lives in its Body()
	// now, not the shared status bar — see config_editor.go — and Verify/
	// Baseline's summary lines and actionStyle()'s "[SET]" prefix are body
	// content too). colBg matches all of these consistently.
	styleStatusModified = lipgloss.NewStyle().
				Foreground(colCyan).
				Background(colBg).
				Bold(true)

	styleStatusSaved = lipgloss.NewStyle().
				Foreground(colGreen).
				Background(colBg).
				Bold(true)

	// styleKeyHint/styleKeyName are body-context (scroll indicators,
	// [SKIP]/[INFO] prefixes, "Fix:" detail lines) — colBg. hint() below
	// needs its own colStatusBg variants since it's rendered exclusively
	// inside a styleStatusBar wrap; see the styleStatusSaved comment above
	// for why these can't share one token across both contexts.
	styleKeyHint = lipgloss.NewStyle().
			Foreground(colDimmed).
			Background(colBg)

	styleKeyName = lipgloss.NewStyle().
			Foreground(colWhite).
			Background(colBg)

	styleStatusKeyHint = lipgloss.NewStyle().
				Foreground(colDimmed).
				Background(colStatusBg)

	styleStatusKeyName = lipgloss.NewStyle().
				Foreground(colWhite).
				Background(colStatusBg)

	styleMenuTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colAmber).
			Background(colBg).
			PaddingLeft(2)

	// PaddingLeft moved from these tokens into the sidebar's own row layout
	// (menu.go's Body()) in Phase 10 — the sidebar now controls left
	// spacing directly (cursor glyph + label), so a fixed style-level pad
	// would double up with it.
	styleMenuItem = lipgloss.NewStyle().
			Foreground(colGrey).
			Background(colBg)

	styleMenuItemSelected = lipgloss.NewStyle().
				Background(colSlate).
				Foreground(colWhite)

	styleMenuItemDisabled = lipgloss.NewStyle().
				Foreground(colDimmed).
				Background(colBg)

	styleError = lipgloss.NewStyle().
			Foreground(colRed).
			Background(colBg)
)

// stylePage is the outer full-screen wrap: pads every line to the
// terminal's width and fills unused vertical space, both with colBg, so
// the app's background is what styles.go says it is rather than whatever
// the terminal emulator's own theme provides. Applied once, at the very
// top of App.View() — see PHASE_10_PLAN.md, Phase 10-Paint.
func stylePage(width, height int) lipgloss.Style {
	return lipgloss.NewStyle().Background(colBg).Width(width).Height(height)
}

// hint renders one "key description" pair for a status bar line. Always
// used inside a styleStatusBar.Render(...) call — see styleStatusKeyName/
// styleStatusKeyHint's doc comment for why this can't reuse the
// general-purpose styleKeyName/styleKeyHint tokens.
func hint(key, desc string) string {
	return styleStatusKeyName.Render(key) + styleStatusKeyHint.Render(" "+desc)
}

// statusGap renders the "  " separator used between multiple hint() pairs
// on one status bar line. A bare "  " literal there would be an unstyled
// gap after hint()'s own trailing reset — confirmed by direct experiment
// that Lip Gloss does not repaint background across an inner reset except
// at a line's start or its own added padding. See PHASE_10_PLAN.md,
// Phase 10-Paint.
func statusGap() string {
	return styleStatusKeyHint.Render("  ")
}
