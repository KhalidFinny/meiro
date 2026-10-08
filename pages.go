package main

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/youtube"
)

// What a page, or the thing an item opens, is.
const (
	pageTrack    = "track"
	pageAlbum    = "album"
	pagePlaylist = "playlist"
	pageArtist   = "artist"
	pageHome     = "home"
	pageExplore  = "explore"
	pageLibrary  = "library"
	pageSearch   = "search"
)

// searchKinds maps the search page's choices to the package's filters.
var searchKinds = map[string]youtube.SearchType{
	"All":       youtube.SearchAll,
	"Songs":     youtube.SearchSongs,
	"Albums":    youtube.SearchAlbums,
	"Artists":   youtube.SearchArtists,
	"Playlists": youtube.SearchPlaylists,
	"Videos":    youtube.SearchVideos,
}

var searchKindNames = []string{"All", "Songs", "Albums", "Artists", "Playlists", "Videos"}

// pageState is what a page has loaded, whichever page it is.
type pageState struct {
	loading  bool
	err      string
	sections []youtube.MusicSection
	items    []youtube.MusicItem
	more     string
}

// searchState is the search page, which also waits for typing to pause.
type searchState struct {
	pageState
	query string
	kind  string
	at    time.Time
}

// row is one line of a page: a section heading, an item, or the button that
// loads the next page of items.
type row struct {
	header bool
	more   bool
	title  string
	item   youtube.MusicItem
	// track is the item's place among the page's playable items, or -1.
	track int
	// seq tells rows that carry no identity apart.
	seq int
}

// key identifies a row across frames, so the list keeps its place and its
// choice when items change.
func (r row) key() any {
	switch {
	case r.header:
		return "header:" + strconv.Itoa(r.seq)
	case r.more:
		return "more"
	default:
		return "item:" + r.item.ID + "\x00" + r.item.Title
	}
}

// onNavigate resets the page state and loads what the new location shows.
// The heading is kept for an album, playlist or artist page, which the
// action that opened it already described.
func (a *app) onNavigate() {
	a.feed = pageState{}
	a.search = searchState{query: a.search.query, kind: a.search.kind}
	a.rows, a.playable, a.selected = nil, nil, -1
	// The list is shared by every page, so a new page starts at its top.
	a.list.ScrollTo(0, ui.Start)

	path := a.router.Path()
	switch {
	case path == "/home":
		a.detail = detail{}
		a.loadFeed(pageHome)
	case path == "/explore":
		a.detail = detail{}
		a.loadFeed(pageExplore)
	case path == "/library":
		a.detail = detail{}
		if a.signedIn {
			a.loadFeed(pageLibrary)
		}
	case path == "/search":
		a.detail = detail{}
		if a.search.query != "" {
			a.loadSearch()
		}
	case strings.HasPrefix(path, "/album/"):
		a.detail = a.details[path]
		a.loadDetail(pageAlbum, pathArg(path))
	case strings.HasPrefix(path, "/playlist/"):
		a.detail = a.details[path]
		a.loadDetail(pagePlaylist, pathArg(path))
	case strings.HasPrefix(path, "/artist/"):
		a.detail = a.details[path]
		a.loadDetail(pageArtist, pathArg(path))
	}
}

// pathArg returns the ID at the end of a page's path.
func pathArg(path string) string {
	_, id, _ := strings.Cut(path, "/")
	_, id, _ = strings.Cut(id, "/")
	if decoded, err := url.PathUnescape(id); err == nil {
		return decoded
	}
	return id
}

// loadFeed fetches one of the pages that need no argument.
func (a *app) loadFeed(page string) {
	a.fetch(func(ctx context.Context) (*youtube.BrowseResult, error) {
		switch page {
		case pageHome:
			return a.client().GetHomeFeed(ctx)
		case pageExplore:
			return a.client().GetExplore(ctx)
		default:
			return a.client().GetAllLibrary(ctx)
		}
	})
}

// loadDetail fetches an album, a playlist or an artist page.
func (a *app) loadDetail(kind, id string) {
	a.fetch(func(ctx context.Context) (*youtube.BrowseResult, error) {
		switch kind {
		case pageAlbum:
			return a.client().GetAlbum(ctx, id)
		case pagePlaylist:
			return a.client().GetPlaylist(ctx, id)
		default:
			return a.client().GetArtist(ctx, id)
		}
	})
}

