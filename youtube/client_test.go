package youtube

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSearchUsesMusicContextAndMapsItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/youtubei/v1/search" {
			t.Fatalf("request path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("key") != "test-key" {
			t.Errorf("API key = %q", r.URL.Query().Get("key"))
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["query"] != "ambient" || request["client"] != nil || request["isAudioOnly"] != true {
			t.Errorf("request fields = %#v", request)
		}
		clientContext := request["context"].(map[string]any)["client"].(map[string]any)
		if clientContext["clientName"] != "WEB_REMIX" || clientContext["clientVersion"] != "1.2026.test" {
			t.Errorf("client context = %#v", clientContext)
		}
		encoded, ok := request["params"].(string)
		if !ok {
			t.Fatal("song search is missing filter params")
		}
		decoded, err := url.QueryUnescape(encoded)
		if err != nil {
			t.Fatal(err)
		}
		filter, err := base64.StdEncoding.DecodeString(decoded)
		if err != nil {
			t.Fatal(err)
		}
		if len(filter) == 0 || filter[0] != 0x12 {
			t.Errorf("search filter protobuf = %x", filter)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"contents":{"musicShelfRenderer":{"contents":[{"musicResponsiveListItemRenderer":{"videoId":"track-1","flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"First track"}]}}},{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"An artist"}]}}}],"fixedColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"simpleText":"3:42"}}}],"thumbnail":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.example/small"},{"url":"https://img.example/large"}]}}}}}],"continuations":[{"nextContinuationData":{"continuation":"SEARCH_NEXT"}}]}}}`))
	}))
	defer server.Close()

	client := NewClient(Options{BaseURL: server.URL, APIKey: "test-key", ClientVersion: "1.2026.test"})
	result, err := client.Search(context.Background(), "ambient", SearchOptions{Type: SearchSongs})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("got %d items, want one: %#v", len(result.Items), result.Items)
	}
	item := result.Items[0]
	if item.VideoID != "track-1" || item.Title != "First track" || item.Subtitle != "An artist" || item.Duration != "3:42" || item.Thumbnail != "https://img.example/large" {
		t.Errorf("mapped item = %#v", item)
	}
	if result.ContinuationToken != "SEARCH_NEXT" {
		t.Errorf("search continuation = %q", result.ContinuationToken)
	}
}

func TestReadBoundedBodyEnforcesTheLimit(t *testing.T) {
	if got, err := readBoundedBody(strings.NewReader("1234"), 4, "test body"); err != nil || string(got) != "1234" {
		t.Fatalf("reading an exact-limit body = %q, %v", got, err)
	}
	if got, err := readBoundedBody(strings.NewReader("12345"), 4, "test body"); err == nil || got != nil {
		t.Fatalf("reading an oversized body = %q, %v, want an error and no data", got, err)
	}
}

func TestVideoRenderersKeepTheirKindAndThumbnail(t *testing.T) {
	client := NewClient(Options{})
	result := client.newSearchResult(json.RawMessage(`{"contents":{"items":[
		{"musicVideoRenderer":{"videoId":"music-video","title":{"simpleText":"Music video"},"thumbnail":{"thumbnails":[{"url":"https://img.example/music-video"}]}}},
		{"videoRenderer":{"videoId":"regular-video","title":{"simpleText":"Regular video"},"thumbnail":{"thumbnails":[{"url":"https://img.example/regular-video"}]}}}
	]}}`))
	if len(result.Items) != 2 {
		t.Fatalf("video search items = %#v", result.Items)
	}
	items := make(map[string]MusicItem, len(result.Items))
	for _, item := range result.Items {
		items[item.VideoID] = item
	}
	for id, thumbnail := range map[string]string{
		"music-video":   "https://img.example/music-video",
		"regular-video": "https://img.example/regular-video",
	} {
		item, ok := items[id]
		if !ok || item.Kind != "video" || item.Thumbnail != thumbnail {
			t.Errorf("video %q = %#v, want kind video and thumbnail %q", id, item, thumbnail)
		}
	}
}

func TestContinueSearchSendsContinuationToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if r.URL.Path != "/youtubei/v1/search" || request["continuation"] != "SEARCH_NEXT" || request["query"] != nil {
			t.Errorf("continuation request path=%q body=%#v", r.URL.Path, request)
		}
		_, _ = w.Write([]byte(`{"continuationContents":{"musicShelfContinuation":{"contents":[{"musicResponsiveListItemRenderer":{"videoId":"track-2","title":{"simpleText":"Second track"}}}]}}}`))
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key"})
	result, err := client.ContinueSearch(context.Background(), "SEARCH_NEXT")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].VideoID != "track-2" {
		t.Errorf("continued search items = %#v", result.Items)
	}
}

