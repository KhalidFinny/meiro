// Package youtube provides a small, read-only YouTube Music client.
//
// The package is independent of the desktop application and can be imported on
// its own. It uses YouTube's InnerTube API and preserves each response's raw
// JSON so callers can access fields that are not mapped to the convenience
// types yet.
package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultMusicURL      = "https://music.youtube.com"
	defaultAPIURL        = "https://www.youtube.com"
	defaultMusicVersion  = "1.20250219.01.00"
	defaultWebVersion    = "2.20260623.01.00"
	defaultTVVersion     = "7.20260311.12.00"
	defaultMusicContext  = "WEB_REMIX"
	defaultMusicClient   = "YTMUSIC"
	defaultMusicClientID = "67"
)

// maxResponseBytes is the most of an API response the client will read. The
// biggest pages, a whole library, are a few megabytes.
const maxResponseBytes = 64 << 20

// maxConfigBytes bounds the HTML downloaded to discover InnerTube settings.
const maxConfigBytes = 32 << 20

// browserUserAgent is the User-Agent the Music homepage needs to serve its
// full page, which carries the InnerTube API key. Without it, YouTube
// answers with a stub that has no key.
const browserUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

var (
	apiKeyPattern        = regexp.MustCompile(`"INNERTUBE_API_KEY"\s*:\s*"([^"]+)"`)
	clientVersionPattern = regexp.MustCompile(`"INNERTUBE_CLIENT_VERSION"\s*:\s*"([^"]+)"`)
	visitorDataPattern   = regexp.MustCompile(`"VISITOR_DATA"\s*:\s*"([^"]+)"`)
)

// Options configures a Music client. BaseURL is the YouTube InnerTube API host;
// MusicURL is the homepage used to discover its API key and client context. If
// APIKey is empty, the first request reads the key, Music client version, and
// visitor data from MusicURL.
type Options struct {
	HTTPClient       *http.Client
	BaseURL          string
	MusicURL         string
	APIKey           string
	ClientVersion    string
	WebClientVersion string
	TVClientVersion  string
	VisitorData      string
	Language         string
	Country          string
	// PlayerPoToken supplies an optional precomputed playback PO token.
	PlayerPoToken string
	CookieAuth    *CookieAuth
	// AllowInsecureCookieAuth permits sending CookieAuth to a non-YouTube or
	// non-HTTPS BaseURL. This is intended for trusted local test servers only.
	AllowInsecureCookieAuth bool
	// KeepRenderers keeps the renderer JSON in MusicItem.Raw and
	// MusicSection.Raw. It is dropped by default: the result's own Raw still
	// holds the whole response, but the items a caller keeps around for as
	// long as a page is open should not each carry a copy of theirs.
	KeepRenderers bool
}

// Client issues read-only YouTube Music requests.
type Client struct {
	httpClient              *http.Client
	baseURL                 string
	musicURL                string
	apiKey                  string
	clientVersion           string
	webClientVersion        string
	tvClientVersion         string
	visitorData             string
	language                string
	country                 string
	playerPoToken           string
	cookieAuth              *CookieAuth
	allowInsecureCookieAuth bool
	keepRenderers           bool

	configMu sync.Mutex
	playerMu sync.Mutex
	player   *playerScript
}

// NewClient constructs a client. No network request is made until a method is
// called. If APIKey is empty, the first request bootstraps configuration from
// the Music homepage.
func NewClient(options Options) *Client {
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if options.BaseURL == "" {
		options.BaseURL = defaultAPIURL
	}
	if options.MusicURL == "" {
		options.MusicURL = defaultMusicURL
	}
	if options.ClientVersion == "" {
		options.ClientVersion = defaultMusicVersion
	}
	if options.WebClientVersion == "" {
		options.WebClientVersion = defaultWebVersion
	}
	if options.TVClientVersion == "" {
		options.TVClientVersion = defaultTVVersion
	}
	if options.Language == "" {
		options.Language = "en"
	}
	if options.Country == "" {
		options.Country = "US"
	}
	return &Client{
		httpClient:              options.HTTPClient,
		baseURL:                 strings.TrimRight(options.BaseURL, "/"),
		musicURL:                strings.TrimRight(options.MusicURL, "/"),
		apiKey:                  options.APIKey,
		clientVersion:           options.ClientVersion,
		webClientVersion:        options.WebClientVersion,
		tvClientVersion:         options.TVClientVersion,
		visitorData:             options.VisitorData,
		language:                options.Language,
		country:                 options.Country,
		playerPoToken:           options.PlayerPoToken,
		cookieAuth:              options.CookieAuth,
		allowInsecureCookieAuth: options.AllowInsecureCookieAuth,
		keepRenderers:           options.KeepRenderers,
	}
}

type clientContext struct {
	Client struct {
		HL            string `json:"hl"`
		GL            string `json:"gl"`
		ClientName    string `json:"clientName"`
		ClientVersion string `json:"clientVersion"`
		VisitorData   string `json:"visitorData,omitempty"`
	} `json:"client"`
	User *clientUserContext `json:"user,omitempty"`
}

type clientUserContext struct {
	OnBehalfOfUser string `json:"onBehalfOfUser,omitempty"`
}

func (c *Client) context() clientContext {
	var ctx clientContext
	ctx.Client.HL = c.language
	ctx.Client.GL = c.country
	ctx.Client.ClientName = defaultMusicContext
	ctx.Client.ClientVersion = c.clientVersion
	if c.cookieAuth == nil {
		ctx.Client.VisitorData = c.visitorData
	}
	return ctx
}

