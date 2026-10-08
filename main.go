// Meiro is a small desktop client for YouTube Music, drawn by MyGo itself in
// Material 3 Expressive.
//
// It browses the public catalogue with the youtube package, and the
// account's library once the user signs in with their YouTube Music session.
// Playback resolves a stream URL and decodes it in a child ffmpeg; see
// package player. The interface is built from the components of package m3,
// which take their colours, shapes and motion from one theme.
package main

import (
	"context"
	"log"
	"math/rand/v2"
	"path/filepath"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
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
	// its cookie once the user signs in. The store keeps the sign-in across
	// restarts.
	public *youtube.Client
	authed *youtube.Client
	// newClient makes the client that carries an account's cookie; tests
	// replace it with one that needs no network.
	newClient func(*youtube.CookieAuth) *youtube.Client
	store     cookieStore
	account   youtube.AccountDetails
	signedIn  bool
	signIn    signInState
	// signInAttempt invalidates work when a sign-in dialog is dismissed.
	signInAttempt atomic.Uint64
	// credentialMu serializes restore, save and delete operations so a late
	// restore or cancelled sign-in cannot resurrect a removed session.
	credentialMu       sync.Mutex
	savedSignInAttempt uint64 // guarded by credentialMu
	// credentialDeletePending prevents a new sign-in from racing a sign-out
	// deletion and having its freshly saved session deleted afterward.
	credentialDeletePending bool
	// accountGen numbers the changes of account, so that the answer to a
	// question asked before a sign-in or sign-out is not taken as its result.
	accountGen int
	// notice is a message for the next frame to show as a toast.
	notice string

	// What the user chose, and where it is kept; empty keeps nothing.
	settings     settings
	settingsPath string
	saver        settingsWriter
	// The theme being shown. A change of colours glides from one scheme to
	// the next rather than cutting.
	shown     *m3.Theme
	themeTo   *m3.Theme
	themeFrom m3.Scheme
	themeAt   time.Time
	// dynamic is the seed taken from the artwork playing.
	dynamic   ui.Color
	dynamicOK bool

	// The page shown, and what it holds.
	location    string
	feed        pageState
	search      searchState
	focusSearch bool
	rows        []row
	playable    []youtube.MusicItem
	list        ui.ListState
	// detail is the heading of the album, playlist or artist page shown,
	// and details remembers one for each page the user opened, so going
	// back to one finds its heading again.
	detail  detail
	details map[string]detail
	// carousels keep the scroll of each shelf.
	carousels map[string]*m3.CarouselState
	menuOpen  bool

	// The full-screen player, and what its side panel shows.
	npOpen      bool
	npTab       int
	npQueueOnly bool
	queueList   ui.ListState
	queueFollow string
	volumeHeld  bool
	lyrics      lyricsState
	related     relatedState
	relatedList ui.ListState

	// Playback.
	current youtube.MusicItem
	queue   []youtube.MusicItem
	index   int
	// recommendationStart marks the first generated track in queue; tracks
	// before it came from the user's selected page or playlist.
	recommendationStart int
	queueSource         string
	upNextOptions       youtube.UpNextOptions
	upNextGeneration    int
	upNextLoading       bool
	upNextFetched       bool
	upNextErr           string
	upNextToken         string
	upNextSeen          map[string]struct{}
	waitingForAuto      bool
	total               time.Duration
	scrub               float64
	scrubbing           bool
	volume              float64
	muted               float64 // the volume to return to, while muted
	shuffle             bool
	repeat              repeatMode
	playErr             string
	// resolving is set from the moment a track is chosen until its audio URL
	// is found, and streamGen numbers those requests, so that only the latest
	// one plays when it lands.
	resolving bool
	streamGen int
	// opening is the album or playlist whose first song is being looked up
	// to play, so that a second press does not look it up again.
	opening string

	// job numbers page loads, so a slow one for a page the user has left
	// is dropped when it lands.
	job    int
	cancel context.CancelFunc
}

// repeatMode is what happens when a track, or the queue, ends.
type repeatMode int

const (
	repeatOff   repeatMode = iota // play on, and stop at the end of the queue
	repeatQueue                   // go back to the first track after the last
	repeatTrack                   // play the same track again
)

// detail remembers what the page being opened is, for its heading.
type detail struct {
	title    string
	subtitle string
	art      string
	kind     string
}

// relatedState is the related songs tab for the track playing.
type relatedState struct {
	videoID string
	loading bool
	items   []youtube.MusicItem
	err     string
}

func newApp() *app {
	var playerOptions []player.Option
	if ffmpeg, err := toolPath("ffmpeg"); err == nil {
		playerOptions = append(playerOptions, player.WithFFmpeg(ffmpeg))
	}
	a := &app{
		router:              ui.NewRouter("/home"),
		player:              player.New(playerOptions...),
		settings:            defaultSettings(),
		search:              searchState{},
		details:             make(map[string]detail),
		carousels:           make(map[string]*m3.CarouselState),
		recommendationStart: -1,
	}
	a.volume = a.settings.Volume
	a.run = func(work func()) { go work() }
	a.newClient = func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{CookieAuth: auth})
	}
	a.thumbs = newThumbCache(a.refresh)
	a.player.SetVolume(a.volume / 100)
	return a
}

