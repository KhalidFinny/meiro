package youtube

import (
	"context"
	"crypto/rand"
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
	defaultYouTubeURL = "https://www.youtube.com"
	youtubeMusicScope = "http://gdata.youtube.com https://www.googleapis.com/auth/youtube-paid-content"
)

var (
	tvScriptPattern       = regexp.MustCompile(`<script\s+id="base-js"\s+src="([^"]+)"[^>]*></script>`)
	clientIdentityPattern = regexp.MustCompile(`clientId:"([^"]+)",[^,]*?:"([^"]+)"`)
)

// OAuthClientCredentials are the client ID and secret used by YouTube's
// device authorization flow. The secret is an OAuth client secret, not a user
// password.
type OAuthClientCredentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// Tokens contains the user's OAuth credentials. Keep these values private.
type Tokens struct {
	AccessToken  string                  `json:"access_token"`
	RefreshToken string                  `json:"refresh_token"`
	ExpiryDate   time.Time               `json:"expiry_date"`
	ExpiresIn    int                     `json:"expires_in,omitempty"`
	Scope        string                  `json:"scope,omitempty"`
	TokenType    string                  `json:"token_type,omitempty"`
	Client       *OAuthClientCredentials `json:"client,omitempty"`
}

// DeviceCode is shown to the user to complete OAuth in a browser.
type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// OAuthConfig configures YouTube device authorization. Client ID and secret
// may be omitted; BeginDeviceFlow then discovers them from YouTube TV.
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	HTTPClient   *http.Client
	BaseURL      string
}

// OAuth manages device authorization and refresh tokens.
type OAuth struct {
	httpClient *http.Client
	baseURL    string

	mu          sync.Mutex
	credentials OAuthClientCredentials
	tokens      *Tokens
}

// NewOAuth creates an OAuth manager. It does not contact YouTube.
func NewOAuth(config OAuthConfig) *OAuth {
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if config.BaseURL == "" {
		config.BaseURL = defaultYouTubeURL
	}
	return &OAuth{
		httpClient: config.HTTPClient,
		baseURL:    strings.TrimRight(config.BaseURL, "/"),
		credentials: OAuthClientCredentials{
			ClientID: config.ClientID, ClientSecret: config.ClientSecret,
		},
	}
}

