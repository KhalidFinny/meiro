package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// How the list page draws its rows. It is the same page whichever is
// chosen: what a row holds, what a click or a double click does, and the
// keyboard stay the same.
const (
	layoutRows = iota
	layoutCompact
	layoutColumns
	layoutGrid
	layoutSplit
)

var (
	layoutNames = []string{"Rows", "Compact", "Columns", "Grid", "Split"}
	layoutIcons = []*ui.SVG{rowsIcon, compactIcon, columnsIcon, gridIcon, splitIcon}
)

// cardMin is the narrowest a grid card gets, and cardGap the room between
// cards: the grid fits as many cards as the room allows, sharing the rest.
const (
	cardMin = 152
	cardGap = 16
)

// listView shows the page's rows in the layout the user chose. A row is
// chosen by a click or the arrow keys, and opened by a double click or
// Enter.
func (a *app) listView(c *ui.Context) {
	if a.selected >= len(a.rows) {
		a.selected = -1
	}
	// The identity, the name and the headings are the same for every
	// layout, so they are set once here.
	a.list.Key = func(i int) any { return a.rows[i].key() }
	a.list.Label = func(i int) string { return a.rows[i].title }
	a.list.Header = func(i int) bool { return a.rows[i].header }
	a.list.Selected = &a.selected
	switch a.layout {
	case layoutCompact:
		a.compactList(c)
	case layoutColumns:
		a.columnsList(c)
	case layoutGrid:
		a.gridList(c)
	case layoutSplit:
		a.splitList(c)
	default:
		a.rowsList(c)
	}
}

// layoutPicker chooses how the list draws its rows. It is icons only, so
// it stays out of the heading's way, and named for assistive technology.
func (a *app) layoutPicker(c *ui.Context) {
	t := c.Theme()
	segments := ui.SegmentedBase(c, &a.layout, len(layoutNames))
	segments.Track.AlignItems(ui.Stretch).Padding(2).Gap(2).
		Border(1, t.Border).Label("List layout").Shrink(0)
	segments.Track.Children(func() {
		for i, icon := range layoutIcons {
			segment := segments.Segment(i).Padding(4, 7).Label(layoutNames[i])
			if i == a.layout {
				segment.Background(t.SurfaceHover)
			}
			segment.Children(func() {
				ui.Icon(c, icon).FontSize(14)
			})
		}
	})
}

// playButton is the filled button that starts playback, with the icon and
// the label the interface uses.
func playButton(c *ui.Context, label string) ui.Element {
	button := ui.PrimaryButton(c, "").Label(label)
	button.Children(func() {
		ui.Icon(c, playIcon).FontSize(13)
		ui.Text(c, label).SingleLine()
	})
	return button
}

// rowsList draws the page as one column of rows: artwork, or the place of
// a song that has none, the title over the subtitle, and the length.
func (a *app) rowsList(c *ui.Context) {
	list := ui.List(c, &a.list, len(a.rows), func(i int) {
		switch r := a.rows[i]; {
		case r.header:
			a.sectionHeading(c, r.title)
		case r.more:
			a.moreRow(c)
		default:
			a.itemRow(c, r)
		}
	}).Grow(1).Padding(0, 12, 16)
	a.activateSubmitted(list)
}

// itemRow is one row of the rows layout.
func (a *app) itemRow(c *ui.Context, r row) {
	t := c.Theme()
	line := ui.Row(c).Gap(12).Padding(7, 12).AlignItems(ui.Center)
	if line.Hovered() {
		line.Background(t.SurfaceHover)
	}
	if line.Clicked() {
		a.activate(r)
	}
	line.Children(func() {
		// A song on an album or playlist page carries no artwork of its
		// own; its place in the list is more use than an empty square.
		if r.item.Thumbnail == "" {
			ui.Text(c, trackNumber(r)).Width(38).TextAlign(ui.End).FontSize(12).
				TextColor(t.TextMuted).Shrink(0)
		} else {
			ui.Image(c, a.thumbs.bitmap(r.item.Thumbnail, 96)).Size(38, 38).Fit(ui.Cover).
				Background(t.Surface).Shrink(0)
		}
		a.itemTitle(c, r)
		if r.item.Duration != "" {
			ui.Text(c, r.item.Duration).FontSize(12).TextColor(t.TextMuted).Shrink(0)
		}
	})
}

// itemTitle is an item's title over its subtitle, in the room a row leaves
// for it.
func (a *app) itemTitle(c *ui.Context, r row) {
	t := c.Theme()
	ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
		ui.Text(c, r.item.Title).SingleLine()
		if r.item.Subtitle != "" {
			ui.Text(c, r.item.Subtitle).SingleLine().FontSize(12).TextColor(t.TextMuted)
		}
	})
}

// compactList draws the page as dense rows, twice as many as the rows
// layout fits: the place of a song becomes a play button under the
// pointer, and its subtitle sits beside its title rather than under it.
func (a *app) compactList(c *ui.Context) {
	list := ui.List(c, &a.list, len(a.rows), func(i int) {
		switch r := a.rows[i]; {
		case r.header:
			a.compactHeading(c, r.title)
		case r.more:
			a.moreRow(c)
		default:
			a.compactRow(c, r)
		}
	}).Grow(1).Padding(0, 12, 16)
	a.activateSubmitted(list)
}

