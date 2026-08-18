package tui

import "charm.land/lipgloss/v2"

// This file is the whole visual language of the interface. Change a colour, a
// mark, or a rule here and every screen follows.
//
// Every colour is a 4-bit ANSI index, so the terminal theme chooses the shade.
// This keeps the interface readable on a light and on a dark background.
// Bubble Tea downsamples the colours at its renderer and honours NO_COLOR, so
// each line is written with its style and no profile is detected here.
var (
	accentColor  = lipgloss.Cyan        // the one accent, for the selection
	healthyColor = lipgloss.Green       // a state that is in order
	warnColor    = lipgloss.Red         // a state that needs the user
	mutedColor   = lipgloss.BrightBlack // everything secondary
)

// Every style is named for the role it fills, never for its colour. Bold marks
// hierarchy only: the brand, a heading, and the selected row.
var (
	brandStyle    = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	headingStyle  = lipgloss.NewStyle().Bold(true).Foreground(mutedColor)
	itemStyle     = lipgloss.NewStyle()
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	mutedStyle    = lipgloss.NewStyle().Foreground(mutedColor)
	healthyStyle  = lipgloss.NewStyle().Foreground(healthyColor)
	warnStyle     = lipgloss.NewStyle().Foreground(warnColor)
)

// The brand of the application, which every screen carries.
const (
	brandName = "fourseas"
	brandMark = "≋"
)

// cursorMark and blankMark keep every row of a list in the same column. They
// are the same printable width, so a row never moves when the cursor does.
// blankMark is also the left gutter of every other line.
const (
	cursorMark = "› "
	blankMark  = "  "
)

// The marks of a state. Each one is one printable cell.
const (
	okMark     = "●"
	failedMark = "✕"
	noneMark   = "○"
	newMark    = "NEW"
)

// dividerRune is the faint rule that separates the key help from the screen.
const dividerRune = "─"

// The rules of the two columns. rightPad holds the right column off the edge,
// so it balances the left gutter, and minGap is the smallest space that still
// reads as two columns.
const (
	rightPad = 2
	minGap   = 2
)

// choiceStyle is how one row of a list is written.
func choiceStyle(selected bool) lipgloss.Style {
	if selected {
		return selectedStyle
	}
	return itemStyle
}

// mark is the cursor column of one list row.
func mark(selected bool) string {
	if selected {
		return selectedStyle.Render(cursorMark)
	}
	return blankMark
}
