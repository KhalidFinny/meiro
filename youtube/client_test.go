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
			_, _ = w.Write([]byte(`{"contents":{"playlistVideoRenderer":{"videoId":"playlist-track","title":{"simpleText":"Playlist song"},"lengthText":{"simpleText":"2:58"}}}}`))
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
	if len(playlist.Items) != 1 || playlist.Items[0].VideoID != "playlist-track" || playlist.Items[0].Title != "Playlist song" {
		t.Errorf("playlist items = %#v", playlist.Items)
	}
	next, err := client.GetUpNext(context.Background(), "playlist-track")
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Items) != 1 || next.Items[0].VideoID != "next-track" || next.Items[0].Title != "Next song" {
		t.Errorf("up-next items = %#v", next.Items)
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

func TestGetAccountDetailsUsesOAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/youtubei/v1/account/accounts_list" {
			t.Fatalf("request path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer account-token" {
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
	oauth := NewOAuth(OAuthConfig{HTTPClient: server.Client(), BaseURL: server.URL})
	if err := oauth.SetTokens(Tokens{
		AccessToken: "account-token", RefreshToken: "refresh", ExpiryDate: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	client := NewClient(Options{HTTPClient: server.Client(), BaseURL: server.URL, APIKey: "key", OAuth: oauth})
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
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key", CookieAuth: cookieAuth})
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

func TestOAuthDiscoversClientCredentialsFromTV(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tv":
			_, _ = w.Write([]byte(`<script id="base-js" src="/base.js"></script>`))
		case "/base.js":
			_, _ = w.Write([]byte(`clientId:"discovered-id",clientSecret:"discovered-secret"`))
		case "/o/oauth2/device/code":
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			if request["client_id"] != "discovered-id" || request["device_model"] != "ytlr::" || request["device_id"] == "" {
				t.Errorf("device authorization request = %#v", request)
			}
			_, _ = w.Write([]byte(`{"device_code":"device","user_code":"ABCD","verification_url":"https://google.test/device","expires_in":600,"interval":5}`))
		default:
			t.Errorf("unexpected OAuth path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	oauth := NewOAuth(OAuthConfig{HTTPClient: server.Client(), BaseURL: server.URL})
	code, err := oauth.BeginDeviceFlow(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if code.DeviceCode != "device" {
		t.Fatalf("device code = %#v", code)
	}
	oauth.mu.Lock()
	credentials := oauth.credentials
	oauth.mu.Unlock()
	if credentials.ClientID != "discovered-id" || credentials.ClientSecret != "discovered-secret" {
		t.Errorf("discovered credentials = %#v", credentials)
	}
}

func TestOAuthRestoreRefreshesAndPersistsTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/o/oauth2/token" {
			t.Errorf("request path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"access_token":"refreshed","refresh_token":"rotated","expires_in":3600}`))
	}))
	defer server.Close()
	store := &memoryTokenStore{tokens: Tokens{
		AccessToken: "expired", RefreshToken: "refresh", ExpiryDate: time.Now().Add(-time.Minute),
		Client: &OAuthClientCredentials{ClientID: "client-id", ClientSecret: "client-secret"},
	}, exists: true}
	oauth := NewOAuth(OAuthConfig{HTTPClient: server.Client(), BaseURL: server.URL, TokenStore: store})
	if err := oauth.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, ok := oauth.Tokens()
	if !ok || got.AccessToken != "refreshed" || got.RefreshToken != "rotated" {
		t.Errorf("restored tokens = %#v, found %v", got, ok)
	}
	if !store.exists || store.tokens.AccessToken != "refreshed" || store.tokens.RefreshToken != "rotated" {
		t.Errorf("persisted tokens = %#v", store.tokens)
	}
}

func TestOAuthDeviceFlowAndAutomaticRefresh(t *testing.T) {
	var pollCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/o/oauth2/device/code":
			_, _ = w.Write([]byte(`{"device_code":"device","user_code":"ABCD","verification_url":"https://google.test/device","expires_in":5,"interval":1}`))
		case "/o/oauth2/token":
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["client_secret"] != "client-secret" {
				t.Errorf("client secret missing from token request")
			}
			if payload["grant_type"] == "refresh_token" {
				_, _ = w.Write([]byte(`{"access_token":"refreshed","expires_in":3600}`))
				return
			}
			if pollCount.Add(1) == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"initial","refresh_token":"refresh","expires_in":3600}`))
		default:
			t.Fatalf("unexpected OAuth path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	oauth := NewOAuth(OAuthConfig{
		ClientID: "client-id", ClientSecret: "client-secret", HTTPClient: server.Client(), BaseURL: server.URL,
	})
	code, err := oauth.BeginDeviceFlow(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if code.UserCode != "ABCD" || code.VerificationURL != "https://google.test/device" {
		t.Fatalf("device code response = %#v", code)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	tokens, err := oauth.PollForTokens(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != "initial" {
		t.Fatalf("access token = %q", tokens.AccessToken)
	}
	if err := oauth.SetTokens(Tokens{
		AccessToken: "expired", RefreshToken: "refresh", ExpiryDate: time.Now().Add(-time.Minute),
		Client: &OAuthClientCredentials{ClientID: "client-id", ClientSecret: "client-secret"},
	}); err != nil {
		t.Fatal(err)
	}
	accessToken, err := oauth.AccessToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if accessToken != "refreshed" {
		t.Errorf("refreshed access token = %q", accessToken)
	}
}

type memoryTokenStore struct {
	tokens Tokens
	exists bool
}

func (store *memoryTokenStore) Load(context.Context) (Tokens, error) {
	if !store.exists {
		return Tokens{}, ErrNoStoredTokens
	}
	return store.tokens, nil
}

func (store *memoryTokenStore) Save(_ context.Context, tokens Tokens) error {
	store.tokens = tokens
	store.exists = true
	return nil
}

func (store *memoryTokenStore) Delete(context.Context) error {
	store.tokens = Tokens{}
	store.exists = false
	return nil
}

func TestSetTokensRejectsIncompleteCredentials(t *testing.T) {
	oauth := NewOAuth(OAuthConfig{})
	if err := oauth.SetTokens(Tokens{AccessToken: "only-access"}); err == nil {
		t.Fatal("SetTokens accepted incomplete tokens")
	}
	if _, err := oauth.AccessToken(context.Background()); err == nil {
		t.Fatal("AccessToken should require saved tokens")
	}
}
