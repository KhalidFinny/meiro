package youtube

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// SearchType limits search results to one music category. An empty value
// searches all categories.
type SearchType string

const (
	SearchAll       SearchType = ""
	SearchSongs     SearchType = "song"
	SearchVideos    SearchType = "video"
	SearchAlbums    SearchType = "album"
	SearchArtists   SearchType = "artist"
	SearchPlaylists SearchType = "playlist"
)

// SearchOptions controls a music search.
type SearchOptions struct {
	Type SearchType
}

// MusicItem is the common subset of YouTube Music song, video, album, artist,
// and playlist renderers. Raw preserves the complete source renderer.
type MusicItem struct {
	ID         string          `json:"id,omitempty"`
	VideoID    string          `json:"videoId,omitempty"`
	BrowseID   string          `json:"browseId,omitempty"`
	PlaylistID string          `json:"playlistId,omitempty"`
	Title      string          `json:"title,omitempty"`
	Subtitle   string          `json:"subtitle,omitempty"`
	Kind       string          `json:"kind,omitempty"`
	Duration   string          `json:"duration,omitempty"`
	Thumbnail  string          `json:"thumbnail,omitempty"`
	Raw        json.RawMessage `json:"raw,omitempty"`
}

// SearchResult contains the parsed music items and untouched InnerTube result.
type SearchResult struct {
	Items []MusicItem     `json:"items"`
	Raw   json.RawMessage `json:"raw"`
}

// BrowseResult represents a Music browse page such as an artist, album,
// playlist, home feed, explore page, library, or account settings.
type BrowseResult struct {
	Items []MusicItem     `json:"items"`
	Raw   json.RawMessage `json:"raw"`
}

// AccountDetails contains the active account response. Account payloads vary
// by client and sign-in state, so Raw is the authoritative representation.
type AccountDetails struct {
	Raw json.RawMessage `json:"raw"`
}

// TrackInfo contains player metadata and formats for one music track. URLs
// that YouTube returns directly can be played by an audio player; encrypted
// signatureCipher formats are exposed as-is and need player-script deciphering.
type TrackInfo struct {
	VideoDetails  VideoDetails    `json:"videoDetails"`
	StreamingData StreamingData   `json:"streamingData"`
	Playability   Playability     `json:"playabilityStatus"`
	Raw           json.RawMessage `json:"raw"`
}

type VideoDetails struct {
	VideoID   string `json:"videoId"`
	Title     string `json:"title"`
	Author    string `json:"author"`
	Length    string `json:"lengthSeconds"`
	ChannelID string `json:"channelId"`
}

type Playability struct {
	Status   string   `json:"status"`
	Reason   string   `json:"reason"`
	Messages []string `json:"messages"`
}

type StreamingData struct {
	ExpiresInSeconds string        `json:"expiresInSeconds"`
	Formats          []AudioFormat `json:"formats"`
	AdaptiveFormats  []AudioFormat `json:"adaptiveFormats"`
}

// AudioFormat describes a player response format. SignatureCipher may contain
// encrypted URL/signature data and is not directly playable without decoding.
type AudioFormat struct {
	Itag            int    `json:"itag"`
	URL             string `json:"url"`
	MimeType        string `json:"mimeType"`
	Bitrate         int    `json:"bitrate"`
	AverageBitrate  int    `json:"averageBitrate"`
	ContentLength   string `json:"contentLength"`
	AudioQuality    string `json:"audioQuality"`
	SignatureCipher string `json:"signatureCipher"`
	Cipher          string `json:"cipher"`
}

// Search searches YouTube Music. Supported types are song, video, album,
// artist, playlist, and the empty value for all categories.
func (c *Client) Search(ctx context.Context, query string, options SearchOptions) (*SearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("youtube: search query is required")
	}
	payload := map[string]any{"query": query}
	if options.Type != SearchAll {
		params, err := musicSearchParams(options.Type)
		if err != nil {
			return nil, err
		}
		payload["params"] = url.QueryEscape(params)
	}
	raw, err := c.execute(ctx, "search", payload)
	if err != nil {
		return nil, err
	}
	return &SearchResult{Items: extractMusicItems(raw), Raw: raw}, nil
}

func musicSearchParams(kind SearchType) (string, error) {
	field := map[SearchType]byte{
		SearchSongs: 1, SearchVideos: 2, SearchAlbums: 3,
		SearchArtists: 4, SearchPlaylists: 5,
	}[kind]
	if field == 0 {
		return "", fmt.Errorf("youtube: unsupported music search type %q", kind)
	}
	// SearchFilter{filters:{musicSearchType:{<field>:true}}}; protobuf field 17
	// of Filters holds the nested MusicSearchType message.
	inner := []byte{field << 3, 1}
	filters := append([]byte{0x8a, 0x01, byte(len(inner))}, inner...)
	message := append([]byte{0x12, byte(len(filters))}, filters...)
	return base64.StdEncoding.EncodeToString(message), nil
}

func (c *Client) GetHomeFeed(ctx context.Context) (*BrowseResult, error) {
	return c.browse(ctx, "FEmusic_home")
}

