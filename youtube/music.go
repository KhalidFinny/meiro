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
	Items             []MusicItem     `json:"items"`
	ContinuationToken string          `json:"continuationToken,omitempty"`
	Raw               json.RawMessage `json:"raw"`
}

// BrowseResult represents a Music browse page such as an artist, album,
// playlist, home feed, explore page, library, or account settings.
type BrowseResult struct {
	Items             []MusicItem       `json:"items"`
	Sections          []MusicSection    `json:"sections,omitempty"`
	ContinuationToken string            `json:"continuationToken,omitempty"`
	Pages             []json.RawMessage `json:"pages,omitempty"`
	Raw               json.RawMessage   `json:"raw"`
}

// MusicSection is a shelf or grid in a browse response. ContinuationToken can
// be passed to ContinueBrowse to load more items in that section.
type MusicSection struct {
	Title             string          `json:"title,omitempty"`
	Kind              string          `json:"kind"`
	Items             []MusicItem     `json:"items"`
	ContinuationToken string          `json:"continuationToken,omitempty"`
	Raw               json.RawMessage `json:"raw"`
}

// AccountDetails contains the active account response. Account payloads vary
// by client and sign-in state, so Raw is the authoritative representation.
type AccountDetails struct {
	Name      string          `json:"name,omitempty"`
	Email     string          `json:"email,omitempty"`
	ChannelID string          `json:"channelId,omitempty"`
	Thumbnail string          `json:"thumbnail,omitempty"`
	Raw       json.RawMessage `json:"raw"`
}

// AccountChannel describes a channel in the account switcher.
type AccountChannel struct {
	Name       string          `json:"name,omitempty"`
	Byline     string          `json:"byline,omitempty"`
	Handle     string          `json:"handle,omitempty"`
	ChannelID  string          `json:"channelId,omitempty"`
	Thumbnail  string          `json:"thumbnail,omitempty"`
	Selected   bool            `json:"selected,omitempty"`
	Disabled   bool            `json:"disabled,omitempty"`
	HasChannel bool            `json:"hasChannel,omitempty"`
	Raw        json.RawMessage `json:"raw"`
}

// AccountList contains channels available to a cookie-authenticated account.
type AccountList struct {
	Items []AccountChannel `json:"items"`
	Raw   json.RawMessage  `json:"raw"`
}

// TrackInfo contains player metadata and formats for one music track. URLs
// that YouTube returns directly can be played by an audio player; encrypted
// signatureCipher formats are exposed as-is and need player-script deciphering.
type TrackInfo struct {
	VideoDetails  VideoDetails    `json:"videoDetails"`
	StreamingData StreamingData   `json:"streamingData"`
	Playability   Playability     `json:"playabilityStatus"`
	Player        PlayerMetadata  `json:"player"`
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
	return &SearchResult{
		Items: extractMusicItems(raw), ContinuationToken: continuationToken(raw), Raw: raw,
	}, nil
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

// GetAllLibrary loads every section and continuation page available from the
// library landing page. YouTube may still omit data based on account, region,
// or subscription access.
func (c *Client) GetAllLibrary(ctx context.Context) (*BrowseResult, error) {
	result, err := c.GetLibrary(ctx)
	if err != nil {
		return nil, err
	}
	queue := browseContinuations(result)
	seen := make(map[string]struct{})
	for len(queue) > 0 {
		token := queue[0]
		queue = queue[1:]
		if _, exists := seen[token]; exists {
			continue
		}
		seen[token] = struct{}{}
		if len(seen) > 1000 {
			return nil, errors.New("youtube: library exceeded 1000 continuation pages")
		}
		page, err := c.ContinueBrowse(ctx, token)
		if err != nil {
			return nil, err
		}
		result.Items = append(result.Items, page.Items...)
		result.Sections = append(result.Sections, page.Sections...)
		result.Pages = append(result.Pages, page.Raw)
		queue = append(queue, browseContinuations(page)...)
	}
	result.ContinuationToken = ""
	for index := range result.Sections {
		result.Sections[index].ContinuationToken = ""
	}
	return result, nil
}

// ContinueBrowse requests the next page for a browse or library section.
func (c *Client) ContinueBrowse(ctx context.Context, token string) (*BrowseResult, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("youtube: browse continuation token is required")
	}
	raw, err := c.execute(ctx, "browse", map[string]any{"continuation": token})
	if err != nil {
		return nil, err
	}
	return newBrowseResult(raw), nil
}

