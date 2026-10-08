package youtube

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		_, _ = w.Write([]byte(`{"contents":{"musicShelfRenderer":{"contents":[{"musicResponsiveListItemRenderer":{"videoId":"track-1","flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"First track"}]}}},{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"An artist"}]}}}],"fixedColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"simpleText":"3:42"}}}],"thumbnail":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.example/small"},{"url":"https://img.example/large"}]}}}}}]}}}`))
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
		if r.URL.Path != "/youtubei/v1/player" {
			t.Fatalf("request path = %q", r.URL.Path)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["videoId"] != "track-2" || request["racyCheckOk"] != true || request["contentCheckOk"] != true {
			t.Errorf("player payload = %#v", request)
		}
		_, _ = w.Write([]byte(`{"videoDetails":{"videoId":"track-2","title":"Song","author":"Artist","lengthSeconds":"201"},"playabilityStatus":{"status":"OK"},"streamingData":{"adaptiveFormats":[{"itag":140,"mimeType":"audio/mp4","bitrate":128000,"url":"https://audio.example/low"},{"itag":251,"mimeType":"audio/webm","averageBitrate":160000,"url":"https://audio.example/high"},{"itag":999,"mimeType":"audio/webm","averageBitrate":256000,"signatureCipher":"s=encrypted"},{"itag":18,"mimeType":"video/mp4","url":"https://video.example/video"}]}}`))
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key"})
	track, err := client.GetTrackInfo(context.Background(), "track-2")
	if err != nil {
		t.Fatal(err)
	}
	format, ok := track.BestAudioFormat()
	if !ok || format.Itag != 251 || format.URL != "https://audio.example/high" {
		t.Errorf("best audio = %#v, found %v", format, ok)
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

func TestSetTokensRejectsIncompleteCredentials(t *testing.T) {
	oauth := NewOAuth(OAuthConfig{})
	if err := oauth.SetTokens(Tokens{AccessToken: "only-access"}); err == nil {
		t.Fatal("SetTokens accepted incomplete tokens")
	}
	if _, err := oauth.AccessToken(context.Background()); err == nil {
		t.Fatal("AccessToken should require saved tokens")
	}
}