// lastReclaim is when reclaimMemory last ran, in Unix nanoseconds.
var lastReclaim atomic.Int64

// reclaimMemory gives the runtime's free memory back to the system. Reading a
// page, or YouTube's player script, builds far more garbage than it keeps, and
// the runtime would sit on that memory for a while after. It is for the app
// to ask, not the youtube package, and no more than every few seconds, for a
// run of loads costs one collection.
func reclaimMemory() {
	const every = 5 * time.Second
	now := time.Now().UnixNano()
	last := lastReclaim.Load()
	if now-last < int64(every) || !lastReclaim.CompareAndSwap(last, now) {
		return
	}
	debug.FreeOSMemory()
}

func main() {
	// The heap is mostly artwork, which lives as long as the user browses and
	// then goes at once. The default lets the heap grow to twice what is live
	// before a collection; half as much costs a little time and saves tens of
	// megabytes.
	debug.SetGCPercent(50)
	a := newApp()
	mygo.App.WhenReady(func() {
		a.setup()
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:         "Meiro",
			Width:         1180,
			Height:        760,
			MinWidth:      860,
			MinHeight:     560,
			StateKey:      "meiro",
			TitleBarStyle: mygo.TitleBarHidden,
			Content:       ui.View(a.view),
		})
		// Background work reaches the window through a.win, so it starts only
		// once the window is there.
		a.restoreAccount()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// setup prepares the account and the settings: the token store, the two
// clients, and the sign-in an earlier run left behind.
func (a *app) setup() {
	if directory, err := mygo.App.Path(mygo.PathUserData); err == nil {
		a.settingsPath = filepath.Join(directory, "settings.json")
		a.settings = loadSettings(a.settingsPath)
		a.volume = a.settings.Volume
		a.player.SetVolume(a.volume / 100)
	}
	a.public = youtube.NewClient(youtube.Options{})
	store, err := newCookieStore()
	if err != nil {
		a.signIn.err = err.Error()
		return
	}
	a.store = store
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		store.forgetLegacy(ctx)
	})
}

// saveSettings keeps the settings, off the main thread.
func (a *app) saveSettings() {
	if a.settingsPath == "" {
		return
	}
	a.saver.save(&a.settings, a.settingsPath)
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

// resolveTheme returns the theme to draw this frame with: the one the
// settings describe, glided to from the one before when it changed.
func (a *app) resolveTheme(c *ui.Context) *m3.Theme {
	cfg := a.settings.config()
	if a.settings.Dynamic && a.dynamicOK {
		cfg.Seed = a.dynamic
	}
	target := m3.New(cfg, c.Theme().Dark)
	if a.themeTo == nil {
		a.themeTo, a.shown = target, target
		a.themeFrom = target.Scheme
		return target
	}
	if target.Config != a.themeTo.Config || target.Dark != a.themeTo.Dark {
		a.themeFrom = a.shown.Scheme
		a.themeTo, a.themeAt = target, c.Now()
		if c.Preferences().ReduceMotion {
			a.themeAt = time.Time{} // no glide: cut to the new colours
		}
	}
	const glide = 420 * time.Millisecond
	k := float32(c.Now().Sub(a.themeAt)) / float32(glide)
	if k >= 1 || a.themeAt.IsZero() {
		a.shown = a.themeTo
		return a.shown
	}
	shown := *a.themeTo
	shown.Scheme = a.themeFrom.Mix(a.themeTo.Scheme, 1-(1-k)*(1-k)*(1-k))
	a.shown = &shown
	c.AnimationFrame()
	return a.shown
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
		a.trackEnded()
		return
	}
	if a.player.Playing() {
		a.ensureUpNext()
		if !a.scrubbing {
			a.scrub = a.player.Position().Seconds()
		}
		c.After(250 * time.Millisecond)
	}
}

// trackEnded moves on when a track finishes: it repeats, or plays the next
// one, or stops at the end of the queue.
func (a *app) trackEnded() {
	if a.repeat == repeatTrack {
		a.start(a.current)
		return
	}
	a.advance()
}

// nextIndex returns the place of the track that follows the current one in
// the queue, and false when the queue is over.
func (a *app) nextIndex() (int, bool) {
	limit := len(a.queue)
	if !a.settings.AutoPlay && a.recommendationStart >= 0 {
		limit = a.recommendationStart
	}
	switch {
	case limit == 0 || (!a.settings.AutoPlay && a.recommendationStart >= 0 && a.index >= limit):
		return 0, false
	case a.shuffle && limit > 1:
		// Any track but the current one: draw from the others, and step over
		// the current one's place.
		i := rand.IntN(limit - 1)
		if i >= a.index && a.index >= 0 {
			i++
		}
		return i, true
	case a.index+1 < limit:
		return a.index + 1, true
	case a.repeat == repeatQueue:
		return 0, true
	}
	return 0, false
}