// ContinueSearch requests the next page from SearchResult.ContinuationToken.
func (c *Client) ContinueSearch(ctx context.Context, token string) (*SearchResult, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("youtube: search continuation token is required")
	}
	raw, err := c.execute(ctx, "search", map[string]any{"continuation": token})
	if err != nil {
		return nil, err
	}
	return &SearchResult{
		Items: extractMusicItems(raw), ContinuationToken: continuationToken(raw), Raw: raw,
	}, nil
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
	if c.oauth == nil && c.cookieAuth == nil {
		return nil, errors.New("youtube: GetAccountDetails requires OAuth or cookie authentication")
	}
	raw, err := c.executeForClient(ctx, "account/accounts_list", map[string]any{}, "TV")
	if err != nil {
		return nil, err
	}
	details := &AccountDetails{Raw: raw}
	if channels := extractAccountChannels(raw); len(channels) > 0 {
		selected := channels[0]
		for _, channel := range channels {
			if channel.Selected {
				selected = channel
				break
			}
		}
		details.Name = selected.Name
		details.ChannelID = selected.ChannelID
		details.Thumbnail = selected.Thumbnail
	}
	var direct struct {
		Name      string `json:"accountName"`
		Email     string `json:"email"`
		ChannelID string `json:"channelId"`
		Thumbnail string `json:"thumbnail"`
	}
	_ = json.Unmarshal(raw, &direct)
	if details.Name == "" {
		details.Name = direct.Name
	}
	details.Email = direct.Email
	if details.ChannelID == "" {
		details.ChannelID = direct.ChannelID
	}
	if details.Thumbnail == "" {
		details.Thumbnail = direct.Thumbnail
	}
	return details, nil
}

// GetAccounts lists all channels available to a cookie-authenticated account.
// YouTube's OAuth flow returns only the active channel.
func (c *Client) GetAccounts(ctx context.Context) (*AccountList, error) {
	if c.cookieAuth == nil {
		return nil, errors.New("youtube: GetAccounts requires cookie authentication")
	}
	raw, err := c.executeForClient(ctx, "account/accounts_list", map[string]any{
		"requestType":      "ACCOUNTS_LIST_REQUEST_TYPE_CHANNEL_SWITCHER",
		"callCircumstance": "SWITCHING_USERS_FULL",
	}, "WEB")
	if err != nil {
		return nil, err
	}
	return &AccountList{Items: extractAccountChannels(raw), Raw: raw}, nil
}

