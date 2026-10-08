package youtube

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

var (
	playerIDPattern        = regexp.MustCompile(`player/([A-Za-z0-9._-]+)/`)
	playerTimestampPattern = regexp.MustCompile(`signatureTimestamp\s*:\s*(\d+)`)
)

// PlayerMetadata contains the current YouTube player identifier and request
// signature timestamp. The client discovers and caches it on first playback
// metadata request.
type PlayerMetadata struct {
	PlayerID           string `json:"playerId"`
	SignatureTimestamp int    `json:"signatureTimestamp"`
	ScriptURL          string `json:"scriptUrl"`
}

func (c *Client) playerMetadata(ctx context.Context) (PlayerMetadata, error) {
	c.playerMu.Lock()
	defer c.playerMu.Unlock()
	if c.player != nil {
		return *c.player, nil
	}
	iframe, err := c.getText(ctx, c.baseURL+"/iframe_api")
	if err != nil {
		return PlayerMetadata{}, fmt.Errorf("youtube: load player iframe API: %w", err)
	}
	normalized := strings.ReplaceAll(iframe, `\\/`, "/")
	normalized = strings.ReplaceAll(normalized, `\/`, "/")
	match := playerIDPattern.FindStringSubmatch(normalized)
	if len(match) < 2 {
		return PlayerMetadata{}, errors.New("youtube: player ID not found in iframe API")
	}
	playerID := match[1]
	scriptURL := fmt.Sprintf("%s/s/player/%s/player_es6.vflset/en_US/base.js", c.baseURL, playerID)
	script, err := c.getText(ctx, scriptURL)
	if err != nil {
		return PlayerMetadata{}, fmt.Errorf("youtube: load player script: %w", err)
	}
	timestampMatch := playerTimestampPattern.FindStringSubmatch(script)
	if len(timestampMatch) < 2 {
		return PlayerMetadata{}, errors.New("youtube: signature timestamp not found in player script")
	}
	var timestamp int
	if _, err := fmt.Sscan(timestampMatch[1], &timestamp); err != nil {
		return PlayerMetadata{}, fmt.Errorf("youtube: parse signature timestamp: %w", err)
	}
	metadata := PlayerMetadata{PlayerID: playerID, SignatureTimestamp: timestamp, ScriptURL: scriptURL}
	c.player = &metadata
	return metadata, nil
}

func (c *Client) getText(ctx context.Context, endpoint string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", responseError("load player metadata", response)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return "", err
	}
	return string(body), nil
}
