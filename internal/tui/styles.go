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
			Foreground(colBorder)

	styleSectionHeader = lipgloss.NewStyle().
				Bold(true).
				Foreground(colAmber)

	styleToolHeader = lipgloss.NewStyle().
			Foreground(colSubHeader)

	styleFieldLabel = lipgloss.NewStyle().
			Foreground(colGrey).
			Width(34)

	styleFieldValue = lipgloss.NewStyle().
			Foreground(colWhite)

	styleFieldModified = lipgloss.NewStyle().
				Foreground(colCyan)

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
			Bold(true)

	styleStatusBar = lipgloss.NewStyle().
			Background(colStatusBg).
			Foreground(colGrey).
			PaddingLeft(1)

	styleStatusModified = lipgloss.NewStyle().
				Foreground(colCyan).
				Bold(true)

	styleStatusSaved = lipgloss.NewStyle().
				Foreground(colGreen).
				Bold(true)

	styleKeyHint = lipgloss.NewStyle().
			Foreground(colDimmed)

	styleKeyName = lipgloss.NewStyle().
			Foreground(colWhite)

	styleMenuTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colAmber).
			PaddingLeft(2)

	styleMenuItem = lipgloss.NewStyle().
			Foreground(colGrey).
			PaddingLeft(4)

	styleMenuItemSelected = lipgloss.NewStyle().
				Background(colSlate).
				Foreground(colWhite).
				PaddingLeft(4)

	styleMenuItemDisabled = lipgloss.NewStyle().
				Foreground(colDimmed).
				PaddingLeft(4)

	styleError = lipgloss.NewStyle().
			Foreground(colRed)
)

func hint(key, desc string) string {
	return styleKeyName.Render(key) + styleKeyHint.Render(" "+desc)
}
