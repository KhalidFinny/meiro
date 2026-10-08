package main

import (
	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
)

// searchPage is a search bar over the filters and the results. Typing only
// edits the text: a search runs when the user presses Enter, or picks another
// filter for what they last searched.
func (a *app) searchPage(c *ui.Context) {
	ui.Column(c).Fill().Children(func() {
		ui.Column(c).Padding(0, pageGutter, 12).Gap(16).Shrink(0).Children(func() {
			ui.Row(c).Children(func() {
				result := m3.SearchBar(c, m3.SearchSpec{
					Value:       &a.search.query,
					Placeholder: "Search songs, albums, artists",
					Key:         "search",
					AutoFocus:   a.focusSearch,
					MaxWidth:    760,
				})
				if result.Submitted {
					a.runSearch(a.search.query)
				}
			})
			a.focusSearch = false
			ui.Row(c).Gap(8).Wrap().Children(func() {
				for i, kind := range searchKinds {
					if m3.Chip(c, kind.name, i == a.search.kind, "chip-"+kind.name).Clicked() && i != a.search.kind {
						a.search.kind = i
						if a.search.submitted != "" {
							a.runSearch(a.search.submitted)
						}
					}
				}
			})
		})
		if a.search.submitted == "" {
			a.searchLanding(c)
			return
		}
		a.pageList(c)
	})
}

// searchLanding is the page before the first search: the searches the user
// made before, or an invitation to make one.
func (a *app) searchLanding(c *ui.Context) {
	sc := m3.Active().Scheme
	if len(a.settings.Recent) == 0 {
		ui.Column(c).Grow(1).Center().Children(func() {
			a.message(c, m3.IconSearch, "Find your next favourite",
				"Search for songs, albums, artists and playlists, then press Enter.", "", nil)
		})
		return
	}
	ui.Scroll(c).Grow(1).Padding(8, 0, a.clearance()).Children(func() {
		m3.EmphasizedText(c, m3.TitleMedium, "Recent searches").Padding(8, pageGutter, 8).TextColor(sc.OnSurfaceVariant)
		for _, query := range a.settings.Recent {
			row := ui.ButtonBase(c.Key("recent-" + query))
			row.Height(56).Margin(0, pageGutter-8).Padding(0, 4, 0, 16).Gap(16).AlignItems(ui.Center).Radius(m3.Large).
				Cursor(ui.CursorPointer).Label(query).
				Background(m3.StateFill(ui.Transparent, sc.OnSurface, row.Hovered(), row.Pressed(), row.FocusVisible()))
			removed := false
			row.Children(func() {
				ui.Icon(c, m3.IconHistory).FontSize(24).TextColor(sc.OnSurfaceVariant)
				m3.Text(c, m3.BodyLarge, query).Grow(1).SingleLine().TextColor(sc.OnSurface)
				if m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconClose, Label: "Remove " + query, Size: m3.ExtraSmall32, Key: "forget-" + query}).Clicked() {
					removed = true
				}
			})
			switch {
			case removed:
				a.forget(query)
			case row.Clicked():
				a.search.query = query
				a.runSearch(query)
			}
		}
	})
}

// forget takes a search out of the recent ones.
func (a *app) forget(query string) {
	kept := a.settings.Recent[:0]
	for _, q := range a.settings.Recent {
		if q != query {
			kept = append(kept, q)
		}
	}
	a.settings.Recent = kept
	a.saveSettings()
}