func TestGetAllLibraryLoadsEverySectionContinuation(t *testing.T) {
	var continuationRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["browseId"] == "FEmusic_library_landing" {
			_, _ = w.Write([]byte(`{"contents":{"sectionListRenderer":{"contents":[{"musicShelfRenderer":{"title":{"simpleText":"Songs"},"contents":[{"musicResponsiveListItemRenderer":{"videoId":"saved-song","title":{"simpleText":"Saved song"}}}],"continuations":[{"nextContinuationData":{"continuation":"SONGS_NEXT"}}]}},{"gridRenderer":{"title":{"simpleText":"Playlists"},"items":[{"gridPlaylistRenderer":{"playlistId":"PL1","title":{"simpleText":"Saved playlist"}}}],"continuations":[{"nextContinuationData":{"continuation":"PLAYLISTS_NEXT"}}]}}]}}}`))
			return
		}
		token, _ := request["continuation"].(string)
		continuationRequests.Add(1)
		switch token {
		case "SONGS_NEXT":
			_, _ = w.Write([]byte(`{"continuationContents":{"musicShelfContinuation":{"contents":[{"musicResponsiveListItemRenderer":{"videoId":"saved-song-2","title":{"simpleText":"More saved songs"}}}]}}}`))
		case "PLAYLISTS_NEXT":
			_, _ = w.Write([]byte(`{"continuationContents":{"gridContinuation":{"items":[{"gridPlaylistRenderer":{"playlistId":"PL2","title":{"simpleText":"More playlists"}}}]}}}`))
		default:
			t.Errorf("unexpected browse request %#v", request)
		}
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key"})
	library, err := client.GetAllLibrary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if continuationRequests.Load() != 2 {
		t.Errorf("continuation requests = %d, want 2", continuationRequests.Load())
	}
	if len(library.Items) != 4 {
		t.Errorf("library items = %#v", library.Items)
	}
	if len(library.Sections) != 2 || library.Sections[0].Title == "" || library.Sections[1].Title == "" {
		t.Errorf("library sections = %#v", library.Sections)
	}
	if len(library.Pages) != 3 || library.ContinuationToken != "" {
		t.Errorf("library pages/continuation = %d / %q", len(library.Pages), library.ContinuationToken)
	}
}

func TestBootstrapLoadsMusicAPIConfig(t *testing.T) {
	var receivedVersion atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			_, _ = w.Write([]byte(`{"INNERTUBE_API_KEY":"discovered-key","INNERTUBE_CLIENT_VERSION":"2.2026.discovered","VISITOR_DATA":"visitor"}`))
			return
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		client := request["context"].(map[string]any)["client"].(map[string]any)
		receivedVersion.Store(client["clientVersion"])
		_, _ = w.Write([]byte(`{"contents":[]}`))
	}))
	defer server.Close()

	client := NewClient(Options{BaseURL: server.URL, MusicURL: server.URL})
	if _, err := client.GetHomeFeed(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := receivedVersion.Load(); got != "2.2026.discovered" {
		t.Errorf("client version = %v, want discovered version", got)
	}
	if client.apiKey != "discovered-key" || client.visitorData != "visitor" {
		t.Errorf("bootstrapped client config = key %q visitor %q", client.apiKey, client.visitorData)
	}
}