// fetch runs a page load off the main thread and applies it, dropping the
// result when a newer load has replaced it.
func (a *app) fetch(load func(ctx context.Context) (*youtube.BrowseResult, error)) {
	if a.client() == nil {
		return
	}
	a.feed.loading, a.feed.err = true, ""
	a.job++
	job := a.job
	if a.cancel != nil {
		a.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.run(func() {
		result, err := load(ctx)
		a.update(func() {
			if job != a.job {
				return
			}
			a.feed.loading = false
			a.selected = -1
			if err != nil {
				a.feed.err = err.Error()
				return
			}
			a.feed.sections = result.Sections
			a.feed.items = result.Items
			a.feed.more = result.ContinuationToken
		})
	})
}

// loadSearch runs the query the user typed, with the chosen filter.
func (a *app) loadSearch() {
	query := strings.TrimSpace(a.search.query)
	a.search.at = time.Time{}
	if query == "" {
		a.search = searchState{kind: a.search.kind}
		return
	}
	if a.client() == nil {
		return
	}
	a.search.loading, a.search.err, a.search.more = true, "", ""
	a.search.items = nil
	a.list.ScrollTo(0, ui.Start)
	a.job++
	job := a.job
	if a.cancel != nil {
		a.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	kind := searchKinds[a.search.kind]
	a.run(func() {
		result, err := a.client().Search(ctx, query, youtube.SearchOptions{Type: kind})
		a.update(func() {
			if job != a.job {
				return
			}
			a.search.loading = false
			a.selected = -1
			if err != nil {
				a.search.err = err.Error()
				return
			}
			a.search.items = result.Items
			a.search.more = result.ContinuationToken
		})
	})
}

// loadMore appends the next page of items to the one shown.
func (a *app) loadMore() {
	s := a.pageState()
	if s.more == "" || a.client() == nil {
		return
	}
	token, searching := s.more, a.router.Path() == "/search"
	s.more = ""
	a.job++
	job := a.job
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		var items []youtube.MusicItem
		var next string
		var err error
		if searching {
			var result *youtube.SearchResult
			if result, err = a.client().ContinueSearch(ctx, token); err == nil {
				items, next = result.Items, result.ContinuationToken
			}
		} else {
			var result *youtube.BrowseResult
			if result, err = a.client().ContinueBrowse(ctx, token); err == nil {
				items, next = result.Items, result.ContinuationToken
			}
		}
		a.update(func() {
			if job != a.job {
				return
			}
			if err != nil {
				s.err = err.Error()
				return
			}
			if len(s.sections) > 0 {
				last := &s.sections[len(s.sections)-1]
				last.Items = append(last.Items, items...)
			} else {
				s.items = append(s.items, items...)
			}
			s.more = next
		})
	})
}

// pageState returns the state of the page the router shows.
func (a *app) pageState() *pageState {
	if a.router.Path() == "/search" {
		return &a.search.pageState
	}
	return &a.feed
}

// page builds the page the router shows.
func (a *app) page(c *ui.Context) {
	a.router.View(c, func(r *ui.Route) {
		switch {
		case r.Match("/home"):
			r.Title("Home")
			a.contentPage(c, "Home")
		case r.Match("/explore"):
			r.Title("Explore")
			a.contentPage(c, "Explore")
		case r.Match("/library"):
			r.Title("Library")
			a.libraryPage(c)
		case r.Match("/search"):
			r.Title("Search")
			a.searchPage(c)
		case r.Match("/album/{id}"):
			r.Title("Album")
			a.contentPage(c, "Album")
		case r.Match("/playlist/{id}"):
			r.Title("Playlist")
			a.contentPage(c, "Playlist")
		case r.Match("/artist/{id}"):
			r.Title("Artist")
			a.contentPage(c, "Artist")
		default:
			r.Title("Not found")
			a.centered(c, "Not found", "That page does not exist.", "", nil)
		}
	})
}

