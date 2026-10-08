package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// searches counts the search requests the fake YouTube has answered.
var searches atomic.Int32

// fakeMusic answers the InnerTube endpoints with canned responses, so the
// app's tests need no network.
type fakeMusic struct{}

func (fakeMusic) RoundTrip(request *http.Request) (*http.Response, error) {
	asked := ""
	if request.Body != nil {
		body, _ := io.ReadAll(request.Body)
		asked = string(body)
	}
	reply := homeResponse
	switch {
	case strings.Contains(request.URL.Path, "/iframe_api"):
		reply = `var player = "https://www.youtube.com/s/player/abc123/player_es6.vflset/en_US/base.js"`
	case strings.Contains(request.URL.Path, "/s/player/"):
		reply = `var a = 1; signatureTimestamp: 12345, b = 2;`
	case strings.Contains(asked, "FEmusic_explore"):
		reply = exploreResponse
	case strings.Contains(asked, "VLPL_video"):
		reply = playlistResponse
	case strings.Contains(asked, "MPREb_test"):
		reply = albumResponse
	case strings.Contains(asked, `"query"`):
		searches.Add(1)
		reply = searchResponse
	case strings.Contains(asked, `"videoId"`):
		reply = playerResponse
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(reply)),
		Request:    request,
	}, nil
}

// newTestApp builds an app whose loads run inline and whose YouTube requests
// are answered by fakeMusic.
func newTestApp() *app {
	a := newApp()
	a.run = func(work func()) { work() }
	client := youtube.NewClient(youtube.Options{
		APIKey:     "test",
		HTTPClient: &http.Client{Transport: fakeMusic{}},
	})
	a.public, a.authed = client, client
	return a
}

func TestHomeListsSections(t *testing.T) {
	a := newTestApp()
	tt := ui.NewTester(a.view, 1000, 700)
	for _, text := range []string{"Home", "Search", "Explore", "Library", "Settings", "Quick picks", "Ambient One"} {
		if !tt.HasText(text) {
			t.Fatalf("the home page is missing %q: %q", text, tt.Texts())
		}
	}
	if _, ok := tt.Find("Account"); !ok {
		t.Errorf("the account button is missing: %q", tt.Texts())
	}
}

func TestOpeningAnAlbumFromTheHomePage(t *testing.T) {
	a := newTestApp()
	tt := ui.NewTester(a.view, 1000, 700)
	if err := tt.Click("Deep Focus"); err != nil {
		t.Fatal(err)
	}
	if a.router.Path() != "/album/MPREb_test" {
		t.Fatalf("clicking an album went to %q", a.router.Path())
	}
	if a.detail.title != "Deep Focus" || a.detail.kind != pageAlbum {
		t.Errorf("the album page heading is %+v", a.detail)
	}
	if !tt.HasText("Album Track One") || !tt.HasText("ALBUM") {
		t.Fatalf("the album page did not list its tracks: %q", tt.Texts())
	}
	if len(a.playable) != 1 || a.playable[0].VideoID != "vid-3" {
		t.Errorf("the album's play queue is %v", a.playable)
	}
	if a.playable[0].Duration != "4:12" {
		t.Errorf("the track length is %q", a.playable[0].Duration)
	}
	if err := tt.Click("Back"); err != nil {
		t.Fatal(err)
	}
	if a.router.Path() != "/home" {
		t.Errorf("going back led to %q", a.router.Path())
	}
}

func TestPlaylistListsAndQueuesVideoEntries(t *testing.T) {
	a := newTestApp()
	path := "/playlist/VLPL_video"
	a.details[path] = detail{title: "Video playlist", kind: pagePlaylist}
	a.router.Push(path)
	tt := ui.NewTester(a.view, 1000, 700)

	if !tt.HasText("Playlist video") {
		t.Fatalf("the playlist did not list its video: %q", tt.Texts())
	}
	if len(a.playable) != 1 || a.playable[0].VideoID != "playlist-video" || !isVideo(a.playable[0]) {
		t.Errorf("playlist queue = %#v, want its video entry", a.playable)
	}
}