func (c *Client) ensureConfig(ctx context.Context) error {
	c.configMu.Lock()
	defer c.configMu.Unlock()
	if c.apiKey != "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.musicURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", browserUserAgent)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("youtube: load Music configuration: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return responseError("load Music configuration", resp)
	}
	body, err := readBoundedBody(resp.Body, maxConfigBytes, "Music configuration")
	if err != nil {
		return err
	}
	key := apiKeyPattern.FindSubmatch(body)
	if len(key) < 2 {
		return errors.New("youtube: API key not found on Music homepage; set Options.APIKey")
	}
	c.apiKey = string(key[1])
	if match := clientVersionPattern.FindSubmatch(body); len(match) > 1 {
		c.clientVersion = string(match[1])
	}
	if match := visitorDataPattern.FindSubmatch(body); len(match) > 1 {
		c.visitorData = string(match[1])
	}
	return nil
}

func readBoundedBody(body io.Reader, limit int64, label string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("youtube: read %s: %w", label, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("youtube: %s exceeds %d bytes", label, limit)
	}
	return data, nil
}

// innerTubeClient is which of YouTube's clients a request speaks as.
type innerTubeClient string

const (
	musicClient innerTubeClient = "YTMUSIC"
	webClient   innerTubeClient = "WEB"
	tvClient    innerTubeClient = "TV"
)

func (c *Client) execute(ctx context.Context, endpoint string, payload map[string]any) (json.RawMessage, error) {
	return c.executeForClient(ctx, endpoint, payload, musicClient)
}

func (c *Client) executeForClient(ctx context.Context, endpoint string, payload map[string]any, client innerTubeClient) (json.RawMessage, error) {
	if err := c.ensureConfig(ctx); err != nil {
		return nil, err
	}
	var clientName, clientID, clientVersion string
	switch client {
	case musicClient:
		clientName, clientID, clientVersion = defaultMusicContext, defaultMusicClientID, c.clientVersion
	case webClient:
		clientName, clientID, clientVersion = "WEB", "1", c.webClientVersion
	case tvClient:
		clientName, clientID, clientVersion = "TVHTML5", "7", c.tvClientVersion
	default:
		return nil, fmt.Errorf("youtube: unsupported InnerTube client %q", client)
	}
	requestContext := c.context()
	requestContext.Client.ClientName = clientName
	requestContext.Client.ClientVersion = clientVersion
	if c.cookieAuth != nil {
		requestContext.User = &clientUserContext{OnBehalfOfUser: c.cookieAuth.onBehalfOfUser}
	}
	payload["context"] = requestContext
	if client == musicClient {
		payload["isAudioOnly"] = true
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("youtube: encode request: %w", err)
	}
	endpointURL := c.baseURL + "/youtubei/v1/" + strings.TrimLeft(endpoint, "/")
	parsedURL, err := url.Parse(endpointURL)
	if err != nil {
		return nil, fmt.Errorf("youtube: invalid API URL: %w", err)
	}
	if c.cookieAuth != nil && !c.allowInsecureCookieAuth && !isYouTubeURL(parsedURL) {
		return nil, errors.New("youtube: refusing to send cookie authentication to a non-YouTube or non-HTTPS URL")
	}
	query := parsedURL.Query()
	query.Set("key", c.apiKey)
	parsedURL.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, parsedURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	origin := parsedURL.Scheme + "://" + parsedURL.Host
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	req.Header.Set("X-YouTube-Client-Name", clientID)
	req.Header.Set("X-YouTube-Client-Version", clientVersion)
	// The visitor ID is an anonymous visit's; a signed-in session is its own.
	if c.visitorData != "" && c.cookieAuth == nil {
		req.Header.Set("X-Goog-Visitor-Id", c.visitorData)
	}
	if client == tvClient {
		req.Header.Set("User-Agent", "Mozilla/5.0 (ChromiumStylePlatform) Cobalt/Version")
	}
	if c.cookieAuth != nil {
		req.Header.Set("Cookie", c.cookieAuth.cookie)
		req.Header.Set("Authorization", c.cookieAuth.authorization(time.Now()))
		req.Header.Set("X-Goog-Authuser", strconv.Itoa(c.cookieAuth.accountIndex))
		if c.cookieAuth.onBehalfOfUser != "" {
			req.Header.Set("X-Goog-PageId", c.cookieAuth.onBehalfOfUser)
		}
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("youtube: %s request: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, responseError(endpoint, resp)
	}
	var result json.RawMessage
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&result); err != nil {
		return nil, fmt.Errorf("youtube: decode %s response: %w", endpoint, err)
	}
	return result, nil
}

func isYouTubeURL(endpoint *url.URL) bool {
	if endpoint.Scheme != "https" || endpoint.User != nil {
		return false
	}
	host := strings.ToLower(endpoint.Hostname())
	return host == "youtube.com" || strings.HasSuffix(host, ".youtube.com")
}

func responseError(operation string, resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("youtube: %s returned HTTP %d: %s", operation, resp.StatusCode, strings.TrimSpace(string(data)))
}

func (c *Client) browse(ctx context.Context, browseID string) (*BrowseResult, error) {
	raw, err := c.execute(ctx, "browse", map[string]any{"browseId": browseID})
	if err != nil {
		return nil, err
	}
	return c.newBrowseResult(raw), nil
}