func (a *app) libraryPage(c *ui.Context) {
	if !a.signedIn {
		t := c.Theme()
		ui.Column(c).Fill().Center().Gap(10).Padding(24).Children(func() {
			ui.Text(c, "Your library").FontSize(18).Bold()
			ui.Text(c, "Sign in with your Google account to see your songs, albums and playlists.").
				TextColor(t.TextMuted).MaxWidth(460).TextAlign(ui.Center)
			if ui.PrimaryButton(c, "Sign in with Google").Clicked() {
				a.signInWithGoogle()
			}
		})
		return
	}
	a.contentPage(c, "Library")
}

// contentPage shows a loaded browse page under a heading, with a button
// that plays everything on it.
func (a *app) contentPage(c *ui.Context, fallback string) {
	a.setRows(a.pageState())
	ui.Column(c).Fill().Children(func() {
		a.heading(c, fallback)
		a.body(c)
	})
}

func (a *app) searchPage(c *ui.Context) {
	ui.Column(c).Fill().Children(func() {
		ui.Column(c).Padding(20, 24, 8).Gap(12).Children(func() {
			ui.Text(c, "Search").FontSize(24).Bold()
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				field := ui.SearchField(c, &a.search.query).Label("Search YouTube Music").Grow(1).MaxWidth(480)
				switch {
				case field.Submitted():
					a.loadSearch()
				case field.Changed():
					a.search.at = c.Now().Add(300 * time.Millisecond)
					c.After(300 * time.Millisecond)
				}
				if ui.Select(c, &a.search.kind, searchKindNames).Label("Type").Width(150).Changed() {
					a.loadSearch()
				}
			})
		})
		// Typing runs the search once it pauses, not on every keystroke.
		if !a.search.at.IsZero() && !c.Now().Before(a.search.at) {
			a.loadSearch()
		}
		a.setRows(a.pageState())
		a.body(c)
	})
}

// heading shows what the page is, and the button that plays all of it.
func (a *app) heading(c *ui.Context, fallback string) {
	t := c.Theme()
	title, subtitle, art := a.detail.title, a.detail.subtitle, a.detail.art
	if title == "" {
		title = fallback
	}
	ui.Row(c).Gap(16).Padding(20, 24, 12).AlignItems(ui.Center).Children(func() {
		if art != "" {
			ui.Image(c, a.thumbs.bitmap(art, 160)).Size(88, 88).Fit(ui.Cover).Radius(10).Background(t.Surface).Shrink(0)
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Text(c, title).FontSize(26).Bold().SingleLine()
			if subtitle != "" {
				ui.Text(c, subtitle).TextColor(t.TextMuted).SingleLine()
			}
		})
		if len(a.playable) == 0 {
			return
		}
		play := ui.PrimaryButton(c, "").Label("Play")
		play.Children(func() {
			ui.Icon(c, playIcon).FontSize(13)
			ui.Text(c, "Play").SingleLine()
		})
		if play.Clicked() {
			a.playAll()
		}
	})
}

// body shows the page's rows, or what to do while they are not there.
func (a *app) body(c *ui.Context) {
	t := c.Theme()
	s := a.pageState()
	switch {
	case s.loading:
		ui.Row(c).Padding(24).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c)
			ui.Text(c, "Loading…").TextColor(t.TextMuted)
		})
	case s.err != "":
		a.centered(c, "Something went wrong", s.err, "Try again", a.retry)
	case len(a.rows) == 0:
		a.centered(c, "Nothing here yet", "", "", nil)
	default:
		a.listView(c)
	}
}

// listView shows the page's rows. A row is chosen by a click or the arrow
// keys, and opened by a double click or Enter.
func (a *app) listView(c *ui.Context) {
	if a.selected >= len(a.rows) {
		a.selected = -1
	}
	a.list.Key = func(i int) any { return a.rows[i].key() }
	a.list.Label = func(i int) string { return a.rows[i].title }
	a.list.Header = func(i int) bool { return a.rows[i].header }
	a.list.Selected = &a.selected
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
	if list.Submitted() && a.selected >= 0 {
		a.activate(a.rows[a.selected])
	}
}