func TestGetTrackInfoChoosesBestDirectAudio(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/iframe_api":
			_, _ = w.Write([]byte(`var playerUrl="player\/test-player\/player_ias.vflset/en_US/base.js";`))
		case "/s/player/test-player/player_es6.vflset/en_US/base.js":
			_, _ = w.Write([]byte(`var config={signatureTimestamp:19372};`))
		case "/youtubei/v1/player":
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request["videoId"] != "track-2" || request["racyCheckOk"] != true || request["contentCheckOk"] != true {
				t.Errorf("player payload = %#v", request)
			}
			playback := request["playbackContext"].(map[string]any)["contentPlaybackContext"].(map[string]any)
			if playback["signatureTimestamp"] != float64(19372) {
				t.Errorf("signatureTimestamp = %v", playback["signatureTimestamp"])
			}
			integrity := request["serviceIntegrityDimensions"].(map[string]any)
			if integrity["poToken"] != "test-po-token" {
				t.Errorf("service integrity dimensions = %#v", integrity)
			}
			_, _ = w.Write([]byte(`{"videoDetails":{"videoId":"track-2","title":"Song","author":"Artist","lengthSeconds":"201"},"playabilityStatus":{"status":"OK"},"streamingData":{"dashManifestUrl":"https://manifest.example/track.mpd","hlsManifestUrl":"https://manifest.example/track.m3u8","serverAbrStreamingUrl":"https://manifest.example/abr","adaptiveFormats":[{"itag":140,"mimeType":"audio/mp4","bitrate":128000,"url":"https://audio.example/low"},{"itag":251,"mimeType":"audio/webm","averageBitrate":160000,"url":"https://audio.example/high"},{"itag":999,"mimeType":"audio/webm","averageBitrate":256000,"signatureCipher":"s=encrypted"},{"itag":18,"mimeType":"video/mp4","url":"https://video.example/video"}]}}`))
		default:
			t.Errorf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key", PlayerPoToken: "test-po-token"})
	track, err := client.GetTrackInfo(context.Background(), "track-2")
	if err != nil {
		t.Fatal(err)
	}
	format, ok := track.BestAudioFormat()
	if !ok || format.Itag != 251 || format.URL != "https://audio.example/high" {
		t.Errorf("best audio = %#v, found %v", format, ok)
	}
	playableURL, err := url.Parse(format.PlayableURL())
	if err != nil {
		t.Fatal(err)
	}
	if playableURL.Query().Get("cver") == "" || playableURL.Query().Get("pot") != "test-po-token" || playableURL.Query().Get("cpn") != track.CPN {
		t.Errorf("playable URL query = %#v", playableURL.Query())
	}
	if len(track.CPN) != 16 {
		t.Errorf("playback client ID length = %d, want 16", len(track.CPN))
	}
	if track.StreamingData.DashManifestURL == "" || track.StreamingData.HLSManifestURL == "" || track.StreamingData.ServerABRStreamingURL == "" {
		t.Errorf("manifest streaming URLs = %#v", track.StreamingData)
	}
	if track.Player.PlayerID != "test-player" || track.Player.SignatureTimestamp != 19372 {
		t.Errorf("player metadata = %#v", track.Player)
	}
}

func TestGetTrackInfoResolvesEncryptedAudioURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/iframe_api":
			_, _ = w.Write([]byte(`player\/decoder-player\/player_ias.vflset/en_US/base.js`))
		case "/s/player/decoder-player/player_es6.vflset/en_US/base.js":
			_, _ = w.Write([]byte(`var OPS={swap:function(a,b){var c=a[0];a[0]=a[b%a.length];a[b%a.length]=c},reverse:function(a){a.reverse()}};function decode(a){a=a.split("");OPS.swap(a,2);OPS.reverse(a,0);return a.join("")}if(x.get("n"))&&(b=ABC[0].x||NFn);NFn=function(a){return a.split("").reverse().join("")}var config={signatureTimestamp:20001};`))
		case "/youtubei/v1/player":
			_, _ = w.Write([]byte(`{"videoDetails":{"videoId":"cipher-track","title":"Cipher song"},"playabilityStatus":{"status":"OK"},"streamingData":{"adaptiveFormats":[{"itag":251,"mimeType":"audio/webm","averageBitrate":192000,"signatureCipher":"url=https%3A%2F%2Faudio.example%2Fvideoplayback%3Fexpire%3D1%26n%3Dabc&s=abcdef&sp=sig"}]}}`))
		default:
			t.Errorf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key", ClientVersion: "1.2026.test"})
	track, err := client.GetTrackInfo(context.Background(), "cipher-track")
	if err != nil {
		t.Fatal(err)
	}
	format, ok := track.BestAudioFormat()
	if !ok {
		t.Fatalf("no playable audio format: %#v", track.StreamingData.AdaptiveFormats)
	}
	resolved, err := url.Parse(format.PlayableURL())
	if err != nil {
		t.Fatal(err)
	}
	query := resolved.Query()
	if resolved.Host != "audio.example" || query.Get("sig") != "fedabc" || query.Get("n") != "cba" || query.Get("expire") != "1" || query.Get("cver") != "1.2026.test" {
		t.Errorf("resolved playback URL = %q", format.PlayableURL())
	}
}