// Typing in the search field must not search: only Enter does.
func TestSearchRunsOnSubmitOnly(t *testing.T) {
	a := newTestApp()
	a.router.Push("/search")
	tt := ui.NewTester(a.view, 1000, 700)
	searches.Store(0)
	if err := tt.Click("Search songs, albums, artists"); err != nil {
		t.Fatal(err)
	}
	tt.Type("ambient")
	time.Sleep(50 * time.Millisecond)
	tt.Frame()
	if n := searches.Load(); n != 0 || tt.HasText("Search Result Song") {
		t.Fatalf("typing ran %d searches: %q", n, tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	if n := searches.Load(); n != 1 {
		t.Fatalf("Enter ran %d searches, want 1", n)
	}
	if !tt.HasText("Search Result Song") {
		t.Fatalf("the search results are missing: %q", tt.Texts())
	}
	if len(a.settings.Recent) != 1 || a.settings.Recent[0] != "ambient" {
		t.Errorf("the search was not remembered: %v", a.settings.Recent)
	}
	// Picking another filter searches again for what was submitted, not for
	// whatever is half typed.
	tt.Type("zzz")
	if err := tt.Click("Songs"); err != nil {
		t.Fatal(err)
	}
	if n := searches.Load(); n != 2 || a.search.submitted != "ambient" {
		t.Errorf("a filter ran %d searches for %q", n, a.search.submitted)
	}
}

func TestPlayerBarShowsTheCurrentTrack(t *testing.T) {
	a := newTestApp()
	a.current = youtube.MusicItem{VideoID: "vid-1", Title: "Ambient One", Subtitle: "Someone"}
	a.total = 3*time.Minute + 33*time.Second
	tt := ui.NewTester(a.view, 1000, 700)
	if !tt.HasText("Ambient One") || !tt.HasText("3:33") || !tt.HasText("0:00") {
		t.Fatalf("the player bar does not show the track: %q", tt.Texts())
	}
	for _, control := range []string{"Play", "Previous", "Next", "Shuffle", "Repeat", "Position", "Up next"} {
		if _, ok := tt.Find(control); !ok {
			t.Errorf("the %s control is missing: %q", control, tt.Texts())
		}
	}
	if err := tt.Click("Open the player"); err != nil {
		t.Fatal(err)
	}
	if !a.npOpen || !tt.HasText("Now playing") {
		t.Errorf("the full-screen player did not open: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	if a.npOpen {
		t.Errorf("Escape did not close the full-screen player")
	}
}

func TestVideoPlaybackShowsBadgeOnPlayerArtwork(t *testing.T) {
	a := newTestApp()
	a.current = youtube.MusicItem{VideoID: "video-1", Title: "Video track", Kind: "video"}
	tt := ui.NewTester(a.view, 1000, 700)
	if _, ok := tt.Find("Video"); !ok || !tt.HasText("VIDEO") {
		t.Fatalf("video playback has no badge: %q", tt.Texts())
	}
	a.current.Kind = "track"
	tt.Frame()
	if _, ok := tt.Find("Video"); ok {
		t.Errorf("audio-only track retained the video badge: %q", tt.Texts())
	}
}

func TestUpNextShowsAutoplayStateAndRecommendationSection(t *testing.T) {
	a := newTestApp()
	a.current = youtube.MusicItem{VideoID: "current", Title: "Current song"}
	a.queue = []youtube.MusicItem{
		{VideoID: "current", Title: "Current song"},
		{VideoID: "chosen-next", Title: "Chosen next"},
		{VideoID: "recommended", Title: "Recommended song"},
	}
	a.index, a.recommendationStart, a.queueSource = 1, 2, "My playlist"
	a.location, a.npOpen = "/home", true
	tt := ui.NewTester(a.view, 1180, 760)
	for _, label := range []string{"Playing from", "My playlist", "Auto-play", "Add similar music when this queue ends.", "Recommended", "Recommended song"} {
		if !tt.HasText(label) {
			t.Errorf("Up next panel is missing %q: %q", label, tt.Texts())
		}
	}
	if err := tt.Click("Toggle auto-play"); err != nil {
		t.Fatal(err)
	}
	if a.settings.AutoPlay {
		t.Fatal("the Auto-play switch did not turn autoplay off")
	}
	if _, ok := a.nextIndex(); ok {
		t.Errorf("turning autoplay off left a generated recommendation playable")
	}
}

func TestRelatedTabShowsRelatedTracks(t *testing.T) {
	a := newTestApp()
	a.current = youtube.MusicItem{VideoID: "current", Title: "Current song"}
	a.related = relatedState{
		videoID: "current",
		items:   []youtube.MusicItem{{VideoID: "related", Title: "Related song", Kind: "track"}},
	}
	a.location, a.npOpen, a.npTab = "/home", true, 2
	tt := ui.NewTester(a.view, 1180, 760)
	if !tt.HasText("Related") || !tt.HasText("Related song") {
		t.Fatalf("related tab did not show its tracks: %q", tt.Texts())
	}
}

func TestShuffleAndRepeatChooseTheNextTrack(t *testing.T) {
	a := newTestApp()
	a.queue = []youtube.MusicItem{{VideoID: "a"}, {VideoID: "b"}, {VideoID: "c"}}
	if i, ok := a.nextIndex(); !ok || i != 1 {
		t.Errorf("next = %d, %v", i, ok)
	}
	a.index = 2
	if _, ok := a.nextIndex(); ok {
		t.Errorf("the queue should end after its last track")
	}
	a.repeat = 1
	if i, ok := a.nextIndex(); !ok || i != 0 {
		t.Errorf("repeating the queue went to %d, %v", i, ok)
	}
	a.shuffle = true
	for range 50 {
		if i, ok := a.nextIndex(); !ok || i == a.index {
			t.Fatalf("shuffle chose %d, %v for the current track", i, ok)
		}
	}
}

// Every page draws, in both appearances, with and without a track playing.
func TestEveryPageDraws(t *testing.T) {
	for _, path := range []string{"/home", "/explore", "/library", "/search", "/settings", "/album/MPREb_test", "/nowhere"} {
		for _, dark := range []bool{false, true} {
			t.Run(path, func(t *testing.T) {
				a := newTestApp()
				a.router.Push(path)
				tt := ui.NewTester(a.view, 1000, 700)
				tt.SetDark(dark)
				a.current = youtube.MusicItem{VideoID: "vid-1", Title: "Ambient One"}
				a.npOpen = path == "/home"
				tt.Frame()
				if len(tt.Texts()) == 0 {
					t.Fatalf("%s drew nothing", path)
				}
			})
		}
	}
}

// The settings restyle the window: a new seed, palette style or mode changes
// the colours every component draws with.
func TestSettingsChangeTheTheme(t *testing.T) {
	a := newTestApp()
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 900)
	before := m3.Active().Scheme.Primary
	if err := tt.Click("Rose"); err != nil {
		t.Fatal(err)
	}
	if a.settings.Seed != "#d81b78" {
		t.Fatalf("clicking Rose chose %q", a.settings.Seed)
	}
	tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	tt.Frame()
	if after := m3.Active().Scheme.Primary; after == before {
		t.Errorf("the primary colour stayed %v after choosing a new seed", after)
	}
	if err := tt.Click("Dark"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !m3.Active().Dark || a.settings.Mode != "dark" {
		t.Errorf("choosing Dark left dark=%v, mode=%q", m3.Active().Dark, a.settings.Mode)
	}
	if err := tt.Click("Vibrant"); err != nil {
		t.Fatal(err)
	}
	if a.settings.Style != int(m3.Vibrant) {
		t.Errorf("choosing Vibrant left style %d", a.settings.Style)
	}
}

func TestSettingsPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "settings.json")
	s := defaultSettings()
	s.Seed, s.Mode, s.Style, s.Dynamic, s.AutoPlay = "#00897b", "dark", int(m3.Expressive), true, false
	s.remember("one")
	s.remember("two")
	s.remember("One")
	if err := s.save(path); err != nil {
		t.Fatal(err)
	}
	got := loadSettings(path)
	if got.Seed != s.Seed || got.Mode != "dark" || got.Style != int(m3.Expressive) || !got.Dynamic || got.AutoPlay {
		t.Errorf("settings came back as %+v", got)
	}
	if len(got.Recent) != 2 || got.Recent[0] != "One" {
		t.Errorf("recent searches came back as %v", got.Recent)
	}
	cfg := got.config()
	if cfg.Mode != m3.Dark || cfg.Style != m3.Expressive {
		t.Errorf("config = %+v", cfg)
	}
	if bad := loadSettings(filepath.Join(t.TempDir(), "missing.json")); bad.Seed != defaultSettings().Seed {
		t.Errorf("a missing file gave %+v", bad)
	}
	if err := os.WriteFile(path, []byte("{nonsense"), 0o600); err != nil {
		t.Fatal(err)
	}
	if bad := loadSettings(path); bad.Seed != defaultSettings().Seed {
		t.Errorf("a broken file gave %+v", bad)
	}
}

func TestOlderSettingsDefaultAutoplayToOn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"seed":"#6750a4","mode":"system","volume":70}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadSettings(path); !got.AutoPlay {
		t.Errorf("older settings disabled autoplay: %+v", got)
	}
}

