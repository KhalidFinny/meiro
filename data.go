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
	pageSettings = "settings"
)

// searchKinds are the filters of the search page, in the order its chips show
// them.
var searchKinds = []struct {
	name string
	kind youtube.SearchType
}{
	{"All", youtube.SearchAll},
	{"Songs", youtube.SearchSongs},
	{"Albums", youtube.SearchAlbums},
	{"Artists", youtube.SearchArtists},
	{"Playlists", youtube.SearchPlaylists},
	{"Videos", youtube.SearchVideos},
}

// pageState is what a page has loaded, whichever page it is.
type pageState struct {
	loading  bool
	err      string
	sections []youtube.MusicSection
	items    []youtube.MusicItem
	more     string
}

// searchState is the search page. Typing only edits query; a search runs when
// the user submits it, and submitted is what the results are for.
type searchState struct {
	pageState
	query     string
	submitted string
	kind      int
}

// rowKind says what a row of a page's list holds.
type rowKind int

const (
	rowHero    rowKind = iota // the heading of an album, playlist or artist
	rowHeading                // the name of a section, with arrows for its shelf
	rowCards                  // a shelf of cards that scrolls sideways
	rowColumns                // a shelf of songs in columns that scrolls sideways
	rowTrack                  // one song, album, artist or playlist in a column of rows
	rowMore                   // the button that loads the next page
	rowLoading                // the page is loading
	rowError                  // the page failed
	rowEmpty                  // the page has nothing on it
)

// row is one line of a page's list.
type row struct {
	kind  rowKind
	title string
	// shelf names the carousel a heading steers, and a shelf draws.
	shelf string
	// items are the cards or songs of a shelf; item is the one of a track row.
	items []youtube.MusicItem
	item  youtube.MusicItem
	// queue is what playing from this row plays: the songs of its shelf, or
	// of the page.
	queue []youtube.MusicItem
	// track is the place of a song among the page's songs, or -1.
	track int
}

// key identifies a row across frames, so the list keeps its place when the
// rows change.
func (r row) key() any {
	switch r.kind {
	case rowTrack:
		return "track:" + r.item.ID + "\x00" + r.item.Title + "\x00" + strconv.Itoa(r.track)
	case rowCards, rowColumns, rowHeading:
		return strconv.Itoa(int(r.kind)) + ":" + r.shelf
	}
	return strconv.Itoa(int(r.kind)) + ":" + r.title
}

