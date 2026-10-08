// Meiro is a small desktop client for YouTube Music, drawn by MyGo itself.
//
// It browses the public catalogue with the youtube package, and the
// account's library once the user signs in with their Google account.
// Playback resolves a stream URL and decodes it in a child ffmpeg; see
// package player.
package main

import (
	"context"
	"log"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/player"
	"github.com/elianiva/meiro/youtube"
)

// app is the state the window shows.
type app struct {
	win    *mygo.Window
	router *ui.Router
	player *player.Player
	thumbs *thumbCache

	// run runs work off the main thread. Tests replace it with a runner
	// that runs the work inline, so a frame sees its result at once.
	run func(work func())

	// The YouTube clients: public browses without an account, authed with
	// one. The store keeps a sign-in across restarts.
	public   *youtube.Client
	authed   *youtube.Client
	oauth    *youtube.OAuth
	store    youtube.TokenStore
	account  youtube.AccountDetails
	signedIn bool
	signIn   signInState

	// The page shown, and what it holds.
	location string
	feed     pageState
	search   searchState
	rows     []row
	playable []youtube.MusicItem
	list     ui.ListState
	selected int
	// layout is how the list draws its rows, and gridWidth the room the
	// cards of the grid layout had in the last frame; see layouts.go.
	layout    int
	gridWidth float32
	// detail is the heading of the album, playlist or artist page shown,
	// and details remembers one for each page the user opened, so going
	// back to one finds its heading again.
	detail  detail
	details map[string]detail

	// Playback.
	current   youtube.MusicItem
	queue     []youtube.MusicItem
	index     int
	total     time.Duration
	scrub     float64
	scrubbing bool
	volume    float64
	playErr   string

	// job numbers page loads, so a slow one for a page the user has left
	// is dropped when it lands.
	job    int
	cancel context.CancelFunc
}

// detail remembers what the page being opened is, for its heading.
type detail struct {
	title    string
	subtitle string
	art      string
}

func newApp() *app {
	a := &app{
		router:   ui.NewRouter("/home"),
		player:   player.New(),
		selected: -1,
		volume:   70,
		search:   searchState{kind: "All"},
		details:  make(map[string]detail),
	}
	a.run = func(work func()) { go work() }
	a.thumbs = newThumbCache(a.refresh)
	a.player.SetVolume(a.volume / 100)
	return a
}

func main() {
	a := newApp()
	mygo.App.WhenReady(func() {
		a.setup()
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "Meiro",
			Width:     1080,
			Height:    720,
			MinWidth:  760,
			MinHeight: 480,
			StateKey:  "meiro",
			Content:   ui.View(a.view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// setup prepares the account: the token store, the two clients, and the
// sign-in an earlier run left behind.
func (a *app) setup() {
	store, err := newTokenStore()
	if err != nil {
		a.signIn.err = err.Error()
		return
	}
	a.store = store
	a.oauth = youtube.NewOAuth(youtube.OAuthConfig{TokenStore: store})
	a.public = youtube.NewClient(youtube.Options{})
	a.authed = youtube.NewClient(youtube.Options{OAuth: a.oauth})
	a.restoreAccount()
}

// client returns the client to use for a request: the account's when the
// user is signed in, the public one otherwise.
func (a *app) client() *youtube.Client {
	if a.signedIn && a.authed != nil {
		return a.authed
	}
	return a.public
}

// update runs fn where the view can see its result, on the main thread, and
// draws a frame. Without a window, as in tests, it runs fn at once.
func (a *app) update(fn func()) {
	if a.win == nil {
		fn()
		return
	}
	a.win.Update(fn)
}

// refresh draws a frame with what background work produced.
func (a *app) refresh() {
	if a.win != nil {
		a.win.Invalidate()
	}
}

// view builds the window: the sidebar, the page the router shows, and the
// player bar.
func (a *app) view(c *ui.Context) {
	monoTheme(c)
	if location := a.router.Location(); location != a.location {
		a.location = location
		a.onNavigate()
	}
	a.tick(c)
	if !a.signIn.open && a.signIn.cancel != nil {
		a.signIn.cancel()
		a.signIn.cancel = nil
	}

	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
			a.sidebar(c)
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				a.page(c)
			})
		})
		if a.current.VideoID != "" {
			a.playerBar(c)
		}
	})
	a.signInModal(c)

	// The transport, wherever the keyboard focus is that does not want the
	// keys itself.
	if c.Shortcut(0, ui.KeySpace) {
		a.togglePlay()
	}
	if c.Shortcut(ui.Cmd, ui.KeyLeft) {
		a.previous()
	}
	if c.Shortcut(ui.Cmd, ui.KeyRight) {
		a.advance()
	}
}