// GetAccountSettings returns the account overview page for the signed-in user.
func (c *Client) GetAccountSettings(ctx context.Context) (*BrowseResult, error) {
	if c.oauth == nil && c.cookieAuth == nil {
		return nil, errors.New("youtube: GetAccountSettings requires OAuth or cookie authentication")
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
	player, err := c.playerMetadata(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := c.execute(ctx, "player", map[string]any{
		"videoId":        videoID,
		"racyCheckOk":    true,
		"contentCheckOk": true,
		"playbackContext": map[string]any{
			"contentPlaybackContext": map[string]any{
				"vis":                0,
				"splay":              false,
				"lactMilliseconds":   "-1",
				"signatureTimestamp": player.SignatureTimestamp,
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
		Player:        player,
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
	return &BrowseResult{
		Items: extractMusicItems(raw), Sections: extractMusicSections(raw),
		ContinuationToken: continuationToken(raw), Pages: []json.RawMessage{raw}, Raw: raw,
	}
}

func browseContinuations(result *BrowseResult) []string {
	tokens := make([]string, 0, len(result.Sections)+1)
	if result.ContinuationToken != "" {
		tokens = append(tokens, result.ContinuationToken)
	}
	for _, section := range result.Sections {
		if section.ContinuationToken != "" {
			tokens = append(tokens, section.ContinuationToken)
		}
	}
	return tokens
}

func continuationToken(raw json.RawMessage) string {
	var root any
	if json.Unmarshal(raw, &root) != nil {
		return ""
	}
	var walk func(any) string
	walk = func(value any) string {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				if token := walk(child); token != "" {
					return token
				}
			}
		case map[string]any:
			if command, ok := node["continuationCommand"].(map[string]any); ok {
				if token, ok := command["token"].(string); ok && token != "" {
					return token
				}
			}
			if next, ok := node["nextContinuationData"].(map[string]any); ok {
				if token, ok := next["continuation"].(string); ok && token != "" {
					return token
				}
			}
			if token, ok := node["continuation"].(string); ok && token != "" {
				return token
			}
			keys := sortedKeys(node)
			for _, key := range keys {
				if token := walk(node[key]); token != "" {
					return token
				}
			}
		}
		return ""
	}
	return walk(root)
}

func extractMusicSections(raw json.RawMessage) []MusicSection {
	var root any
	if json.Unmarshal(raw, &root) != nil {
		return nil
	}
	var sections []MusicSection
	var walk func(any)
	walk = func(value any) {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				walk(child)
			}
		case map[string]any:
			for _, key := range sortedKeys(node) {
				child := node[key]
				if key == "musicShelfRenderer" || key == "musicPlaylistShelfRenderer" || key == "gridRenderer" || key == "musicCarouselShelfRenderer" {
					if renderer, ok := child.(map[string]any); ok {
						data, _ := json.Marshal(renderer)
						sections = append(sections, MusicSection{
							Title: rendererText(renderer["title"]), Kind: key,
							Items: extractMusicItems(data), ContinuationToken: continuationToken(data), Raw: data,
						})
					}
					continue
				}
				walk(child)
			}
		}
	}
	walk(root)
	return sections
}

func extractAccountChannels(raw json.RawMessage) []AccountChannel {
	var root any
	if json.Unmarshal(raw, &root) != nil {
		return nil
	}
	var channels []AccountChannel
	seen := make(map[string]struct{})
	appendChannel := func(channel AccountChannel) {
		identity := channel.ChannelID + channel.Name + channel.Handle
		if identity == "" {
			return
		}
		if _, exists := seen[identity]; exists {
			return
		}
		seen[identity] = struct{}{}
		channels = append(channels, channel)
	}
	var walk func(any)
	walk = func(value any) {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				walk(child)
			}
		case map[string]any:
			if hasAccountChannelFields(node) {
				appendChannel(parseAccountChannel(node))
				return
			}
			for _, key := range sortedKeys(node) {
				child, ok := node[key].(map[string]any)
				if !ok {
					walk(node[key])
					continue
				}
				if key == "accountItemRenderer" || key == "accountItem" {
					appendChannel(parseAccountChannel(child))
					continue
				}
				if hasAccountChannelFields(child) {
					appendChannel(parseAccountChannel(child))
					continue
				}
				walk(child)
			}
		}
	}
	walk(root)
	return channels
}

func parseAccountChannel(renderer map[string]any) AccountChannel {
	channel := AccountChannel{
		Name:       rendererText(renderer["accountName"]),
		Byline:     rendererText(renderer["accountByline"]),
		Handle:     rendererText(renderer["channelHandle"]),
		Thumbnail:  rendererThumbnail(renderer["accountPhoto"]),
		Selected:   rendererBool(renderer["isSelected"]),
		Disabled:   rendererBool(renderer["isDisabled"]),
		HasChannel: rendererBool(renderer["hasChannel"]),
		ChannelID:  navigationID(renderer["endpoint"]),
	}
	if channel.Name == "" {
		channel.Name = rendererText(renderer["account_name"])
	}
	if channel.Name == "" {
		channel.Name = rendererText(renderer["name"])
	}
	if channel.ChannelID == "" {
		channel.ChannelID, _ = renderer["channelId"].(string)
	}
	if channel.Handle == "" {
		channel.Handle, _ = renderer["handle"].(string)
	}
	if channel.Handle == "" {
		channel.Handle, _ = renderer["channel_handle"].(string)
	}
	if channel.Byline == "" {
		channel.Byline = rendererText(renderer["account_byline"])
	}
	if channel.Thumbnail == "" {
		channel.Thumbnail = rendererThumbnail(renderer["account_photo"])
	}
	data, _ := json.Marshal(renderer)
	channel.Raw = data
	return channel
}

func hasAccountChannelFields(value map[string]any) bool {
	for _, key := range []string{"channelId", "accountName", "account_name", "channelHandle", "channel_handle"} {
		if value[key] != nil {
			return true
		}
	}
	return false
}

func rendererBool(value any) bool {
	result, _ := value.(bool)
	return result
}

func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
	case "gridPlaylistRenderer":
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
	if text, ok := value.(string); ok {
		return text
	}
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