// onNavigate resets the page state and loads what the new location shows.
// The heading is kept for an album, playlist or artist page, which the
// action that opened it already described.
func (a *app) onNavigate() {
	a.feed = pageState{}
	a.rows, a.playable = nil, nil
	// The list is shared by every page, so a new page starts at its top.
	a.list.ScrollTo(0, ui.Start)
	a.npOpen = false

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
		a.focusSearch = a.search.submitted == ""
	case path == "/settings":
		a.detail = detail{}
	case strings.HasPrefix(path, "/album/"):
		a.detail = a.details[path]
		a.detail.kind = pageAlbum
		a.loadDetail(pageAlbum, pathArg(path))
	case strings.HasPrefix(path, "/playlist/"):
		a.detail = a.details[path]
		a.detail.kind = pagePlaylist
		a.loadDetail(pagePlaylist, pathArg(path))
	case strings.HasPrefix(path, "/artist/"):
		a.detail = a.details[path]
		a.detail.kind = pageArtist
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
	job := a.nextJob()
	ctx := a.jobContext()
	a.run(func() {
		result, err := load(ctx)
		a.update(func() {
			if job != a.job {
				return
			}
			a.feed.loading = false
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

// nextJob numbers a new load, so one that lands after a newer one began is
// dropped.
func (a *app) nextJob() int {
	a.job++
	return a.job
}

// jobContext cancels the load in flight and returns the context of the next.
func (a *app) jobContext() context.Context {
	if a.cancel != nil {
		a.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	return ctx
}

// runSearch searches for what the user submitted, with the chosen filter. It
// is the only way a search starts: typing in the field never does.
func (a *app) runSearch(query string) {
	query = strings.TrimSpace(query)
	if query == "" {
		a.search = searchState{kind: a.search.kind}
		return
	}
	a.search.query, a.search.submitted = query, query
	a.settings.remember(query)
	a.saveSettings()
	a.search.sections = nil
	if a.client() == nil {
		return
	}
	a.search.loading, a.search.err, a.search.more = true, "", ""
	a.search.items = nil
	a.list.ScrollTo(0, ui.Start)
	job := a.nextJob()
	ctx := a.jobContext()
	kind := searchKinds[a.search.kind].kind
	a.run(func() {
		result, err := a.client().Search(ctx, query, youtube.SearchOptions{Type: kind})
		a.update(func() {
			if job != a.job {
				return
			}
			a.search.loading = false
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
	job := a.nextJob()
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

// isDetail reports whether the page is an album, a playlist or an artist.
func isDetail(path string) bool {
	return strings.HasPrefix(path, "/album/") || strings.HasPrefix(path, "/playlist/") || strings.HasPrefix(path, "/artist/")
}

// setRows flattens the loaded page into the rows its list shows, and
// collects the songs that playing the page would queue.
func (a *app) setRows() {
	path := a.router.Path()
	s := a.pageState()
	a.rows, a.playable = a.rows[:0], a.playable[:0]
	onDetail := isDetail(path)
	if onDetail {
		a.rows = append(a.rows, row{kind: rowHero, title: a.detail.title, track: -1})
	}
	switch {
	case s.loading:
		a.rows = append(a.rows, row{kind: rowLoading, track: -1})
		return
	case s.err != "":
		a.rows = append(a.rows, row{kind: rowError, title: s.err, track: -1})
		return
	}

	// Songs on an album or playlist page carry no artwork of their own: the
	// page's is what they belong to, and what the player shows.
	fallback := ""
	if onDetail && a.detail.kind != pageArtist {
		fallback = a.detail.art
	}
	sections := s.sections
	if len(sections) == 0 && len(s.items) > 0 {
		sections = []youtube.MusicSection{{Items: s.items}}
	}
	vertical := onDetail && a.detail.kind != pageArtist || path == "/search"
	for index, section := range sections {
		if len(section.Items) == 0 {
			continue
		}
		shelf := path + "#" + strconv.Itoa(index)
		songs := 0
		for _, item := range section.Items {
			if kind, _ := targetOf(item); kind == pageTrack {
				songs++
			}
		}
		if section.Title != "" {
			a.rows = append(a.rows, row{kind: rowHeading, title: section.Title, shelf: shelf, track: -1})
		}
		switch {
		case vertical || (onDetail && songs*10 >= len(section.Items)*6):
			for _, item := range section.Items {
				r := row{kind: rowTrack, item: item, track: -1}
				if kind, _ := targetOf(item); kind == pageTrack {
					r.track = len(a.playable)
					if item.Thumbnail == "" {
						item.Thumbnail = fallback
					}
					a.playable = append(a.playable, item)
				}
				a.rows = append(a.rows, r)
			}
		default:
			var queue []youtube.MusicItem
			for _, item := range section.Items {
				if kind, _ := targetOf(item); kind == pageTrack {
					queue = append(queue, item)
				}
			}
			kind := rowCards
			if songs*10 >= len(section.Items)*6 && len(section.Items) >= 3 && section.Items[0].Duration != "" {
				kind = rowColumns
			}
			a.rows = append(a.rows, row{kind: kind, shelf: shelf, items: section.Items, queue: queue, track: -1})
		}
	}
	if s.more != "" {
		a.rows = append(a.rows, row{kind: rowMore, title: "Show more", track: -1})
	}
	if len(a.rows) == 0 || (len(a.rows) == 1 && a.rows[0].kind == rowHero) {
		title := "Nothing here yet"
		if path == "/search" {
			title = "No results for “" + a.search.submitted + "”"
		}
		a.rows = append(a.rows, row{kind: rowEmpty, title: title, track: -1})
	}
}

// activate opens what a song, album, artist or playlist is: a song plays from
// its place in queue, and the others open their page.
func (a *app) activate(item youtube.MusicItem, queue []youtube.MusicItem) {
	kind, id := targetOf(item)
	switch kind {
	case pageTrack:
		index := 0
		for i, q := range queue {
			if q.VideoID == item.VideoID {
				index = i
				break
			}
		}
		a.play(item, queue, index)
	case pageAlbum, pagePlaylist, pageArtist:
		path := "/" + kind + "/" + url.PathEscape(id)
		a.details[path] = detail{title: item.Title, subtitle: item.Subtitle, art: item.Thumbnail, kind: kind}
		a.router.Push(path)
	}
}

// playCollection plays an album or a playlist from its first song, without
// opening its page, as the play button over its card does.
func (a *app) playCollection(item youtube.MusicItem) {
	kind, id := targetOf(item)
	if kind == pageTrack {
		a.play(item, []youtube.MusicItem{item}, 0)
		return
	}
	if kind != pageAlbum && kind != pagePlaylist || a.client() == nil {
		return
	}
	key := itemKey("open", item)
	if a.opening == key {
		return
	}
	a.opening = key
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		var result *youtube.BrowseResult
		var err error
		if kind == pageAlbum {
			result, err = a.client().GetAlbum(ctx, id)
		} else {
			result, err = a.client().GetPlaylist(ctx, id)
		}
		var songs []youtube.MusicItem
		if err == nil {
			all := result.Items
			for _, section := range result.Sections {
				all = append(all, section.Items...)
			}
			for _, song := range all {
				if k, _ := targetOf(song); k == pageTrack {
					if song.Thumbnail == "" {
						song.Thumbnail = item.Thumbnail
					}
					songs = append(songs, song)
				}
			}
		}
		a.update(func() {
			if a.opening == key {
				a.opening = ""
			}
			if len(songs) > 0 {
				a.play(songs[0], songs, 0)
			}
		})
	})
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

// retry loads the page shown again.
func (a *app) retry() {
	if a.router.Path() == "/search" {
		a.runSearch(a.search.submitted)
		return
	}
	a.onNavigate()
}