func TestArtworkColour(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 40, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 40; x++ {
			img.Set(x, y, color.RGBA{R: 30, G: 60, B: 200, A: 255})
		}
	}
	for y := 0; y < 6; y++ {
		for x := 0; x < 6; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	got, ok := dominantColour(decoded(t, data.Bytes()))
	if !ok || got.B < 150 || got.R > 80 {
		t.Errorf("dominantColour = %v, %v; want the blue", got, ok)
	}
	grey := image.NewRGBA(image.Rect(0, 0, 8, 8))
	data.Reset()
	png.Encode(&data, grey)
	if _, ok := dominantColour(decoded(t, data.Bytes())); ok {
		t.Errorf("a grey picture should have no dominant colour")
	}
}

// decoded reads a picture the way the thumbnail cache does.
func decoded(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestTargetOfRoutesItemsToPages(t *testing.T) {
	cases := []struct {
		name string
		item youtube.MusicItem
		kind string
		id   string
	}{
		{"song", youtube.MusicItem{VideoID: "abc"}, pageTrack, "abc"},
		{"video", youtube.MusicItem{VideoID: "video-1", Kind: "video"}, pageTrack, "video-1"},
		{"album", youtube.MusicItem{BrowseID: "MPREb_1"}, pageAlbum, "MPREb_1"},
		{"artist", youtube.MusicItem{BrowseID: "UCabc"}, pageArtist, "UCabc"},
		{"playlist", youtube.MusicItem{BrowseID: "VLPLabc"}, pagePlaylist, "VLPLabc"},
		{"library playlist", youtube.MusicItem{PlaylistID: "PLabc"}, pagePlaylist, "PLabc"},
		{"library album", youtube.MusicItem{BrowseID: "FEmusic_library_privately_owned_release1"}, pageAlbum, "FEmusic_library_privately_owned_release1"},
		{"nothing", youtube.MusicItem{Title: "empty"}, "", ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			kind, id := targetOf(test.item)
			if kind != test.kind || id != test.id {
				t.Errorf("targetOf = (%q, %q), want (%q, %q)", kind, id, test.kind, test.id)
			}
		})
	}
}