func (c *Client) GetExplore(ctx context.Context) (*BrowseResult, error) {
	return c.browse(ctx, "FEmusic_explore")
}

func (c *Client) GetLibrary(ctx context.Context) (*BrowseResult, error) {
	return c.browse(ctx, "FEmusic_library_landing")
}

func (c *Client) GetArtist(ctx context.Context, artistID string) (*BrowseResult, error) {
	if !(strings.HasPrefix(artistID, "UC") || strings.HasPrefix(artistID, "FEmusic_library_privately_owned_artist")) {
		return nil, fmt.Errorf("youtube: invalid artist ID %q", artistID)
	}
	return c.browse(ctx, artistID)
}

func (c *Client) GetAlbum(ctx context.Context, albumID string) (*BrowseResult, error) {
	if !(strings.HasPrefix(albumID, "MPR") || strings.HasPrefix(albumID, "FEmusic_library_privately_owned_release")) {
		return nil, fmt.Errorf("youtube: invalid album ID %q", albumID)
	}
	return c.browse(ctx, albumID)
}

func (c *Client) GetPlaylist(ctx context.Context, playlistID string) (*BrowseResult, error) {
	if playlistID == "" {
		return nil, errors.New("youtube: playlist ID is required")
	}
	if !strings.HasPrefix(playlistID, "VL") {
		playlistID = "VL" + playlistID
	}
	return c.browse(ctx, playlistID)
}

// GetAccountDetails returns the active signed-in account. It requires a
// configured OAuth session; YouTube's OAuth flow reports the active channel.
func (c *Client) GetAccountDetails(ctx context.Context) (*AccountDetails, error) {
	if c.oauth == nil {
		return nil, errors.New("youtube: GetAccountDetails requires OAuth")
	}
	raw, err := c.executeForClient(ctx, "account/accounts_list", map[string]any{}, "TV")
	if err != nil {
		return nil, err
	}
	return &AccountDetails{Raw: raw}, nil
}

// GetAccountSettings returns the account overview page for the signed-in user.
func (c *Client) GetAccountSettings(ctx context.Context) (*BrowseResult, error) {
	if c.oauth == nil {
		return nil, errors.New("youtube: GetAccountSettings requires OAuth")
	}
	raw, err := c.executeForClient(ctx, "browse", map[string]any{"browseId": "SPaccount_overview"}, "WEB")
	if err != nil {
		return nil, err
	}
	return newBrowseResult(raw), nil
}