func TestPlaylistAndUpNextAreReadOnlyBrowseCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("request method = %q, want POST", r.Method)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		switch r.URL.Path {
		case "/youtubei/v1/browse":
			if request["browseId"] != "VLPL123" {
				t.Errorf("playlist browse ID = %v", request["browseId"])
			}
			_, _ = w.Write([]byte(`{"contents":{"playlistVideoListRenderer":{"contents":[{"playlistVideoRenderer":{"videoId":"playlist-track","title":{"simpleText":"Playlist song"},"lengthText":{"simpleText":"2:58"}}}]}}}`))
		case "/youtubei/v1/next":
			if request["videoId"] != "playlist-track" {
				t.Errorf("up-next video ID = %v", request["videoId"])
			}
			_, _ = w.Write([]byte(`{"contents":{"playlistPanelVideoRenderer":{"videoId":"next-track","title":{"simpleText":"Next song"}}}}`))
		default:
			t.Errorf("unexpected endpoint %q", r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key"})
	playlist, err := client.GetPlaylist(context.Background(), "PL123")
	if err != nil {
		t.Fatal(err)
	}
	if len(playlist.Items) != 1 || playlist.Items[0].VideoID != "playlist-track" || playlist.Items[0].Title != "Playlist song" || playlist.Items[0].Kind != "video" {
		t.Errorf("playlist items = %#v", playlist.Items)
	}
	if len(playlist.Sections) != 1 || playlist.Sections[0].Kind != "playlistVideoListRenderer" || len(playlist.Sections[0].Items) != 1 {
		t.Errorf("playlist sections = %#v", playlist.Sections)
	}
	next, err := client.GetUpNext(context.Background(), "playlist-track")
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Items) != 1 || next.Items[0].VideoID != "next-track" || next.Items[0].Title != "Next song" {
		t.Errorf("up-next items = %#v", next.Items)
	}
}

// TestSearchKeepsItsTopCardResult reads the card of a search's top result. The
// media lives in the card itself: its title run navigates to the video, its
// subtitle names the artist and trails the length. The mix queue of the
// card's menu entries must not become the video's destination.
func TestSearchKeepsItsTopCardResult(t *testing.T) {
	client := NewClient(Options{})
	card := `{"thumbnail":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.example/top-card"}]}}},"title":{"runs":[{"text":"Top video","navigationEndpoint":{"watchEndpoint":{"videoId":"top-video"}}}]},"subtitle":{"runs":[{"text":"Video"},{"text":" • "},{"text":"Top Artist","navigationEndpoint":{"browseEndpoint":{"browseId":"UCartist"}}},{"text":" • "},{"text":"8.4M views"},{"text":" • "},{"text":"2:05"}]},"onTap":{"watchEndpoint":{"videoId":"top-video"}},"menu":{"menuRenderer":{"items":[{"menuNavigationItemRenderer":{"navigationEndpoint":{"watchEndpoint":{"videoId":"top-video","playlistId":"RDAMVMtop-video"}}}}]}}}`
	row := `{"playlistItemData":{"videoId":"row-video"},"flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Row video"}]}}},{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Video"},{"text":" • "},{"text":"Row Artist"}]}}}]}`

	raw := `{"contents":{"tabbedSearchResultsRenderer":{"tabs":[{"tabRenderer":{"content":{"sectionListRenderer":{"contents":[{"musicCardShelfRenderer":` + card + `},{"itemSectionRenderer":{"contents":[{"musicResponsiveListItemRenderer":` + row + `}]}}]}}}}]}}}`
	result := client.newSearchResult(json.RawMessage(raw))
	if len(result.Items) != 2 {
		t.Fatalf("search items = %#v", result.Items)
	}
	top := result.Items[0]
	if top.VideoID != "top-video" || top.Title != "Top video" {
		t.Errorf("top card = %#v, want the card's video", top)
	}
	if top.Subtitle != "Video • Top Artist • 8.4M views" || top.Duration != "2:05" {
		t.Errorf("top card subtitle %q with length %q", top.Subtitle, top.Duration)
	}
	if top.BrowseID != "UCartist" {
		t.Errorf("top card artist = %q, want UCartist", top.BrowseID)
	}
	if top.Thumbnail != "https://img.example/top-card" {
		t.Errorf("top card thumbnail = %q", top.Thumbnail)
	}
}

func TestTrackReadsArtistBrowseIDFromItsSubtitle(t *testing.T) {
	var renderer map[string]any
	if err := json.Unmarshal([]byte(`{"videoId":"track-1","flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"simpleText":"Track"}}},{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Album","navigationEndpoint":{"browseEndpoint":{"browseId":"MPRalbum"}}},{"text":" • "},{"text":"Artist","navigationEndpoint":{"browseEndpoint":{"browseId":"UCartist"}}}]}}}]}`), &renderer); err != nil {
		t.Fatal(err)
	}
	item := parseMusicItem("track", renderer, false)
	if item.BrowseID != "UCartist" {
		t.Errorf("track artist browse ID = %q, want UCartist", item.BrowseID)
	}
}