// SetTokens replaces the current token set. Tokens can be persisted by the
// caller and restored with this method on a later run.
func (o *OAuth) SetTokens(tokens Tokens) error {
	if tokens.ExpiresIn > 0 {
		tokens.ExpiryDate = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
		tokens.ExpiresIn = 0
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" || tokens.ExpiryDate.IsZero() {
		return errors.New("youtube: OAuth tokens require access_token, refresh_token, and expiry_date")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if tokens.Client != nil {
		client := *tokens.Client
		tokens.Client = &client
		o.credentials = client
	}
	copy := tokens
	o.tokens = &copy
	return nil
}

// Tokens returns a copy of the current credentials, if any.
func (o *OAuth) Tokens() (Tokens, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.tokens == nil {
		return Tokens{}, false
	}
	copy := *o.tokens
	if o.tokens.Client != nil {
		client := *o.tokens.Client
		copy.Client = &client
	}
	return copy, true
}

// BeginDeviceFlow requests a user code. The caller should display UserCode and
// VerificationURL, then call PollForTokens.
func (o *OAuth) BeginDeviceFlow(ctx context.Context) (DeviceCode, error) {
	o.mu.Lock()
	credentials := o.credentials
	o.mu.Unlock()
	if credentials.ClientID == "" || credentials.ClientSecret == "" {
		var err error
		credentials, err = o.discoverCredentials(ctx)
		if err != nil {
			return DeviceCode{}, err
		}
		o.mu.Lock()
		o.credentials = credentials
		o.mu.Unlock()
	}
	deviceID, err := randomDeviceID()
	if err != nil {
		return DeviceCode{}, fmt.Errorf("youtube: create OAuth device ID: %w", err)
	}
	request := map[string]any{
		"client_id":    credentials.ClientID,
		"scope":        youtubeMusicScope,
		"device_id":    deviceID,
		"device_model": "ytlr::",
	}
	var code DeviceCode
	if err := o.postJSON(ctx, "/o/oauth2/device/code", request, &code); err != nil {
		return DeviceCode{}, fmt.Errorf("youtube: request OAuth device code: %w", err)
	}
	if code.DeviceCode == "" || code.UserCode == "" || code.ExpiresIn <= 0 {
		return DeviceCode{}, errors.New("youtube: device authorization response was incomplete")
	}
	if code.Interval <= 0 {
		code.Interval = 5
	}
	return code, nil
}

// PollForTokens waits for browser authorization and returns the resulting
// tokens. It stops when the context is canceled or the device code expires.
func (o *OAuth) PollForTokens(ctx context.Context, code DeviceCode) (Tokens, error) {
	o.mu.Lock()
	credentials := o.credentials
	o.mu.Unlock()
	if credentials.ClientID == "" || credentials.ClientSecret == "" {
		return Tokens{}, errors.New("youtube: OAuth client credentials are missing")
	}
	if code.DeviceCode == "" || code.ExpiresIn <= 0 {
		return Tokens{}, errors.New("youtube: invalid OAuth device code")
	}
	interval := time.Duration(code.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.NewTimer(time.Duration(code.ExpiresIn) * time.Second)
	defer deadline.Stop()
	for {
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Tokens{}, ctx.Err()
		case <-deadline.C:
			timer.Stop()
			return Tokens{}, errors.New("youtube: OAuth device code expired")
		case <-timer.C:
		}

		var response struct {
			Tokens
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		request := map[string]string{
			"client_id":     credentials.ClientID,
			"client_secret": credentials.ClientSecret,
			"code":          code.DeviceCode,
			"grant_type":    "http://oauth.net/grant_type/device/1.0",
		}
		err := o.postJSON(ctx, "/o/oauth2/token", request, &response)
		if err != nil {
			var oauthErr *oauthResponseError
			if errors.As(err, &oauthErr) {
				switch oauthErr.Code {
				case "authorization_pending":
					continue
				case "slow_down":
					interval += 5 * time.Second
					continue
				case "access_denied":
					return Tokens{}, errors.New("youtube: OAuth authorization was denied")
				case "expired_token":
					return Tokens{}, errors.New("youtube: OAuth device code expired")
				}
			}
			return Tokens{}, fmt.Errorf("youtube: poll OAuth device code: %w", err)
		}
		tokens := response.Tokens
		if tokens.AccessToken == "" {
			return Tokens{}, errors.New("youtube: OAuth token response did not include an access token")
		}
		if tokens.ExpiresIn > 0 {
			tokens.ExpiryDate = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
			tokens.ExpiresIn = 0
		}
		if tokens.RefreshToken == "" {
			return Tokens{}, errors.New("youtube: OAuth token response did not include a refresh token")
		}
		client := credentials
		tokens.Client = &client
		if err := o.SetTokens(tokens); err != nil {
			return Tokens{}, err
		}
		return tokens, nil
	}
}

// AccessToken returns a valid access token, refreshing it when it expires
// within the next 30 seconds.
func (o *OAuth) AccessToken(ctx context.Context) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.tokens == nil {
		return "", errors.New("youtube: not signed in")
	}
	if time.Until(o.tokens.ExpiryDate) <= 30*time.Second {
		if err := o.refreshLocked(ctx); err != nil {
			return "", err
		}
	}
	return o.tokens.AccessToken, nil
}

// Refresh forces an access-token refresh.
func (o *OAuth) Refresh(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.tokens == nil {
		return errors.New("youtube: no OAuth tokens to refresh")
	}
	return o.refreshLocked(ctx)
}

func (o *OAuth) refreshLocked(ctx context.Context) error {
	if o.credentials.ClientID == "" || o.credentials.ClientSecret == "" {
		return errors.New("youtube: OAuth client credentials are missing")
	}
	request := map[string]string{
		"client_id":     o.credentials.ClientID,
		"client_secret": o.credentials.ClientSecret,
		"refresh_token": o.tokens.RefreshToken,
		"grant_type":    "refresh_token",
	}
	var response struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Error        string `json:"error"`
	}
	if err := o.postJSON(ctx, "/o/oauth2/token", request, &response); err != nil {
		return fmt.Errorf("youtube: refresh OAuth token: %w", err)
	}
	if response.AccessToken == "" || response.ExpiresIn <= 0 {
		return errors.New("youtube: invalid OAuth refresh response")
	}
	o.tokens.AccessToken = response.AccessToken
	o.tokens.ExpiryDate = time.Now().Add(time.Duration(response.ExpiresIn) * time.Second)
	if response.RefreshToken != "" {
		o.tokens.RefreshToken = response.RefreshToken
	}
	return nil
}