// tick advances what the frame depends on: the player's position, the next
// track when one ends, and the frame after this one.
func (a *app) tick(c *ui.Context) {
	if !a.player.Active() {
		return
	}
	if failure := a.player.Failure(); failure != "" {
		a.playErr = failure
		return
	}
	if a.player.Ended() {
		a.advance()
		return
	}
	if a.player.Playing() {
		if !a.scrubbing {
			a.scrub = a.player.Position().Seconds()
		}
		c.After(250 * time.Millisecond)
	}
}

func (a *app) sidebar(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(210).Background(t.Surface).PaddingY(10).Gap(6).Shrink(0).Children(func() {
		ui.Text(c, "Meiro").FontSize(15).Bold().Padding(6, 16, 8)
		page := navPage(a.router.Path())
		if ui.Sidebar(c, &page, func() {
			ui.SidebarItem(c, "home", homeIcon, "Home")
			ui.SidebarItem(c, "explore", exploreIcon, "Explore")
			ui.SidebarItem(c, "library", libraryIcon, "Library")
			ui.SidebarItem(c, "search", searchIcon, "Search")
		}).Grow(1).Label("Pages").Changed() {
			a.router.Push("/" + page)
		}
		a.accountPanel(c)
	})
}

// navPage returns the sidebar item a path belongs to, and "" for a page the
// sidebar does not name, as an album or a playlist.
func navPage(path string) string {
	switch path {
	case "/home", "/explore", "/library", "/search":
		return path[1:]
	}
	return ""
}

// monoTheme replaces the theme's colors with a black and white palette that
// still follows the desktop's light or dark appearance.
func monoTheme(c *ui.Context) {
	t := *c.Theme()
	if t.Dark {
		t.Background = ui.Hex("#0a0a0a")
		t.Surface = ui.Hex("#141414")
		t.SurfaceHover = ui.Hex("#1f1f1f")
		t.SurfacePressed = ui.Hex("#2a2a2a")
		t.Border = ui.Hex("#262626")
		t.Text = ui.Hex("#fafafa")
		t.TextMuted = ui.Hex("#8c8c8c")
		t.Accent = ui.Hex("#fafafa")
		t.AccentHover = ui.Hex("#e5e5e5")
		t.AccentPressed = ui.Hex("#d4d4d4")
		t.AccentText = ui.Hex("#0a0a0a")
		t.Selection = ui.RGBA(250, 250, 250, 0.22)
		t.Focus = ui.RGBA(250, 250, 250, 0.5)
		t.Scrollbar = ui.RGBA(255, 255, 255, 0.28)
	} else {
		t.Background = ui.Hex("#ffffff")
		t.Surface = ui.Hex("#f5f5f5")
		t.SurfaceHover = ui.Hex("#ededed")
		t.SurfacePressed = ui.Hex("#e0e0e0")
		t.Border = ui.Hex("#e6e6e6")
		t.Text = ui.Hex("#0a0a0a")
		t.TextMuted = ui.Hex("#737373")
		t.Accent = ui.Hex("#0a0a0a")
		t.AccentHover = ui.Hex("#262626")
		t.AccentPressed = ui.Hex("#404040")
		t.AccentText = ui.Hex("#ffffff")
		t.Selection = ui.RGBA(10, 10, 10, 0.16)
		t.Focus = ui.RGBA(10, 10, 10, 0.4)
		t.Scrollbar = ui.RGBA(0, 0, 0, 0.28)
	}
	// Square corners everywhere: nothing in the interface has a rounded
	// border.
	t.Radius = 0
	c.SetTheme(&t)
}
