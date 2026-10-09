package youtube

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const playerCacheTTL = 5 * time.Minute

// maxScriptBytes is the most of the player script, which is a few megabytes,
// the client will read.
const maxScriptBytes = 16 << 20

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

type playerScript struct {
	metadata    PlayerMetadata
	source      []byte
	expiresAt   time.Time
	decoderOnce sync.Once
	decoder     *playerDecipher
}

func (p *playerScript) decipher() *playerDecipher {
	p.decoderOnce.Do(func() {
		p.decoder = newPlayerDecipher(p.source)
	})
	return p.decoder
}

func (c *Client) loadPlayer(ctx context.Context) (*playerScript, error) {
	c.playerMu.Lock()
	defer c.playerMu.Unlock()
	if c.player != nil && time.Now().Before(c.player.expiresAt) {
		return c.player, nil
	}
	iframe, err := c.getText(ctx, c.baseURL+"/iframe_api")
	if err != nil {
		return nil, fmt.Errorf("youtube: load player iframe API: %w", err)
	}
	normalized := strings.ReplaceAll(string(iframe), `\\/`, "/")
	normalized = strings.ReplaceAll(normalized, `\/`, "/")
	match := playerIDPattern.FindStringSubmatch(normalized)
	if len(match) < 2 {
		return nil, errors.New("youtube: player ID not found in iframe API")
	}
	playerID := match[1]
	// The script is megabytes and what is taken from it is slow to extract,
	// so a player the client already holds stays for as long as YouTube
	// serves it, and only its expiry moves.
	if c.player != nil && c.player.metadata.PlayerID == playerID {
		c.player.expiresAt = time.Now().Add(playerCacheTTL)
		return c.player, nil
	}
	scriptURL := fmt.Sprintf("%s/s/player/%s/player_es6.vflset/en_US/base.js", c.baseURL, playerID)
	script, err := c.getText(ctx, scriptURL)
	if err != nil {
		return nil, fmt.Errorf("youtube: load player script: %w", err)
	}
	timestampMatch := playerTimestampPattern.FindSubmatch(script)
	if len(timestampMatch) < 2 {
		return nil, errors.New("youtube: signature timestamp not found in player script")
	}
	timestamp, err := strconv.Atoi(string(timestampMatch[1]))
	if err != nil {
		return nil, fmt.Errorf("youtube: parse signature timestamp: %w", err)
	}
	metadata := PlayerMetadata{PlayerID: playerID, SignatureTimestamp: timestamp, ScriptURL: scriptURL}
	c.player = &playerScript{metadata: metadata, source: script, expiresAt: time.Now().Add(playerCacheTTL)}
	return c.player, nil
}

func (c *Client) getText(ctx context.Context, endpoint string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, responseError("load player metadata", response)
	}
	// One byte more than the limit tells a script that fits from one that was
	// cut short, which would only fail later as something not found in it.
	body, err := io.ReadAll(io.LimitReader(response.Body, maxScriptBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxScriptBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", endpoint, maxScriptBytes)
	}
	return body, nil
}