// GetTrackInfo fetches playback metadata and streaming formats. It does not
// start playback or change account state.
func (c *Client) GetTrackInfo(ctx context.Context, videoID string) (*TrackInfo, error) {
	if strings.TrimSpace(videoID) == "" {
		return nil, errors.New("youtube: video ID is required")
	}
	raw, err := c.execute(ctx, "player", map[string]any{
		"videoId":        videoID,
		"racyCheckOk":    true,
		"contentCheckOk": true,
		"playbackContext": map[string]any{
			"contentPlaybackContext": map[string]any{
				"vis":              0,
				"splay":            false,
				"lactMilliseconds": "-1",
			},
		},
	})
	if err != nil {
		return nil, err
	}
	var response struct {
		VideoDetails  VideoDetails  `json:"videoDetails"`
		StreamingData StreamingData `json:"streamingData"`
		Playability   Playability   `json:"playabilityStatus"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("youtube: decode track info: %w", err)
	}
	return &TrackInfo{
		VideoDetails:  response.VideoDetails,
		StreamingData: response.StreamingData,
		Playability:   response.Playability,
		Raw:           raw,
	}, nil
}

// BestAudioFormat selects the highest-bitrate audio-only format with a direct
// URL. It returns false if the response only contains encrypted URL ciphers.
func (track *TrackInfo) BestAudioFormat() (AudioFormat, bool) {
	var best AudioFormat
	found := false
	for _, format := range track.StreamingData.AdaptiveFormats {
		if !strings.HasPrefix(format.MimeType, "audio/") || format.URL == "" {
			continue
		}
		bitrate := format.AverageBitrate
		if bitrate == 0 {
			bitrate = format.Bitrate
		}
		bestBitrate := best.AverageBitrate
		if bestBitrate == 0 {
			bestBitrate = best.Bitrate
		}
		if !found || bitrate > bestBitrate {
			best, found = format, true
		}
	}
	return best, found
}

// GetUpNext fetches the read-only queue for a track.
func (c *Client) GetUpNext(ctx context.Context, videoID string) (*BrowseResult, error) {
	if strings.TrimSpace(videoID) == "" {
		return nil, errors.New("youtube: video ID is required")
	}
	raw, err := c.execute(ctx, "next", map[string]any{"videoId": videoID})
	if err != nil {
		return nil, err
	}
	return newBrowseResult(raw), nil
}

// GetSearchSuggestions retrieves suggestion content for a partial query.
func (c *Client) GetSearchSuggestions(ctx context.Context, input string) (json.RawMessage, error) {
	if strings.TrimSpace(input) == "" {
		return nil, errors.New("youtube: suggestion input is required")
	}
	return c.execute(ctx, "music/get_search_suggestions", map[string]any{"input": input})
}

func newBrowseResult(raw json.RawMessage) *BrowseResult {
	return &BrowseResult{Items: extractMusicItems(raw), Raw: raw}
}

func extractMusicItems(raw json.RawMessage) []MusicItem {
	var root any
	if json.Unmarshal(raw, &root) != nil {
		return nil
	}
	var items []MusicItem
	seen := make(map[string]struct{})
	var walk func(any)
	walk = func(value any) {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				walk(child)
			}
		case map[string]any:
			keys := make([]string, 0, len(node))
			for key := range node {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				child := node[key]
				kind, ok := rendererKind(key)
				if ok {
					if renderer, ok := child.(map[string]any); ok {
						item := parseMusicItem(kind, renderer)
						identity := item.VideoID + item.BrowseID + item.PlaylistID + item.Title
						if identity != "" {
							if _, exists := seen[identity]; !exists {
								seen[identity] = struct{}{}
								items = append(items, item)
							}
						}
					}
				}
				walk(child)
			}
		}
	}
	walk(root)
	return items
}

func rendererKind(key string) (string, bool) {
	switch key {
	case "musicResponsiveListItemRenderer":
		return "track", true
	case "musicTwoRowItemRenderer":
		return "music_item", true
	case "musicVideoRenderer":
		return "video", true
	case "videoRenderer":
		return "video", true
	case "playlistVideoRenderer", "playlistPanelVideoRenderer":
		return "track", true
	case "gridPlaylistRenderer", "musicPlaylistShelfRenderer":
		return "playlist", true
	case "gridAlbumRenderer":
		return "album", true
	case "gridArtistRenderer":
		return "artist", true
	default:
		return "", false
	}
}

func parseMusicItem(kind string, renderer map[string]any) MusicItem {
	item := MusicItem{Kind: kind}
	item.Title = rendererText(renderer["title"])
	if item.Title == "" {
		item.Title = rendererText(renderer["headline"])
	}
	if item.Title == "" {
		item.Title = rendererColumnText(renderer, "flexColumns", 0)
	}
	item.Subtitle = rendererText(renderer["subtitle"])
	if item.Subtitle == "" {
		item.Subtitle = rendererColumnText(renderer, "flexColumns", 1)
	}
	item.Duration = rendererText(renderer["lengthText"])
	if item.Duration == "" {
		item.Duration = rendererColumnText(renderer, "fixedColumns", 0)
	}
	item.VideoID, _ = renderer["videoId"].(string)
	item.PlaylistID, _ = renderer["playlistId"].(string)
	item.BrowseID = navigationID(renderer["navigationEndpoint"])
	if item.VideoID != "" {
		item.ID = item.VideoID
	} else if item.BrowseID != "" {
		item.ID = item.BrowseID
	} else {
		item.ID = item.PlaylistID
	}
	if item.Thumbnail == "" {
		item.Thumbnail = rendererThumbnail(renderer["thumbnail"])
	}
	data, _ := json.Marshal(renderer)
	item.Raw = data
	return item
}

func rendererText(value any) string {
	object, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	if text, ok := object["simpleText"].(string); ok {
		return text
	}
	runs, ok := object["runs"].([]any)
	if !ok {
		return ""
	}
	var builder strings.Builder
	for _, value := range runs {
		if run, ok := value.(map[string]any); ok {
			if text, ok := run["text"].(string); ok {
				builder.WriteString(text)
			}
		}
	}
	return builder.String()
}

func rendererColumnText(renderer map[string]any, columnName string, index int) string {
	columns, ok := renderer[columnName].([]any)
	if !ok || index >= len(columns) {
		return ""
	}
	column, ok := columns[index].(map[string]any)
	if !ok {
		return ""
	}
	columnRenderer, ok := column["musicResponsiveListItemFlexColumnRenderer"].(map[string]any)
	if !ok {
		return ""
	}
	return rendererText(columnRenderer["text"])
}

func navigationID(value any) string {
	var result string
	var walk func(any)
	walk = func(current any) {
		if result != "" {
			return
		}
		switch node := current.(type) {
		case []any:
			for _, child := range node {
				walk(child)
			}
		case map[string]any:
			for _, name := range []string{"browseId", "playlistId"} {
				if value, ok := node[name].(string); ok && value != "" {
					result = value
					return
				}
			}
			keys := make([]string, 0, len(node))
			for key := range node {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				walk(node[key])
			}
		}
	}
	walk(value)
	return result
}

func rendererThumbnail(value any) string {
	var result string
	var walk func(any)
	walk = func(current any) {
		if result != "" {
			return
		}
		switch node := current.(type) {
		case []any:
			for _, child := range node {
				walk(child)
			}
		case map[string]any:
			if thumbnails, ok := node["thumbnails"].([]any); ok && len(thumbnails) > 0 {
				if thumbnail, ok := thumbnails[len(thumbnails)-1].(map[string]any); ok {
					result, _ = thumbnail["url"].(string)
				}
			}
			for _, child := range node {
				walk(child)
			}
		}
	}
	walk(value)
	return result
}