// setRows flattens the loaded page into rows, and collects the items that
// playing the page would queue.
func (a *app) setRows(s *pageState) {
	a.rows, a.playable = a.rows[:0], a.playable[:0]
	if s.loading || s.err != "" {
		return
	}
	for _, section := range s.sections {
		if section.Title != "" {
			a.rows = append(a.rows, row{header: true, title: section.Title, seq: len(a.rows)})
		}
		for _, item := range section.Items {
			a.appendRow(item)
		}
	}
	if len(s.sections) == 0 {
		for _, item := range s.items {
			a.appendRow(item)
		}
	}
	if s.more != "" {
		a.rows = append(a.rows, row{more: true, title: "Load more"})
	}
}

func (a *app) appendRow(item youtube.MusicItem) {
	track := -1
	if kind, _ := targetOf(item); kind == pageTrack {
		track = len(a.playable)
		a.playable = append(a.playable, item)
	}
	a.rows = append(a.rows, row{title: item.Title, item: item, track: track})
}

func (a *app) itemRow(c *ui.Context, r row) {
	t := c.Theme()
	line := ui.Row(c).Gap(12).Padding(7, 12).Radius(8).AlignItems(ui.Center)
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
				Radius(6).Background(t.Surface).Shrink(0)
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, r.item.Title).SingleLine()
			if r.item.Subtitle != "" {
				ui.Text(c, r.item.Subtitle).SingleLine().FontSize(12).TextColor(t.TextMuted)
			}
		})
		if r.item.Duration != "" {
			ui.Text(c, r.item.Duration).FontSize(12).TextColor(t.TextMuted).Shrink(0)
		}
	})
}

// trackNumber is the place an item holds among the page's songs, and "" for
// an item that is not a song.
func trackNumber(r row) string {
	if r.track < 0 {
		return ""
	}
	return strconv.Itoa(r.track + 1)
}

func (a *app) sectionHeading(c *ui.Context, title string) {
	ui.Text(c, title).FontSize(15).Bold().Padding(18, 12, 6)
}

func (a *app) moreRow(c *ui.Context) {
	ui.Row(c).Padding(10, 12).Children(func() {
		if ui.Button(c, "Load more").Clicked() {
			a.loadMore()
		}
	})
}

// activate opens what a row holds: a song plays, and an album, artist or
// playlist opens its page.
func (a *app) activate(r row) {
	switch {
	case r.more:
		a.loadMore()
		return
	case r.header:
		return
	}
	kind, id := targetOf(r.item)
	switch kind {
	case pageTrack:
		a.play(r.item, a.playable, r.track)
	case pageAlbum, pagePlaylist, pageArtist:
		path := "/" + kind + "/" + url.PathEscape(id)
		a.details[path] = detail{title: r.item.Title, subtitle: r.item.Subtitle, art: r.item.Thumbnail}
		a.router.Push(path)
	}
}

// targetOf says what an item opens. Album, artist and playlist IDs are
// recognizable by their prefixes; anything else with a video ID is a song.
func targetOf(item youtube.MusicItem) (kind, id string) {
	browse, playlist := item.BrowseID, item.PlaylistID
	if playlist == "" {
		playlist = browse
	}
	switch {
	case strings.HasPrefix(browse, "MPR"), strings.Contains(browse, "privately_owned_release"):
		return pageAlbum, browse
	case strings.HasPrefix(playlist, "VL"), strings.HasPrefix(playlist, "PL"), strings.HasPrefix(playlist, "RD"):
		return pagePlaylist, playlist
	case strings.HasPrefix(browse, "UC"), strings.Contains(browse, "privately_owned_artist"):
		return pageArtist, browse
	case item.VideoID != "":
		return pageTrack, item.VideoID
	}
	return "", ""
}

// centered shows a message in the middle of the page, with a button when
// there is something to do about it.
func (a *app) centered(c *ui.Context, title, detail, action string, run func()) {
	t := c.Theme()
	ui.Column(c).Fill().Center().Gap(8).Padding(24).Children(func() {
		ui.Text(c, title).FontSize(16).Bold()
		if detail != "" {
			ui.Text(c, detail).TextColor(t.TextMuted).MaxLines(4).MaxWidth(520).TextAlign(ui.Center)
		}
		if action != "" && run != nil {
			if ui.Button(c, action).Clicked() {
				run()
			}
		}
	})
}

// retry loads the page shown again.
func (a *app) retry() {
	if a.router.Path() == "/search" {
		a.loadSearch()
		return
	}
	a.onNavigate()
}