func TestUpNextIgnoresItsMixQueueID(t *testing.T) {
	// A queue entry names both its track and the mix offered beside it. The
	// queue ID must not become the track's destination: opening it browses
	// empty.
	client := NewClient(Options{})
	result := client.newUpNextResult(json.RawMessage(`{"contents":{"playlistPanelRenderer":{"contents":[
		{"playlistPanelVideoRenderer":{
			"videoId":"queue-video",
			"title":{"simpleText":"Queue video"},
			"navigationEndpoint":{"watchEndpoint":{"videoId":"queue-video","playlistId":"RDAMVMqueue-video"}}
		}}
	]}}}`))
	if len(result.Items) != 1 {
		t.Fatalf("up-next items = %#v", result.Items)
	}
	item := result.Items[0]
	if item.VideoID != "queue-video" || item.BrowseID != "" {
		t.Errorf("queue entry = %#v, want only its video ID", item)
	}
}

func TestUpNextKeepsPlaylistContextAndContinuesRadioQueue(t *testing.T) {
	var nextRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/youtubei/v1/next" {
			t.Fatalf("request path = %q, want /next", r.URL.Path)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		switch nextRequests.Add(1) {
		case 1:
			if request["videoId"] != "track-1" || request["playlistId"] != "PL123" || request["playlistIndex"] != float64(2) {
				t.Errorf("initial up-next request = %#v", request)
			}
			if request["continuation"] != nil {
				t.Errorf("initial up-next request unexpectedly has continuation: %#v", request)
			}
			_, _ = w.Write([]byte(`{"continuationContents":{"playlistPanelContinuation":{"contents":[{"playlistPanelVideoRenderer":{"videoId":"track-2","title":{"simpleText":"Next song"}}}],"continuations":[{"nextContinuationData":{"continuation":"STANDARD_MORE"},"nextRadioContinuationData":{"continuation":"RADIO_MORE"}}]}}}`))
		case 2:
			if request["videoId"] != "track-1" || request["playlistId"] != "PL123" || request["playlistIndex"] != float64(2) || request["continuation"] != "RADIO_MORE" {
				t.Errorf("continuation request = %#v", request)
			}
			_, _ = w.Write([]byte(`{"continuationContents":{"playlistPanelContinuation":{"contents":[{"playlistPanelVideoRenderer":{"videoId":"track-3","title":{"simpleText":"Radio song"}}}]}}}`))
		default:
			t.Errorf("unexpected /next request #%d", nextRequests.Load())
		}
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key"})
	index := 2
	options := UpNextOptions{VideoID: "track-1", PlaylistID: "PL123", PlaylistIndex: &index}
	next, err := client.GetUpNextWithOptions(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Items) != 1 || next.Items[0].VideoID != "track-2" || next.ContinuationToken != "RADIO_MORE" {
		t.Fatalf("initial queue = items %#v, continuation %q", next.Items, next.ContinuationToken)
	}
	more, err := client.ContinueUpNext(context.Background(), options, next.ContinuationToken)
	if err != nil {
		t.Fatal(err)
	}
	if len(more.Items) != 1 || more.Items[0].VideoID != "track-3" || nextRequests.Load() != 2 {
		t.Errorf("continued queue = %#v after %d requests", more.Items, nextRequests.Load())
	}
}

func TestGetUpNextResolvesAutomixPreview(t *testing.T) {
	var nextRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/youtubei/v1/next" {
			t.Fatalf("request path = %q, want /next", r.URL.Path)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		switch nextRequests.Add(1) {
		case 1:
			if request["videoId"] != "seed-track" {
				t.Errorf("initial request = %#v", request)
			}
			_, _ = w.Write([]byte(`{"contents":{"playlistPanelRenderer":{"playlistId":"","contents":[{"automixPreviewVideoRenderer":{"content":{"automixPlaylistVideoRenderer":{"navigationEndpoint":{"watchPlaylistEndpoint":{"playlistId":"RDAMVMseed-track","params":"RADIO_PARAMS"}}}}}}]}}}`))
		case 2:
			if request["videoId"] != "seed-track" || request["playlistId"] != "RDAMVMseed-track" || request["params"] != "RADIO_PARAMS" {
				t.Errorf("automix request = %#v", request)
			}
			_, _ = w.Write([]byte(`{"contents":{"playlistPanelRenderer":{"playlistId":"RDAMVMseed-track","contents":[{"playlistPanelVideoRenderer":{"videoId":"radio-1","title":{"simpleText":"Radio track"}}}],"continuations":[{"nextRadioContinuationData":{"continuation":"RADIO_MORE"}}]}}}`))
		default:
			t.Errorf("unexpected /next request #%d", nextRequests.Load())
		}
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key"})
	result, err := client.GetUpNext(context.Background(), "seed-track")
	if err != nil {
		t.Fatal(err)
	}
	if nextRequests.Load() != 2 || result.QueuePlaylistID != "RDAMVMseed-track" || result.ContinuationToken != "RADIO_MORE" || len(result.Items) != 1 || result.Items[0].VideoID != "radio-1" {
		t.Fatalf("automix result after %d requests = %#v", nextRequests.Load(), result)
	}
}