func TestClockAndDuration(t *testing.T) {
	if got := clock(3*time.Minute + 42*time.Second); got != "3:42" {
		t.Errorf("clock = %q", got)
	}
	if got := clock(time.Hour + 2*time.Minute + 3*time.Second); got != "1:02:03" {
		t.Errorf("clock = %q", got)
	}
	if got := parseDuration("3:42"); got != 3*time.Minute+42*time.Second {
		t.Errorf("parseDuration = %v", got)
	}
	if got := parseDuration("1:02:03"); got != time.Hour+2*time.Minute+3*time.Second {
		t.Errorf("parseDuration = %v", got)
	}
	if got := parseDuration("2023"); got != 0 {
		t.Errorf("parseDuration of a year = %v", got)
	}
}

func TestThumbnailURLAsksForASmallerPicture(t *testing.T) {
	if got := thumbnailURL("https://img.test/a=w544-h544-l90-rj", 96); got != "https://img.test/a=w96-h96-l90-rj" {
		t.Errorf("thumbnailURL = %q", got)
	}
	if got := thumbnailURL("https://img.test/a=s96-c-k-c0", 64); got != "https://img.test/a=s64-c-k-c0" {
		t.Errorf("thumbnailURL = %q", got)
	}
	if got := thumbnailURL("https://img.test/a", 64); got != "https://img.test/a" {
		t.Errorf("thumbnailURL = %q", got)
	}
}

const homeResponse = `{"contents":{"sectionListRenderer":{"contents":[
	{"musicCarouselShelfRenderer":{
		"header":{"musicCarouselShelfBasicHeaderRenderer":{"title":{"runs":[{"text":"Quick picks"}]}}},
		"contents":[
			{"musicTwoRowItemRenderer":{
				"title":{"runs":[{"text":"Ambient One"}]},
				"subtitle":{"runs":[{"text":"Someone"}]},
				"thumbnailRenderer":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.test/a=w544-h544-l90-rj"}]}}},
				"navigationEndpoint":{"watchEndpoint":{"videoId":"vid-1"}}
			}},
			{"musicTwoRowItemRenderer":{
				"title":{"runs":[{"text":"Deep Focus"}]},
				"subtitle":{"runs":[{"text":"Album"}]},
				"thumbnailRenderer":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.test/b=w544-h544-l90-rj"}]}}},
				"navigationEndpoint":{"browseEndpoint":{"browseId":"MPREb_test"}}
			}}
		]
	}}
]}}}`

const exploreResponse = `{"contents":{"sectionListRenderer":{"contents":[
	{"musicCarouselShelfRenderer":{
		"header":{"musicCarouselShelfBasicHeaderRenderer":{"title":{"runs":[{"text":"Moods"}]}}},
		"contents":[
			{"musicTwoRowItemRenderer":{
				"title":{"runs":[{"text":"Night Drive"}]},
				"navigationEndpoint":{"watchEndpoint":{"videoId":"vid-2"}}
			}}
		]
	}}
]}}}`

const albumResponse = `{"contents":{"musicShelfRenderer":{"contents":[
	{"musicResponsiveListItemRenderer":{
		"playlistItemData":{"videoId":"vid-3"},
		"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Album Track One"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone"},{"text":" • "},{"text":"4:12"}]}}}
		]
	}}
]}}}`

