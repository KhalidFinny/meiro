package main

import "github.com/egoist/mygo/ui"

// icon parses a stroked 24×24 icon drawn in the color of the text, as icon
// sets draw them.
func icon(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">` + shapes + `</svg>`))
}

// solidIcon parses a filled 24×24 icon, for shapes that read better filled,
// as the transport controls do.
func solidIcon(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor">` + shapes + `</svg>`))
}

var (
	homeIcon     = icon(`<path d="M3 10.5 12 3l9 7.5"/><path d="M5.5 9.5V21h13V9.5"/>`)
	exploreIcon  = icon(`<circle cx="12" cy="12" r="9"/><path d="m15.5 8.5-2 5-5 2 2-5z"/>`)
	libraryIcon  = icon(`<path d="M4 4.5h5.5V21H4z"/><path d="M14.5 4.5H20V21h-5.5z"/>`)
	searchIcon   = icon(`<circle cx="11" cy="11" r="6.5"/><path d="m20 20-4-4"/>`)
	playIcon     = solidIcon(`<path d="M7 4.2v15.6L19.5 12z"/>`)
	pauseIcon    = solidIcon(`<path d="M6.5 4h3.6v16H6.5zM13.9 4h3.6v16h-3.6z"/>`)
	nextIcon     = solidIcon(`<path d="M6 4.2v15.6L16 12z"/><path d="M17.4 4H20v16h-2.6z"/>`)
	previousIcon = solidIcon(`<path d="M18 4.2v15.6L8 12z"/><path d="M4 4h2.6v16H4z"/>`)
	volumeIcon   = solidIcon(`<path d="M11 4.5 6 8.5H3v7h3l5 4z"/>`)
	backIcon     = icon(`<path d="M15 5l-7 7 7 7"/>`)

	// The list layouts: rows, compact rows, columns, a grid of cards, and
	// a list beside a pane.
	rowsIcon    = icon(`<path d="M4 6h16M4 12h16M4 18h16"/>`)
	compactIcon = icon(`<path d="M4 6h16M4 9.5h16M4 13h16M4 16.5h16M4 20h16"/>`)
	columnsIcon = icon(`<path d="M4 5h16v14H4z"/><path d="M9.5 5v14M15 5v14"/>`)
	gridIcon    = icon(`<path d="M4 5h7v6.5H4zM13 5h7v6.5h-7zM4 13.5h7v5.5H4zM13 13.5h7v5.5h-7z"/>`)
	splitIcon   = icon(`<path d="M4 5h16v14H4z"/><path d="M14.5 5v14"/>`)
)