func TestGetAllPlaylistLoadsContinuationPages(t *testing.T) {
	var continuationRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if r.URL.Path != "/youtubei/v1/browse" {
			t.Fatalf("request path = %q, want /browse", r.URL.Path)
		}
		if request["browseId"] == "VLPL123" {
			_, _ = w.Write([]byte(`{"contents":{"musicPlaylistShelfRenderer":{"contents":[{"playlistVideoRenderer":{"videoId":"playlist-1","title":{"simpleText":"First song"}}}],"continuations":[{"nextContinuationData":{"continuation":"PLAYLIST_MORE"}}]}}}`))
			return
		}
		if request["continuation"] != "PLAYLIST_MORE" {
			t.Errorf("continuation request = %#v", request)
		}
		continuationRequests.Add(1)
		_, _ = w.Write([]byte(`{"continuationContents":{"musicPlaylistShelfContinuation":{"contents":[{"playlistVideoRenderer":{"videoId":"playlist-2","title":{"simpleText":"Second song"}}}]}}}`))
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key"})
	playlist, err := client.GetAllPlaylist(context.Background(), "PL123")
	if err != nil {
		t.Fatal(err)
	}
	if continuationRequests.Load() != 1 || len(playlist.Items) != 2 {
		t.Fatalf("playlist has %d items after %d continuation calls: %#v", len(playlist.Items), continuationRequests.Load(), playlist.Items)
	}
	if playlist.Items[0].VideoID != "playlist-1" || playlist.Items[1].VideoID != "playlist-2" || len(playlist.Pages) != 2 || playlist.ContinuationToken != "" {
		t.Errorf("playlist continuation result = %#v", playlist)
	}
}

func TestLyricsRelatedAndRecapUseReadOnlyEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		switch r.URL.Path {
		case "/youtubei/v1/next":
			if request["videoId"] != "track-lyrics" {
				t.Errorf("track-tab video ID = %v", request["videoId"])
			}
			_, _ = w.Write([]byte(`{"tabs":[{"tabRenderer":{"endpoint":{"browseEndpoint":{"browseId":"lyrics-id","browseEndpointContextSupportedConfigs":{"browseEndpointContextMusicConfig":{"pageType":"MUSIC_PAGE_TYPE_TRACK_LYRICS"}}}}}},{"tabRenderer":{"endpoint":{"browseEndpoint":{"browseId":"related-id","browseEndpointContextSupportedConfigs":{"browseEndpointContextMusicConfig":{"pageType":"MUSIC_PAGE_TYPE_TRACK_RELATED"}}}}}}]}`))
		case "/youtubei/v1/browse":
			switch request["browseId"] {
			case "lyrics-id":
				_, _ = w.Write([]byte(`{"contents":{"musicDescriptionShelfRenderer":{"description":{"runs":[{"text":"Line one\nLine two"}]},"footer":{"simpleText":"Lyrics provided by partner"}}}}`))
			case "related-id":
				_, _ = w.Write([]byte(`{"contents":{"musicShelfRenderer":{"contents":[{"musicResponsiveListItemRenderer":{"videoId":"related-track","title":{"simpleText":"Related song"}}}]}}}`))
			case "FEmusic_listening_review":
				_, _ = w.Write([]byte(`{"contents":{"musicCarouselShelfRenderer":{"title":{"simpleText":"Your recap"}}}}`))
			default:
				t.Errorf("unexpected browse ID %v", request["browseId"])
			}
		default:
			t.Errorf("unexpected endpoint %q", r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key"})
	lyrics, err := client.GetLyrics(context.Background(), "track-lyrics")
	if err != nil {
		t.Fatal(err)
	}
	if lyrics.Description != "Line one\nLine two" || lyrics.Footer != "Lyrics provided by partner" {
		t.Errorf("lyrics = %#v", lyrics)
	}
	related, err := client.GetRelated(context.Background(), "track-lyrics")
	if err != nil {
		t.Fatal(err)
	}
	if len(related.Items) != 1 || related.Items[0].VideoID != "related-track" {
		t.Errorf("related items = %#v", related.Items)
	}
	recap, err := client.GetRecap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(recap.Sections) != 1 || recap.Sections[0].Title != "Your recap" {
		t.Errorf("recap sections = %#v", recap.Sections)
	}
}

func TestGetAccountDetailsUsesCookie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/youtubei/v1/account/accounts_list" {
			t.Fatalf("request path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "SAPISIDHASH ") {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-YouTube-Client-Name"); got != "7" {
			t.Errorf("client name header = %q, want TV client ID 7", got)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		client := request["context"].(map[string]any)["client"].(map[string]any)
		if client["clientName"] != "TVHTML5" || request["isAudioOnly"] != nil {
			t.Errorf("account request client context = %#v, body = %#v", client, request)
		}
		_, _ = w.Write([]byte(`{"accountName":"Me"}`))
	}))
	defer server.Close()
	cookieAuth, err := NewCookieAuth("SAPISID=secret", CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(Options{HTTPClient: server.Client(), BaseURL: server.URL, APIKey: "key", CookieAuth: cookieAuth, AllowInsecureCookieAuth: true})
	account, err := client.GetAccountDetails(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(account.Raw), `"accountName":"Me"`) {
		t.Errorf("account response = %s", account.Raw)
	}
	if account.Name != "Me" {
		t.Errorf("account name = %q", account.Name)
	}
}