// Revoke revokes the current access token. Callers control when this
// credential-changing operation occurs.
func (o *OAuth) Revoke(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.tokens == nil {
		return errors.New("youtube: no OAuth token to revoke")
	}
	token := o.tokens.AccessToken
	endpoint := o.baseURL + "/o/oauth2/revoke?token=" + url.QueryEscape(token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := o.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("youtube: revoke OAuth token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return responseError("revoke OAuth token", resp)
	}
	o.tokens = nil
	return nil
}

func (o *OAuth) postJSON(ctx context.Context, path string, payload any, output any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+path, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var result map[string]json.RawMessage
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if len(data) > 0 {
		_ = json.Unmarshal(data, &result)
	}
	if result != nil {
		var oauthErr oauthResponseError
		if raw := result["error"]; len(raw) > 0 {
			_ = json.Unmarshal(raw, &oauthErr.Code)
			if rawDescription := result["error_description"]; len(rawDescription) > 0 {
				_ = json.Unmarshal(rawDescription, &oauthErr.Description)
			}
			return &oauthErr
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

type oauthResponseError struct {
	Code        string
	Description string
}

func (e *oauthResponseError) Error() string {
	if e.Description != "" {
		return e.Code + ": " + e.Description
	}
	return e.Code
}

func (o *OAuth) discoverCredentials(ctx context.Context) (OAuthClientCredentials, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.baseURL+"/tv", nil)
	if err != nil {
		return OAuthClientCredentials{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (ChromiumStylePlatform) Cobalt/Version")
	req.Header.Set("Referer", o.baseURL+"/tv")
	req.Header.Set("Accept-Language", "en-US")
	resp, err := o.httpClient.Do(req)
	if err != nil {
		return OAuthClientCredentials{}, fmt.Errorf("youtube: fetch TV app for OAuth credentials: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return OAuthClientCredentials{}, responseError("fetch TV app", resp)
	}
	html, err := io.ReadAll(resp.Body)
	if err != nil {
		return OAuthClientCredentials{}, err
	}
	script := tvScriptPattern.FindSubmatch(html)
	if len(script) < 2 {
		return OAuthClientCredentials{}, errors.New("youtube: TV app did not expose its base-js URL; pass OAuth client credentials explicitly")
	}
	scriptURL := string(script[1])
	if strings.HasPrefix(scriptURL, "//") {
		scriptURL = "https:" + scriptURL
	} else if strings.HasPrefix(scriptURL, "/") {
		scriptURL = o.baseURL + scriptURL
	}
	scriptReq, err := http.NewRequestWithContext(ctx, http.MethodGet, scriptURL, nil)
	if err != nil {
		return OAuthClientCredentials{}, err
	}
	scriptResp, err := o.httpClient.Do(scriptReq)
	if err != nil {
		return OAuthClientCredentials{}, fmt.Errorf("youtube: fetch TV base script: %w", err)
	}
	defer scriptResp.Body.Close()
	if scriptResp.StatusCode < 200 || scriptResp.StatusCode >= 300 {
		return OAuthClientCredentials{}, responseError("fetch TV base script", scriptResp)
	}
	js, err := io.ReadAll(scriptResp.Body)
	if err != nil {
		return OAuthClientCredentials{}, err
	}
	match := clientIdentityPattern.FindSubmatch(js)
	if len(match) < 3 {
		return OAuthClientCredentials{}, errors.New("youtube: OAuth client credentials not found in TV base script")
	}
	return OAuthClientCredentials{ClientID: string(match[1]), ClientSecret: string(match[2])}, nil
}

func randomDeviceID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16]), nil
}
