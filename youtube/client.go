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
	OAuth            *OAuth
	CookieAuth       *CookieAuth
}

// Client issues read-only YouTube Music requests.
type Client struct {
	httpClient       *http.Client
	baseURL          string
	musicURL         string
	apiKey           string
	clientVersion    string
	webClientVersion string
	tvClientVersion  string
	visitorData      string
	language         string
	country          string
	oauth            *OAuth
	cookieAuth       *CookieAuth

	configMu sync.Mutex
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
		httpClient:       options.HTTPClient,
		baseURL:          strings.TrimRight(options.BaseURL, "/"),
		musicURL:         strings.TrimRight(options.MusicURL, "/"),
		apiKey:           options.APIKey,
		clientVersion:    options.ClientVersion,
		webClientVersion: options.WebClientVersion,
		tvClientVersion:  options.TVClientVersion,
		visitorData:      options.VisitorData,
		language:         options.Language,
		country:          options.Country,
		oauth:            options.OAuth,
		cookieAuth:       options.CookieAuth,
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
	ctx.Client.VisitorData = c.visitorData
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
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("youtube: load Music configuration: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return responseError("load Music configuration", resp)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("youtube: read Music configuration: %w", err)
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

func (c *Client) execute(ctx context.Context, endpoint string, payload map[string]any) (json.RawMessage, error) {
	return c.executeForClient(ctx, endpoint, payload, defaultMusicClient)
}

func (c *Client) executeForClient(ctx context.Context, endpoint string, payload map[string]any, client string) (json.RawMessage, error) {
	if err := c.ensureConfig(ctx); err != nil {
		return nil, err
	}
	clientName, clientID, clientVersion := "", "", ""
	switch client {
	case "YTMUSIC":
		clientName, clientID, clientVersion = defaultMusicContext, defaultMusicClientID, c.clientVersion
	case "WEB":
		clientName, clientID, clientVersion = "WEB", "1", c.webClientVersion
	case "TV":
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
	delete(payload, "client")
	if client == "YTMUSIC" {
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
	if c.visitorData != "" {
		req.Header.Set("X-Goog-Visitor-Id", c.visitorData)
	}
	if client == "TV" {
		req.Header.Set("User-Agent", "Mozilla/5.0 (ChromiumStylePlatform) Cobalt/Version")
	}
	if c.cookieAuth != nil {
		req.Header.Set("Cookie", c.cookieAuth.cookie)
		req.Header.Set("Authorization", c.cookieAuth.authorization(time.Now()))
		req.Header.Set("X-Goog-Authuser", fmt.Sprint(c.cookieAuth.accountIndex))
		if c.cookieAuth.onBehalfOfUser != "" {
			req.Header.Set("X-Goog-PageId", c.cookieAuth.onBehalfOfUser)
		}
	} else if c.oauth != nil {
		token, err := c.oauth.AccessToken(ctx)
		if err != nil {
			return nil, fmt.Errorf("youtube: get OAuth access token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
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
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("youtube: decode %s response: %w", endpoint, err)
	}
	return result, nil
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
	return newBrowseResult(raw), nil
}
