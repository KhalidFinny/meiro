package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/youtube"
)

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
	case strings.Contains(asked, "MPREb_test"):
		reply = albumResponse
	case strings.Contains(asked, `"query"`):
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
	if !tt.HasText("Meiro") {
		t.Fatalf("the sidebar is missing: %q", tt.Texts())
	}
	if !tt.HasText("Quick picks") || !tt.HasText("Ambient One") {
		t.Fatalf("the home page did not list its sections: %q", tt.Texts())
	}
	if !tt.HasText("Sign in with Google") {
		t.Errorf("the account panel is missing: %q", tt.Texts())
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
	if a.detail.title != "Deep Focus" {
		t.Errorf("the album page heading is %q", a.detail.title)
	}
	if !tt.HasText("Album Track One") {
		t.Fatalf("the album page did not list its tracks: %q", tt.Texts())
	}
	if len(a.playable) != 1 || a.playable[0].VideoID != "vid-3" {
		t.Errorf("the album's play queue is %v", a.playable)
	}
	if a.playable[0].Duration != "4:12" {
		t.Errorf("the track length is %q", a.playable[0].Duration)
	}
}

func TestSearchShowsResults(t *testing.T) {
	a := newTestApp()
	a.router.Push("/search")
	tt := ui.NewTester(a.view, 1000, 700)
	if err := tt.Click("Search YouTube Music"); err != nil {
		t.Fatal(err)
	}
	tt.Type("ambient")
	tt.Key(0, ui.KeyEnter)
	if !tt.HasText("Search Result Song") {
		t.Fatalf("the search results are missing: %q", tt.Texts())
	}
}

func TestPlayerBarShowsTheCurrentTrack(t *testing.T) {
	a := newTestApp()
	a.current = youtube.MusicItem{VideoID: "vid-1", Title: "Ambient One", Subtitle: "Someone"}
	a.total = 3*time.Minute + 33*time.Second
	tt := ui.NewTester(a.view, 1000, 700)
	if !tt.HasText("Ambient One") {
		t.Fatalf("the player bar does not show the track: %q", tt.Texts())
	}
	if !tt.HasText("0:00 / 3:33") {
		t.Errorf("the player bar does not show the time: %q", tt.Texts())
	}
	if _, ok := tt.Find("Play or pause"); !ok {
		t.Errorf("the transport is missing: %q", tt.Texts())
	}
}

func TestTargetOfRoutesItemsToPages(t *testing.T) {
	cases := []struct {
		name string
		item youtube.MusicItem
		kind string
		id   string
	}{
		{"song", youtube.MusicItem{VideoID: "abc"}, pageTrack, "abc"},
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