const playlistResponse = `{"contents":{"playlistVideoListRenderer":{"contents":[
	{"playlistVideoRenderer":{"videoId":"playlist-video","title":{"simpleText":"Playlist video"},"lengthText":{"simpleText":"5:21"}}}
]}}}`

const searchResponse = `{"contents":{"musicShelfRenderer":{"contents":[
	{"musicResponsiveListItemRenderer":{
		"playlistItemData":{"videoId":"vid-4"},
		"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Search Result Song"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone"},{"text":" • "},{"text":"2:20"}]}}}
		]
	}}
]}}}`

const playerResponse = `{
	"videoDetails":{"videoId":"vid-1","title":"Ambient One","author":"Someone","lengthSeconds":"213"},
	"streamingData":{"adaptiveFormats":[{"itag":251,"mimeType":"audio/webm; codecs=\"opus\"","bitrate":136544,"url":"https://media.test/audio"}]},
	"playabilityStatus":{"status":"OK"}
}`

func TestCookieHeader(t *testing.T) {
	const want = "SAPISID=abc; SID=def"
	for name, in := range map[string]string{
		"value":        want,
		"padded":       "  " + want + "\n",
		"header":       "Cookie: " + want,
		"lowercase":    "cookie: " + want,
		"curl":         "curl 'https://music.youtube.com/youtubei/v1/browse' -H 'cookie: " + want + "' -H 'origin: x'",
		"curl doubled": `curl "https://music.youtube.com" -H "cookie: ` + want + `" --compressed`,
	} {
		if got := cookieHeader(in); got != want {
			t.Errorf("%s: cookieHeader = %q, want %q", name, got, want)
		}
	}
}

func TestCookieFromNetscape(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	text := strings.Join([]string{
		"# Netscape HTTP Cookie File",
		"",
		".youtube.com\tTRUE\t/\tTRUE\t2100000000\tSAPISID\tabc",
		"#HttpOnly_.youtube.com\tTRUE\t/\tTRUE\t2100000000\t__Secure-3PSID\tdef",
		"music.youtube.com\tFALSE\t/\tTRUE\t0\tYSC\tsession",
		".youtube.com\tTRUE\t/\tTRUE\t1000000000\tOLD\texpired",
		".google.com\tTRUE\t/\tTRUE\t2100000000\tSID\tgoogle",
		".notyoutube.com\tTRUE\t/\tTRUE\t2100000000\tEVIL\tx",
		".youtube.com\tTRUE\t/\tTRUE\t2100000000\tSAPISID\tnewer",
		"broken line",
	}, "\n")
	got := cookieFromNetscape(text, now)
	want := "SAPISID=newer; __Secure-3PSID=def; YSC=session"
	if got != want {
		t.Errorf("cookieFromNetscape = %q, want %q", got, want)
	}
	if got := cookieFromNetscape("# nothing\n", now); got != "" {
		t.Errorf("an empty file gave %q", got)
	}
}