// activateSubmitted opens the chosen row when the list reports a double
// click or Enter, whichever layout drew it.
func (a *app) activateSubmitted(list ui.Element) {
	if list.Submitted() && a.selected >= 0 && a.selected < len(a.rows) {
		a.activate(a.rows[a.selected])
	}
}

// compactHeading names a section. The list pins it while its rows scroll
// under it, so it needs a background of its own.
func (a *app) compactHeading(c *ui.Context, title string) {
	t := c.Theme()
	ui.Text(c, strings.ToUpper(title)).FontSize(11).Bold().LetterSpacing(0.6).
		TextColor(t.TextMuted).FillWidth().NoWrap().Padding(14, 10, 4).
		Background(t.Background)
}

// compactRow is one row of the compact layout.
func (a *app) compactRow(c *ui.Context, r row) {
	t := c.Theme()
	line := ui.Row(c).Gap(10).Height(32).PaddingX(10).AlignItems(ui.Center)
	hovered := line.Hovered()
	if hovered {
		line.Background(t.SurfaceHover)
	}
	if line.Clicked() {
		a.activate(r)
	}
	line.Children(func() {
		// The leading column holds the place of a song, or the artwork of
		// anything else, so the titles of a section line up.
		switch {
		case r.track >= 0:
			lead := ui.Row(c).Width(24).Shrink(0).Justify(ui.End).AlignItems(ui.Center)
			lead.Children(func() {
				if hovered {
					ui.Icon(c, playIcon).FontSize(11)
					return
				}
				ui.Text(c, trackNumber(r)).FontSize(12).TextColor(t.TextMuted)
			})
		case r.item.Thumbnail != "":
			ui.Image(c, a.thumbs.bitmap(r.item.Thumbnail, 64)).Size(24, 24).Fit(ui.Cover).
				Background(t.Surface).Shrink(0)
		default:
			ui.Box(c).Width(24).Shrink(0)
		}
		a.itemLine(c, r)
		if r.item.Duration != "" {
			ui.Text(c, r.item.Duration).Width(48).TextAlign(ui.End).FontSize(12).
				TextColor(t.TextMuted).Shrink(0)
		}
	})
}

// itemLine is an item's title with its subtitle beside it, for a row that
// has no room to stack them.
func (a *app) itemLine(c *ui.Context, r row) {
	t := c.Theme()
	ui.Row(c).Grow(1).MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, r.item.Title).SingleLine().MinWidth(0).Shrink(1)
		if r.item.Subtitle != "" {
			ui.Text(c, r.item.Subtitle).SingleLine().MinWidth(0).Shrink(1).
				FontSize(12).TextColor(t.TextMuted)
		}
	})
}

// columnsList draws the page as a table: the place of an item, its title,
// what is under it, and its length, under a header that stays while the
// rows scroll. The user can resize and reorder the columns.
func (a *app) columnsList(c *ui.Context) {
	t := c.Theme()
	columns := []ui.TableColumn{
		{Title: "#", ID: "index", Width: 44, Align: ui.End, Fixed: true},
		{Title: "Title", ID: "title", MinWidth: 160},
		{Title: "Details", ID: "details", Width: 240, MinWidth: 120, MaxWidth: 420},
		{Title: "Time", ID: "time", Width: 84, MinWidth: 84, Align: ui.End, Fixed: true},
	}
	table := ui.Table(c, &a.list, columns, len(a.rows), func(i, col int) {
		r := a.rows[i]
		if r.header {
			// A heading row spans every column, which the table builds as
			// its first.
			if col == 0 {
				ui.Text(c, r.title).Bold().FontSize(13).Padding(4, 0)
			}
			return
		}
		switch col {
		case 0:
			lead := ui.Row(c).Shrink(0).Justify(ui.End).AlignItems(ui.Center)
			lead.Children(func() {
				if r.item.Duration != "" && lead.Hovered() {
					ui.Icon(c, playIcon).FontSize(11)
					return
				}
				if r.track >= 0 {
					ui.Text(c, trackNumber(r)).FontSize(12).TextColor(t.TextMuted)
				}
			})
		case 1:
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				if r.item.Thumbnail != "" {
					ui.Image(c, a.thumbs.bitmap(r.item.Thumbnail, 64)).Size(28, 28).
						Fit(ui.Cover).Background(t.Surface).Shrink(0)
				}
				ui.Text(c, r.item.Title).SingleLine().MinWidth(0).Shrink(1)
			})
		case 2:
			ui.Text(c, r.item.Subtitle).SingleLine().FontSize(12).TextColor(t.TextMuted)
		case 3:
			ui.Text(c, r.item.Duration).FontSize(12).TextColor(t.TextMuted)
		}
	}).Grow(1).Padding(0, 12, 16)
	if table.Submitted() && a.selected >= 0 && a.selected < len(a.rows) {
		a.activate(a.rows[a.selected])
	}
}