func TestCookieAuthenticationAndAllAccounts(t *testing.T) {
	cookieAuth, err := NewCookieAuth("SAPISID=secret; SID=other", CookieOptions{AccountIndex: 2, OnBehalfOfUser: "UC-channel"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/youtubei/v1/account/accounts_list" {
			t.Errorf("request path = %q", r.URL.Path)
		}
		if r.Header.Get("Cookie") != "SAPISID=secret; SID=other" {
			t.Errorf("Cookie header = %q", r.Header.Get("Cookie"))
		}
		if r.Header.Get("X-Goog-Authuser") != "2" || r.Header.Get("X-Goog-PageId") != "UC-channel" {
			t.Errorf("account selection headers = %#v", r.Header)
		}
		authorization := strings.TrimPrefix(r.Header.Get("Authorization"), "SAPISIDHASH ")
		timestampText, digest, ok := strings.Cut(authorization, "_")
		if !ok {
			t.Errorf("malformed cookie authorization %q", r.Header.Get("Authorization"))
		} else if timestamp, err := strconv.ParseInt(timestampText, 10, 64); err != nil {
			t.Errorf("authorization timestamp %q is invalid: %v", timestampText, err)
		} else if got := r.Header.Get("Authorization"); got != cookieAuth.authorization(time.Unix(timestamp, 0)) {
			t.Errorf("authorization digest does not match SAPISID")
		} else if digest == "" {
			t.Error("authorization digest is empty")
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request["requestType"] != "ACCOUNTS_LIST_REQUEST_TYPE_CHANNEL_SWITCHER" || request["callCircumstance"] != "SWITCHING_USERS_FULL" {
			t.Errorf("account-list request = %#v", request)
		}
		user := request["context"].(map[string]any)["user"].(map[string]any)
		if user["onBehalfOfUser"] != "UC-channel" {
			t.Errorf("user context = %#v", user)
		}
		_, _ = w.Write([]byte(`{"accountSectionListRenderer":{"contents":[{"accountItemSectionRenderer":{"contents":[{"accountItemRenderer":{"accountName":{"simpleText":"Main channel"},"accountByline":{"simpleText":"Creator"},"channelHandle":{"runs":[{"text":"@main"}]},"endpoint":{"browseEndpoint":{"browseId":"UC-channel"}},"isSelected":true,"hasChannel":true,"accountPhoto":{"thumbnails":[{"url":"https://img.example/main"}]}}}]}}]}}`))
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key", CookieAuth: cookieAuth, AllowInsecureCookieAuth: true})
	accounts, err := client.GetAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(accounts.Raw), "UC-channel") || len(accounts.Items) != 1 {
		t.Errorf("accounts response = %#v", accounts)
	} else if channel := accounts.Items[0]; channel.Name != "Main channel" || channel.Byline != "Creator" || channel.ChannelID != "UC-channel" || channel.Handle != "@main" || !channel.Selected || !channel.HasChannel || channel.Thumbnail != "https://img.example/main" {
		t.Errorf("parsed account channel = %#v", channel)
	}
	if _, err := NewCookieAuth("SID=not-enough", CookieOptions{}); err == nil {
		t.Fatal("NewCookieAuth accepted cookies without SAPISID")
	}
}

func TestCookieAuthRejectsUntrustedBaseURLByDefault(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"accountName":"Me"}`))
	}))
	defer server.Close()
	auth, err := NewCookieAuth("SAPISID=secret", CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key", CookieAuth: auth})
	if _, err := client.GetAccountDetails(context.Background()); err == nil || !strings.Contains(err.Error(), "refusing to send cookie authentication") {
		t.Fatalf("GetAccountDetails error = %v, want refusal to send credentials", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("sent %d requests to untrusted base URL", requests.Load())
	}
}

func TestIsYouTubeURLRequiresHTTPSAndAYouTubeHostname(t *testing.T) {
	for input, want := range map[string]bool{
		"https://youtube.com":              true,
		"https://www.youtube.com":          true,
		"https://music.youtube.com":        true,
		"http://youtube.com":               false,
		"https://youtube.com.evil.example": false,
		"https://notyoutube.com":           false,
		"https://youtube.com@evil.example": false,
	} {
		parsed, err := url.Parse(input)
		if err != nil {
			t.Fatal(err)
		}
		if got := isYouTubeURL(parsed); got != want {
			t.Errorf("isYouTubeURL(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestLoadPlayerKeepsTheScriptWhileTheIDHolds(t *testing.T) {
	var scripts atomic.Int32
	playerID := "first-player"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/iframe_api":
			_, _ = w.Write([]byte(`player\/` + playerID + `\/player_ias.vflset/en_US/base.js`))
		case strings.HasSuffix(r.URL.Path, "/base.js"):
			scripts.Add(1)
			_, _ = w.Write([]byte(`var config={signatureTimestamp:19372};`))
		default:
			t.Errorf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key"})
	first, err := client.loadPlayer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first.expiresAt = time.Now().Add(-time.Second)
	again, err := client.loadPlayer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if again != first || scripts.Load() != 1 || !again.expiresAt.After(time.Now()) {
		t.Errorf("same ID: player kept = %v, scripts fetched = %d, expiry moved = %v", again == first, scripts.Load(), again.expiresAt.After(time.Now()))
	}
	again.expiresAt = time.Now().Add(-time.Second)
	playerID = "second-player"
	next, err := client.loadPlayer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if next == first || next.metadata.PlayerID != "second-player" || scripts.Load() != 2 {
		t.Errorf("new ID: player replaced = %v, scripts fetched = %d", next != first, scripts.Load())
	}
}

func TestResultsDropRendererJSONUnlessKept(t *testing.T) {
	page := `{"contents":{"musicCarouselShelfRenderer":{"header":{"musicCarouselShelfBasicHeaderRenderer":{"title":{"runs":[{"text":"Shelf"}]}}},"contents":[{"musicResponsiveListItemRenderer":{"videoId":"track-1","flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"First track"}]}}}]}}]}}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()
	for _, keep := range []bool{false, true} {
		client := NewClient(Options{BaseURL: server.URL, APIKey: "key", KeepRenderers: keep})
		result, err := client.GetHomeFeed(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Sections) != 1 || len(result.Sections[0].Items) != 1 || len(result.Items) != 1 {
			t.Fatalf("keep=%v: sections = %#v, items = %#v", keep, result.Sections, result.Items)
		}
		item, section := result.Sections[0].Items[0], result.Sections[0]
		if item.Title != "First track" || section.Title != "Shelf" {
			t.Errorf("keep=%v: item %q in shelf %q", keep, item.Title, section.Title)
		}
		kept := len(item.Raw) > 0 && len(section.Raw) > 0 && len(result.Items[0].Raw) > 0
		dropped := len(item.Raw) == 0 && len(section.Raw) == 0 && len(result.Items[0].Raw) == 0
		if keep && !kept || !keep && !dropped {
			t.Errorf("keep=%v: item raw %d, section raw %d, page item raw %d bytes", keep, len(item.Raw), len(section.Raw), len(result.Items[0].Raw))
		}
		if len(result.Raw) == 0 {
			t.Errorf("keep=%v: the response itself should stay", keep)
		}
	}
}

func TestExtractMusicItemsKeepsASongListedTwice(t *testing.T) {
	entry := func(setID string) string {
		return `{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"V1","playlistSetVideoId":"` + setID + `"},` +
			`"flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Song"}]}}}]}}`
	}
	twice := []byte(`{"contents":[` + entry("S1") + `,` + entry("S2") + `]}`)
	if got := extractMusicItems(decodeResponse(twice), false); len(got) != 2 {
		t.Errorf("a playlist holding a song twice gave %d items, want 2", len(got))
	}
	// The same entry met twice, as a response may repeat one, stays one.
	repeated := []byte(`{"contents":[` + entry("S1") + `,` + entry("S1") + `]}`)
	if got := extractMusicItems(decodeResponse(repeated), false); len(got) != 1 {
		t.Errorf("a repeated entry gave %d items, want 1", len(got))
	}
}