func TestChromiumProfiles(t *testing.T) {
	dir := t.TempDir()
	for _, database := range []string{"Default/Cookies", "Profile 3/Network/Cookies", "Profile 1/Preferences", "Crashpad/Cookies"} {
		path := filepath.Join(dir, filepath.FromSlash(database))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := chromiumProfiles(dir)
	want := []string{filepath.Join(dir, "Default"), filepath.Join(dir, "Profile 3")}
	if !slices.Equal(got, want) {
		t.Errorf("chromiumProfiles = %q, want %q", got, want)
	}
	if got := chromiumProfiles(filepath.Join(dir, "missing")); got != nil {
		t.Errorf("a missing directory gave %q", got)
	}
}

func TestSignInDialogOffersBrowsersInADropdown(t *testing.T) {
	a := newTestApp()
	tt := ui.NewTester(a.view, 1000, 700)
	a.signInWithGoogle()
	tt.Frame()
	if !tt.HasText("Import from a browser") {
		t.Fatalf("the sign-in dialog has no import button: %q", tt.Texts())
	}
	if tt.HasText("Firefox") {
		t.Errorf("the browsers are listed before the dropdown opens: %q", tt.Texts())
	}
	if err := tt.Click("Import from a browser"); err != nil {
		t.Fatal(err)
	}
	for _, browser := range []string{"Chrome", "Safari", "Firefox", "Brave", "Edge"} {
		if !tt.HasText(browser) {
			t.Errorf("the dropdown is missing %s: %q", browser, tt.Texts())
		}
	}
}

func TestPlayingShowsLoadingAndIgnoresASecondPress(t *testing.T) {
	a := newTestApp()
	a.run = func(work func()) {} // the audio never resolves
	item := youtube.MusicItem{VideoID: "vid-1", Title: "Ambient One", Duration: "3:33"}

	a.play(item, []youtube.MusicItem{item}, 0)
	if !a.loading() || a.streamGen != 1 {
		t.Fatalf("playing did not start loading: loading=%v, gen=%d", a.loading(), a.streamGen)
	}
	tt := ui.NewTester(a.view, 1000, 700)
	if _, ok := tt.Find("Loading"); !ok {
		t.Errorf("the play button does not show it is loading: %q", tt.Texts())
	}

	// Pressing play again, in a row or on the button, must not ask twice.
	a.play(item, []youtube.MusicItem{item}, 0)
	a.togglePlay()
	if a.streamGen != 1 {
		t.Errorf("a second press started another request: gen=%d", a.streamGen)
	}

	// Choosing another track supersedes the first one.
	other := youtube.MusicItem{VideoID: "vid-2", Title: "Ambient Two"}
	a.play(other, []youtube.MusicItem{item, other}, 1)
	if a.streamGen != 2 || !a.loading() {
		t.Errorf("the second track did not start: gen=%d, loading=%v", a.streamGen, a.loading())
	}
}

// The queue is the tracks the user chose to play from, not the rows of
// whatever page is drawn next.
func TestQueueSurvivesTheNextPageOfRows(t *testing.T) {
	a := newTestApp()
	a.run = func(work func()) {}
	song := func(id string) youtube.MusicItem { return youtube.MusicItem{VideoID: id, ID: id, Title: id} }
	a.router.Push("/search")
	a.search.submitted = "x"
	a.search.items = []youtube.MusicItem{song("A"), song("B"), song("C")}
	a.setRows()
	a.play(a.playable[0], a.playable, 0)

	a.search.items = []youtube.MusicItem{song("X"), song("Y"), song("Z")}
	a.setRows()
	if got := a.queue[0].VideoID + a.queue[1].VideoID + a.queue[2].VideoID; got != "ABC" {
		t.Errorf("the queue became %q after the next page was drawn", got)
	}
}

func TestAutoplayAppendsAndContinuesWithoutDuplicatingTheSelectedQueue(t *testing.T) {
	var nextRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/youtubei/v1/next" {
			t.Errorf("request path = %q, want /next", r.URL.Path)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		switch nextRequests.Add(1) {
		case 1:
			if request["videoId"] != "seed" || request["playlistId"] != "PL123" || request["playlistIndex"] != float64(1) {
				t.Errorf("initial up-next request = %#v", request)
			}
			_, _ = w.Write([]byte(`{"contents":{"playlistPanelRenderer":{"playlistId":"RDAMVMseed","contents":[{"playlistPanelVideoRenderer":{"videoId":"playlist-next","title":{"simpleText":"Playlist next"}}},{"playlistPanelVideoRenderer":{"videoId":"radio-1","title":{"simpleText":"Radio one"}}}],"continuations":[{"nextRadioContinuationData":{"continuation":"RADIO_MORE"}}]}}}`))
		case 2:
			if request["videoId"] != "seed" || request["playlistId"] != "RDAMVMseed" || request["playlistIndex"] != nil || request["continuation"] != "RADIO_MORE" {
				t.Errorf("radio continuation request = %#v", request)
			}
			_, _ = w.Write([]byte(`{"continuationContents":{"playlistPanelContinuation":{"playlistId":"RDAMVMseed","contents":[{"playlistPanelVideoRenderer":{"videoId":"radio-1","title":{"simpleText":"Radio one"}}},{"playlistPanelVideoRenderer":{"videoId":"radio-2","title":{"simpleText":"Radio two"}}}],"continuations":[{"nextRadioContinuationData":{"continuation":"RADIO_END"}}]}}}`))
		default:
			t.Errorf("unexpected /next request #%d", nextRequests.Load())
		}
	}))
	defer server.Close()
	client := youtube.NewClient(youtube.Options{BaseURL: server.URL, APIKey: "test"})
	a := newTestApp()
	a.public, a.authed = client, client
	a.run = func(work func()) { work() }
	index := 1
	a.current = youtube.MusicItem{VideoID: "seed", Title: "Seed"}
	a.queue = []youtube.MusicItem{{VideoID: "seed"}, {VideoID: "playlist-next"}}
	a.index = 0
	a.resetUpNext(youtube.UpNextOptions{VideoID: "seed", PlaylistID: "PL123", PlaylistIndex: &index}, "Playlist")

	a.ensureUpNext()
	if len(a.queue) != 3 || a.recommendationStart != 2 || a.queue[2].VideoID != "radio-1" || a.upNextToken != "RADIO_MORE" {
		t.Fatalf("initial up-next queue = %#v, recommendation start %d, token %q", a.queue, a.recommendationStart, a.upNextToken)
	}
	if a.queueSource != "Playlist" || a.upNextOptions.PlaylistID != "RDAMVMseed" || a.upNextOptions.PlaylistIndex != nil {
		t.Errorf("resolved queue context = source %q, options %+v", a.queueSource, a.upNextOptions)
	}

	a.index = 1 // two tracks remain, so prefetch the radio continuation.
	a.ensureUpNext()
	if len(a.queue) != 4 || a.queue[3].VideoID != "radio-2" || a.upNextToken != "RADIO_END" || nextRequests.Load() != 2 {
		t.Fatalf("continued queue = %#v with token %q after %d requests", a.queue, a.upNextToken, nextRequests.Load())
	}
	a.setAutoPlay(false)
	if _, ok := a.nextIndex(); ok {
		t.Errorf("autoplay-off advanced into generated recommendations")
	}
}