// gridList draws the page as square cards of artwork, a section at a time.
// The cards are as wide as the room allows: the grid measures the room it
// had in the last frame to work out how many fit.
func (a *app) gridList(c *ui.Context) {
	sections := a.cardSections()
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(4, 12, 16).Gap(22).Children(func() {
			for i := range sections {
				section := sections[i]
				ui.Column(c).Gap(10).Children(func() {
					if section.title != "" {
						a.sectionHeading(c, section.title)
					}
					grid := ui.Grid(c).Key(i).Columns(a.cardColumns()).Gap(cardGap)
					grid.Children(func() {
						for _, index := range section.rows {
							a.card(c, a.rows[index])
						}
					})
					a.gridWidth = grid.Bounds().W
				})
			}
			if n := len(a.rows); n > 0 && a.rows[n-1].more {
				a.moreRow(c)
			}
		})
	})
}

// cardColumns is how many cards fit the room the grid had in the last
// frame, at least one.
func (a *app) cardColumns() int {
	width := a.gridWidth
	if width <= 0 {
		width = 760
	}
	columns := int((width + cardGap) / (cardMin + cardGap))
	return max(columns, 1)
}

// cardSection is one section of the grid layout: its name, and the rows of
// the page that belong to it.
type cardSection struct {
	title string
	rows  []int
}

// cardSections groups the page's rows by section, in order, so a layout
// that draws the items itself can place the headings too.
func (a *app) cardSections() []cardSection {
	var sections []cardSection
	for i, r := range a.rows {
		switch {
		case r.header:
			sections = append(sections, cardSection{title: r.title})
		case r.more:
			// The button that loads more follows the grid.
		default:
			if len(sections) == 0 {
				sections = append(sections, cardSection{})
			}
			last := &sections[len(sections)-1]
			last.rows = append(last.rows, i)
		}
	}
	return sections
}

// card shows one item as artwork with its title under it. A click plays or
// opens it; the play button rises out of the artwork under the pointer.
func (a *app) card(c *ui.Context, r row) {
	t := c.Theme()
	card := ui.Column(c).Gap(8).Role(ui.RoleButton).Label(r.item.Title).Focusable()
	if card.Clicked() {
		a.activate(r)
	}
	card.Children(func() {
		art := ui.Box(c).AspectRatio(1).Clip()
		art.Children(func() {
			ui.Image(c, a.thumbs.bitmap(r.item.Thumbnail, 320)).Fit(ui.Cover).Grow(1).
				Background(t.Surface)
			if art.Hovered() {
				badge := ui.Box(c).Attach(ui.AnchorBottomRight, ui.AnchorBottomRight).
					Right(10).Bottom(10).Padding(6).Background(t.Text)
				badge.Children(func() {
					ui.Icon(c, playIcon).FontSize(12).TextColor(t.Background)
				})
			}
		})
		ui.Text(c, r.item.Title).SingleLine()
		if r.item.Subtitle != "" {
			ui.Text(c, r.item.Subtitle).SingleLine().FontSize(12).TextColor(t.TextMuted)
		}
	})
}

// splitList draws the page as rows, with a pane beside them describing the
// item the user is on.
func (a *app) splitList(c *ui.Context) {
	ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
		a.rowsList(c)
		a.previewPane(c)
	})
}

// previewPane describes one item: its artwork, what it is, and the button
// that plays it.
func (a *app) previewPane(c *ui.Context) {
	t := c.Theme()
	r, ok := a.previewRow()
	ui.Column(c).Width(300).Shrink(0).Gap(10).Padding(20).Background(t.Surface).
		BorderWidth(0, 0, 0, 1).BorderColor(t.Border).Children(func() {
		if !ok {
			ui.Text(c, "Nothing to show yet").FontSize(12).TextColor(t.TextMuted)
			return
		}
		// A song on an album or playlist page carries no artwork of its
		// own; the page's is what it belongs to.
		art := r.item.Thumbnail
		if art == "" {
			art = a.detail.art
		}
		if art != "" {
			ui.Image(c, a.thumbs.bitmap(art, 512)).AspectRatio(1).Fit(ui.Cover).
				Background(t.Background).Shrink(0)
		}
		ui.Text(c, r.item.Title).FontSize(16).Bold().MaxLines(2)
		if r.item.Subtitle != "" {
			ui.Text(c, r.item.Subtitle).FontSize(12).TextColor(t.TextMuted).MaxLines(3)
		}
		if r.track >= 0 {
			if playButton(c, "Play").Clicked() {
				a.play(r.item, a.playable, r.track)
			}
		}
		if r.item.Duration != "" {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Length").FontSize(12).TextColor(t.TextMuted)
				ui.Text(c, r.item.Duration).FontSize(12)
			})
		}
	})
}

// previewRow is the row the split layout describes: the one the user is
// on, or the first the page holds.
func (a *app) previewRow() (row, bool) {
	if a.selected >= 0 && a.selected < len(a.rows) {
		if r := a.rows[a.selected]; !r.header && !r.more {
			return r, true
		}
	}
	for _, r := range a.rows {
		if !r.header && !r.more {
			return r, true
		}
	}
	return row{}, false
}