func TestQueueEndWaitsForInFlightRecommendation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"contents":{"playlistPanelRenderer":{"contents":[{"playlistPanelVideoRenderer":{"videoId":"radio-1","title":{"simpleText":"Radio one"}}}]}}}`))
	}))
	defer server.Close()
	client := youtube.NewClient(youtube.Options{BaseURL: server.URL, APIKey: "test"})
	a := newTestApp()
	a.public, a.authed = client, client
	var pending func()
	a.run = func(work func()) { pending = work }
	current := youtube.MusicItem{VideoID: "seed", Title: "Seed"}
	a.current, a.queue = current, []youtube.MusicItem{current}
	a.resetUpNext(youtube.UpNextOptions{VideoID: current.VideoID}, "")
	a.ensureUpNext()
	if pending == nil || !a.upNextLoading {
		t.Fatal("the initial recommendation request did not start")
	}
	a.advance()
	if !a.waitingForAuto {
		t.Fatal("playback did not wait for the in-flight recommendation request")
	}
	pending()
	if a.current.VideoID != "radio-1" || a.index != 1 || a.waitingForAuto {
		t.Errorf("queue end left current=%q, index=%d, waiting=%v", a.current.VideoID, a.index, a.waitingForAuto)
	}
}

func TestUpNextFailureKeepsTheSelectedQueue(t *testing.T) {
	client := youtube.NewClient(youtube.Options{
		APIKey:     "test",
		HTTPClient: &http.Client{Transport: failingMusic{}},
	})
	a := newTestApp()
	a.public, a.authed = client, client
	a.run = func(work func()) { work() }
	a.current = youtube.MusicItem{VideoID: "seed", Title: "Seed"}
	a.queue = []youtube.MusicItem{{VideoID: "seed"}, {VideoID: "chosen", Title: "Chosen next"}}
	a.resetUpNext(youtube.UpNextOptions{VideoID: "seed"}, "")
	a.ensureUpNext()
	if len(a.queue) != 2 || a.queue[1].VideoID != "chosen" || a.upNextErr == "" || a.playErr != "" {
		t.Errorf("failed recommendation request changed playback state: queue=%#v error=%q play error=%q", a.queue, a.upNextErr, a.playErr)
	}
}

// failingMusic answers every request with a server error.
type failingMusic struct{}

func (failingMusic) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusInternalServerError,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("down")),
		Request:    request,
	}, nil
}

// A failed request for more must not throw away the page, and must leave the
// button to ask again.
func TestFailedLoadMoreKeepsThePage(t *testing.T) {
	a := newTestApp()
	client := youtube.NewClient(youtube.Options{APIKey: "test", HTTPClient: &http.Client{Transport: failingMusic{}}})
	a.public, a.authed = client, client
	a.feed.items = []youtube.MusicItem{{VideoID: "a", ID: "a", Title: "Kept song"}}
	a.feed.more = "token"

	a.loadMore()
	if a.feed.err != "" || a.feed.moreErr == "" || a.feed.more != "token" {
		t.Fatalf("after a failed load: err=%q moreErr=%q more=%q", a.feed.err, a.feed.moreErr, a.feed.more)
	}
	a.setRows()
	kinds := ""
	for _, r := range a.rows {
		if r.kind == rowCards {
			kinds += "c"
		}
		if r.kind == rowMore {
			kinds += "m"
		}
		if r.kind == rowError {
			t.Fatalf("the page was replaced by an error: %q", r.title)
		}
	}
	if kinds != "cm" {
		t.Errorf("rows after a failed load more = %q, want the shelf and the button", kinds)
	}
}

// Saves asked for in a burst, while the list they hold is edited, must not
// race, and the last one must be what ends up on disk.
func TestSettingsSavesAreOrderedAndIndependent(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp()
	a.settingsPath = filepath.Join(dir, "settings.json")
	for i := range 200 {
		a.settings.Recent = []string{"a", "b", "c", "d", "e"}
		a.settings.Volume = float64(i % 100)
		a.saveSettings()
		a.forget("a")
		a.settings.remember("last")
	}
	a.settings.Volume = 42
	a.saveSettings()
	a.saver.wait()

	got := loadSettings(a.settingsPath)
	if got.Volume != 42 || len(got.Recent) == 0 || got.Recent[0] != "last" {
		t.Errorf("the last save was not the one kept: volume %v, recent %v", got.Volume, got.Recent)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("saving left %d files behind, want only the settings", len(entries))
	}
}

func TestParseSeedIsStrict(t *testing.T) {
	if c, ok := parseSeed("#6750a4"); !ok || c != ui.RGB(0x67, 0x50, 0xa4) {
		t.Errorf("a good seed gave %v, %v", c, ok)
	}
	for _, bad := range []string{"", "6750a4", "#6750a", "#6750a4f", "#1 2 3 ", "#+1+1+1", "#0x1234", "#gggggg"} {
		if _, ok := parseSeed(bad); ok {
			t.Errorf("%q was taken as a seed", bad)
		}
	}
}

// memoryStore is a cookie store held in memory.
type memoryStore struct {
	cookie    string
	deleteErr error
}

func (m *memoryStore) Load(context.Context) (string, error) {
	if m.cookie == "" {
		return "", errNotSignedIn
	}
	return m.cookie, nil
}

func (m *memoryStore) Save(_ context.Context, cookie string) error { m.cookie = cookie; return nil }

func (m *memoryStore) Delete(context.Context) error { return m.deleteErr }

// The session saved by an earlier run is read in the background. A sign-out
// that happens meanwhile must not be undone when it lands.
func TestSignOutIsNotUndoneByARestoreInFlight(t *testing.T) {
	a := newTestApp()
	a.newClient = func(*youtube.CookieAuth) *youtube.Client { return a.public }
	a.store = &memoryStore{cookie: "SAPISID=abc"}
	a.signedIn, a.authed = false, nil
	var pending []func()
	a.run = func(work func()) { pending = append(pending, work) }

	a.restoreAccount()
	a.signOut()
	for _, work := range pending[:1] {
		work()
	}
	if a.signedIn {
		t.Error("a restore that began before the sign-out signed the user back in")
	}
}

func TestSignOutSaysWhenTheSessionStays(t *testing.T) {
	a := newTestApp()
	a.store = &memoryStore{cookie: "SAPISID=abc", deleteErr: errors.New("the keychain is locked")}
	a.signedIn = true
	a.signOut()
	if a.signedIn {
		t.Error("the user is still signed in")
	}
	if !strings.Contains(a.notice, "keychain is locked") {
		t.Errorf("the failure was not reported: %q", a.notice)
	}
}

func TestDeletingReportsWhatWouldNotGo(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cookie.txt")
	system := &fakeKeychain{usable: true, holds: true, removeErr: errors.New("access denied")}
	store := &keychainStore{system: system, file: newFileStore(path)}
	if err := store.file.Save(ctx, testCookie); err != nil {
		t.Fatal(err)
	}
	err := store.Delete(ctx)
	if err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Errorf("Delete = %v, want the keychain's failure", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("the file was left behind because the keychain failed")
	}
}

func TestCookieHeaderSurvivesNonASCIIText(t *testing.T) {
	// "İ" is two bytes, and three once lowercased.
	if got := cookieHeader("İİİİİİİİİİ Cookie: SAPISID=abc"); got != "SAPISID=abc" {
		t.Errorf("cookieHeader = %q", got)
	}
}

func TestShuffleOfTwoTracksAlwaysPicksTheOther(t *testing.T) {
	a := newTestApp()
	a.shuffle = true
	a.queue = []youtube.MusicItem{{VideoID: "a"}, {VideoID: "b"}}
	for _, index := range []int{0, 1} {
		a.index = index
		for range 50 {
			if i, ok := a.nextIndex(); !ok || i != 1-index {
				t.Fatalf("from %d, shuffle chose %d, %v", index, i, ok)
			}
		}
	}
	// A stale index past the end still yields a track in the queue.
	a.index = 7
	for range 50 {
		if i, ok := a.nextIndex(); !ok || i < 0 || i >= len(a.queue) {
			t.Fatalf("from a stale index, shuffle chose %d, %v", i, ok)
		}
	}
}

func TestRepeatCyclesThroughItsModes(t *testing.T) {
	a := newTestApp()
	var seen []repeatMode
	for range 4 {
		a.cycleRepeat()
		seen = append(seen, a.repeat)
	}
	if want := []repeatMode{repeatQueue, repeatTrack, repeatOff, repeatQueue}; !slices.Equal(seen, want) {
		t.Errorf("repeat went %v, want %v", seen, want)
	}
}
