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
// and playlist renderers. Raw preserves the complete source renderer when
// Options.KeepRenderers is set, and is empty otherwise.
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
	Items             []MusicItem    `json:"items"`
	Sections          []MusicSection `json:"sections,omitempty"`
	ContinuationToken string         `json:"continuationToken,omitempty"`
	// QueuePlaylistID is the playlist panel's ID, used to continue radio queues.
	QueuePlaylistID string            `json:"queuePlaylistId,omitempty"`
	Pages           []json.RawMessage `json:"pages,omitempty"`
	Raw             json.RawMessage   `json:"raw"`
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

// Lyrics contains the description shelf returned by YouTube Music for a track.
type Lyrics struct {
	Description string          `json:"description,omitempty"`
	Footer      string          `json:"footer,omitempty"`
	Raw         json.RawMessage `json:"raw"`
}

// UpNextOptions identifies the playback queue whose next items are requested.
// PlaylistIndex is optional because a single-track radio request has no
// playlist position. Continuation is set when loading another page of the
// same queue.
type UpNextOptions struct {
	VideoID       string
	PlaylistID    string
	PlaylistIndex *int
	Continuation  string
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
	return c.newSearchResult(raw), nil
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
	return c.newBrowseResult(raw), nil
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
	return c.newSearchResult(raw), nil
}

func (c *Client) GetArtist(ctx context.Context, artistID string) (*BrowseResult, error) {
	if !strings.HasPrefix(artistID, "UC") && !strings.HasPrefix(artistID, "FEmusic_library_privately_owned_artist") {
		return nil, fmt.Errorf("youtube: invalid artist ID %q", artistID)
	}
	return c.browse(ctx, artistID)
}

func (c *Client) GetAlbum(ctx context.Context, albumID string) (*BrowseResult, error) {
	if !strings.HasPrefix(albumID, "MPR") && !strings.HasPrefix(albumID, "FEmusic_library_privately_owned_release") {
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

// GetAllPlaylist loads every browse continuation available for a playlist.
// YouTube may still omit tracks based on account, region, or access.
func (c *Client) GetAllPlaylist(ctx context.Context, playlistID string) (*BrowseResult, error) {
	result, err := c.GetPlaylist(ctx, playlistID)
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
			return nil, errors.New("youtube: playlist exceeded 1000 continuation pages")
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

// GetAccountDetails returns the active signed-in account. It requires cookie
// authentication.
func (c *Client) GetAccountDetails(ctx context.Context) (*AccountDetails, error) {
	if c.cookieAuth == nil {
		return nil, errors.New("youtube: GetAccountDetails requires cookie authentication")
	}
	raw, err := c.executeForClient(ctx, "account/accounts_list", map[string]any{}, tvClient)
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
func (c *Client) GetAccounts(ctx context.Context) (*AccountList, error) {
	if c.cookieAuth == nil {
		return nil, errors.New("youtube: GetAccounts requires cookie authentication")
	}
	raw, err := c.executeForClient(ctx, "account/accounts_list", map[string]any{
		"requestType":      "ACCOUNTS_LIST_REQUEST_TYPE_CHANNEL_SWITCHER",
		"callCircumstance": "SWITCHING_USERS_FULL",
	}, webClient)
	if err != nil {
		return nil, err
	}
	return &AccountList{Items: extractAccountChannels(raw), Raw: raw}, nil
}

// GetAccountSettings returns the account overview page for the signed-in user.
func (c *Client) GetAccountSettings(ctx context.Context) (*BrowseResult, error) {
	if c.cookieAuth == nil {
		return nil, errors.New("youtube: GetAccountSettings requires cookie authentication")
	}
	raw, err := c.executeForClient(ctx, "browse", map[string]any{"browseId": "SPaccount_overview"}, webClient)
	if err != nil {
		return nil, err
	}
	return c.newBrowseResult(raw), nil
}

// GetUpNext fetches the read-only queue for a track.
func (c *Client) GetUpNext(ctx context.Context, videoID string) (*BrowseResult, error) {
	return c.GetUpNextWithOptions(ctx, UpNextOptions{VideoID: videoID})
}

// GetUpNextWithOptions fetches a queue using its playlist position when one
// exists. Set Continuation to extend the same queue with another /next page.
func (c *Client) GetUpNextWithOptions(ctx context.Context, options UpNextOptions) (*BrowseResult, error) {
	if strings.TrimSpace(options.VideoID) == "" {
		return nil, errors.New("youtube: video ID is required")
	}
	payload := map[string]any{"videoId": options.VideoID}
	if options.PlaylistID != "" {
		payload["playlistId"] = options.PlaylistID
	}
	if options.PlaylistIndex != nil {
		payload["playlistIndex"] = *options.PlaylistIndex
	}
	if options.Continuation != "" {
		payload["continuation"] = options.Continuation
	}
	raw, err := c.execute(ctx, "next", payload)
	if err != nil {
		return nil, err
	}
	result := c.newUpNextResult(raw)
	if options.Continuation != "" || result.QueuePlaylistID != "" {
		return result, nil
	}
	playlistPayload := automixPlaylistPayload(raw)
	if playlistPayload == nil {
		return result, nil
	}
	playlistPayload["videoId"] = options.VideoID
	raw, err = c.execute(ctx, "next", playlistPayload)
	if err != nil {
		return nil, err
	}
	return c.newUpNextResult(raw), nil
}

func (c *Client) newUpNextResult(raw json.RawMessage) *BrowseResult {
	result := c.newBrowseResult(raw)
	result.QueuePlaylistID = upNextPlaylistID(raw)
	result.ContinuationToken = upNextContinuationToken(raw)
	return result
}

func upNextPlaylistID(raw json.RawMessage) string {
	var root any
	if json.Unmarshal(raw, &root) != nil {
		return ""
	}
	var walk func(any) string
	walk = func(value any) string {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				if id := walk(child); id != "" {
					return id
				}
			}
		case map[string]any:
			for _, key := range []string{"playlistPanelRenderer", "playlistPanelContinuation"} {
				if panel, ok := node[key].(map[string]any); ok {
					if id, ok := panel["playlistId"].(string); ok && id != "" {
						return id
					}
				}
			}
			for _, key := range sortedKeys(node) {
				if id := walk(node[key]); id != "" {
					return id
				}
			}
		}
		return ""
	}
	return walk(root)
}

func upNextContinuationToken(raw json.RawMessage) string {
	var root any
	if json.Unmarshal(raw, &root) != nil {
		return ""
	}
	tokenFromPanel := func(panel map[string]any) string {
		if token, ok := panel["continuation"].(string); ok && token != "" {
			return token
		}
		continuations, _ := panel["continuations"].([]any)
		for _, value := range continuations {
			continuation, _ := value.(map[string]any)
			for _, key := range []string{"nextRadioContinuationData", "nextContinuationData"} {
				if data, ok := continuation[key].(map[string]any); ok {
					if token, ok := data["continuation"].(string); ok && token != "" {
						return token
					}
				}
			}
		}
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
			for _, key := range []string{"playlistPanelRenderer", "playlistPanelContinuation"} {
				if panel, ok := node[key].(map[string]any); ok {
					if token := tokenFromPanel(panel); token != "" {
						return token
					}
				}
			}
			for _, key := range sortedKeys(node) {
				if token := walk(node[key]); token != "" {
					return token
				}
			}
		}
		return ""
	}
	return walk(root)
}

func automixPlaylistPayload(raw json.RawMessage) map[string]any {
	var root any
	if json.Unmarshal(raw, &root) != nil {
		return nil
	}
	var walk func(any) map[string]any
	walk = func(value any) map[string]any {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				if payload := walk(child); payload != nil {
					return payload
				}
			}
		case map[string]any:
			if preview, ok := node["automixPreviewVideoRenderer"].(map[string]any); ok {
				content, _ := preview["content"].(map[string]any)
				automix, _ := content["automixPlaylistVideoRenderer"].(map[string]any)
				navigation, _ := automix["navigationEndpoint"].(map[string]any)
				if payload, ok := navigation["watchPlaylistEndpoint"].(map[string]any); ok {
					copy := make(map[string]any, len(payload))
					for key, value := range payload {
						copy[key] = value
					}
					return copy
				}
			}
			for _, key := range sortedKeys(node) {
				if payload := walk(node[key]); payload != nil {
					return payload
				}
			}
		}
		return nil
	}
	return walk(root)
}

// ContinueUpNext requests another page of a queue returned by GetUpNext or
// GetUpNextWithOptions. The video ID and optional playlist fields preserve
// the context used to create that queue.
func (c *Client) ContinueUpNext(ctx context.Context, options UpNextOptions, continuation string) (*BrowseResult, error) {
	if strings.TrimSpace(continuation) == "" {
		return nil, errors.New("youtube: up-next continuation token is required")
	}
	options.Continuation = continuation
	return c.GetUpNextWithOptions(ctx, options)
}

// GetSearchSuggestions retrieves suggestion content for a partial query.
func (c *Client) GetSearchSuggestions(ctx context.Context, input string) (json.RawMessage, error) {
	if strings.TrimSpace(input) == "" {
		return nil, errors.New("youtube: suggestion input is required")
	}
	return c.execute(ctx, "music/get_search_suggestions", map[string]any{"input": input})
}

// GetLyrics loads the lyrics tab for a track. Availability depends on the
// track and YouTube Music account/region.
func (c *Client) GetLyrics(ctx context.Context, videoID string) (*Lyrics, error) {
	raw, err := c.getTrackTab(ctx, videoID, "MUSIC_PAGE_TYPE_TRACK_LYRICS")
	if err != nil {
		return nil, err
	}
	lyrics := &Lyrics{Raw: raw}
	if renderer := findRenderer(raw, "musicDescriptionShelfRenderer"); renderer != nil {
		lyrics.Description = rendererText(renderer["description"])
		lyrics.Footer = rendererText(renderer["footer"])
	}
	return lyrics, nil
}

// GetRelated loads the related music tab for a track.
func (c *Client) GetRelated(ctx context.Context, videoID string) (*BrowseResult, error) {
	raw, err := c.getTrackTab(ctx, videoID, "MUSIC_PAGE_TYPE_TRACK_RELATED")
	if err != nil {
		return nil, err
	}
	return c.newBrowseResult(raw), nil
}

// GetRecap loads the listening-review page for the signed-in account.
func (c *Client) GetRecap(ctx context.Context) (*BrowseResult, error) {
	return c.browse(ctx, "FEmusic_listening_review")
}

func (c *Client) getTrackTab(ctx context.Context, videoID, pageType string) (json.RawMessage, error) {
	if strings.TrimSpace(videoID) == "" {
		return nil, errors.New("youtube: video ID is required")
	}
	raw, err := c.execute(ctx, "next", map[string]any{"videoId": videoID})
	if err != nil {
		return nil, err
	}
	browseID := musicTabBrowseID(raw, pageType)
	if browseID == "" {
		return nil, fmt.Errorf("youtube: track response has no %s tab", pageType)
	}
	page, err := c.browse(ctx, browseID)
	if err != nil {
		return nil, err
	}
	return page.Raw, nil
}

func musicTabBrowseID(raw json.RawMessage, pageType string) string {
	var root any
	if json.Unmarshal(raw, &root) != nil {
		return ""
	}
	var walk func(any, []any) string
	walk = func(value any, parents []any) string {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				if id := walk(child, parents); id != "" {
					return id
				}
			}
		case map[string]any:
			if node["pageType"] == pageType {
				for index := len(parents) - 1; index >= 0; index-- {
					if id := navigationID(parents[index]); id != "" {
						return id
					}
				}
				return navigationID(node)
			}
			parents = append(parents, node)
			for _, key := range sortedKeys(node) {
				if id := walk(node[key], parents); id != "" {
					return id
				}
			}
		}
		return ""
	}
	return walk(root, nil)
}

func findRenderer(raw json.RawMessage, rendererName string) map[string]any {
	var root any
	if json.Unmarshal(raw, &root) != nil {
		return nil
	}
	var walk func(any) map[string]any
	walk = func(value any) map[string]any {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				if renderer := walk(child); renderer != nil {
					return renderer
				}
			}
		case map[string]any:
			if renderer, ok := node[rendererName].(map[string]any); ok {
				return renderer
			}
			for _, key := range sortedKeys(node) {
				if renderer := walk(node[key]); renderer != nil {
					return renderer
				}
			}
		}
		return nil
	}
	return walk(root)
}

// newBrowseResult reads a browse response. The response is parsed once, and
// everything taken from it is read off that one tree.
func (c *Client) newBrowseResult(raw json.RawMessage) *BrowseResult {
	root := decodeResponse(raw)
	return &BrowseResult{
		Items: extractMusicItems(root, c.keepRenderers), Sections: extractMusicSections(root, c.keepRenderers),
		ContinuationToken: continuationToken(root), Pages: []json.RawMessage{raw}, Raw: raw,
	}
}

// newSearchResult reads a search response, as newBrowseResult does a browse.
func (c *Client) newSearchResult(raw json.RawMessage) *SearchResult {
	root := decodeResponse(raw)
	return &SearchResult{
		Items: extractMusicItems(root, c.keepRenderers), ContinuationToken: continuationToken(root), Raw: raw,
	}
}

// decodeResponse parses a response into the generic form the extractors walk,
// and gives nil, which they read as empty, for what is not JSON.
func decodeResponse(raw json.RawMessage) any {
	var root any
	if json.Unmarshal(raw, &root) != nil {
		return nil
	}
	return root
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

func continuationToken(root any) string {
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

func extractMusicSections(root any, keep bool) []MusicSection {
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
				if key == "musicShelfRenderer" || key == "musicPlaylistShelfRenderer" || key == "playlistVideoListRenderer" || key == "gridRenderer" || key == "musicCarouselShelfRenderer" {
					if renderer, ok := child.(map[string]any); ok {
						section := MusicSection{
							Title: rendererTitle(renderer), Kind: key,
							Items: extractMusicItems(renderer, keep), ContinuationToken: continuationToken(renderer),
						}
						if keep {
							section.Raw = marshalRenderer(renderer)
						}
						sections = append(sections, section)
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

// marshalRenderer is the JSON a renderer was read from, for a client that
// keeps it. A tree that came from JSON can be written back as JSON.
func marshalRenderer(renderer map[string]any) json.RawMessage {
	data, err := json.Marshal(renderer)
	if err != nil {
		return nil
	}
	return data
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

func extractMusicItems(root any, keep bool) []MusicItem {
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
				if key == "musicCardShelfRenderer" {
					// A search's top result is a card, not a list entry.
					if renderer, ok := child.(map[string]any); ok {
						if item, ok := parseMusicCardShelf(renderer, keep); ok {
							appendMusicItem(&items, seen, item, renderer)
						}
					}
				} else if kind, ok := rendererKind(key); ok {
					if renderer, ok := child.(map[string]any); ok {
						appendMusicItem(&items, seen, parseMusicItem(kind, renderer, keep), renderer)
					}
				}
				walk(child)
			}
		}
	}
	walk(root)
	return items
}

// appendMusicItem keeps a parsed item unless its renderer held nothing to
// identify it by. A playlist may hold a song twice; its entries differ by
// the ID the playlist gave each, which keeps both.
func appendMusicItem(items *[]MusicItem, seen map[string]struct{}, item MusicItem, renderer map[string]any) {
	identity := strings.Join([]string{
		item.VideoID, item.BrowseID, item.PlaylistID, item.Title,
		nestedString(renderer["playlistItemData"], "playlistSetVideoId"),
	}, "\x00")
	if identity == "\x00\x00\x00\x00" {
		return
	}
	if _, exists := seen[identity]; exists {
		return
	}
	seen[identity] = struct{}{}
	*items = append(*items, item)
}

// parseMusicCardShelf reads a search's top result. It carries its media in
// the card itself, outside any list: the title run navigates to the video,
// the artist's browse endpoint sits in the subtitle runs, and the length
// trails the subtitle.
func parseMusicCardShelf(renderer map[string]any, keep bool) (MusicItem, bool) {
	item := MusicItem{Kind: "video"}
	item.Title = rendererText(renderer["title"])
	item.Subtitle = rendererText(renderer["subtitle"])
	if duration := trailingDuration(item.Subtitle); duration != "" {
		item.Duration = duration
		item.Subtitle = strings.TrimSuffix(item.Subtitle, " • "+duration)
	}
	item.VideoID = nestedString([]any{renderer["title"], renderer["onTap"], renderer["thumbnailOverlay"]}, "videoId")
	if item.VideoID == "" {
		item.VideoID = rendererVideoID(renderer)
	}
	item.BrowseID = cardArtistBrowseID(renderer)
	item.Thumbnail = rendererThumbnail(renderer["thumbnail"])
	if item.Thumbnail == "" {
		item.Thumbnail = rendererThumbnail(renderer)
	}
	if item.VideoID != "" {
		item.ID = item.VideoID
	} else if item.BrowseID != "" {
		item.ID = item.BrowseID
	}
	if keep {
		item.Raw = marshalRenderer(renderer)
	}
	if item.VideoID == "" || item.Title == "" {
		return MusicItem{}, false
	}
	return item, true
}

// cardArtistBrowseID reads the artist a card's subtitle names. navigationID
// cannot: it would return the mix queue of the card's menu entries first.
func cardArtistBrowseID(renderer map[string]any) string {
	subtitle, ok := renderer["subtitle"].(map[string]any)
	if !ok {
		return ""
	}
	runs, ok := subtitle["runs"].([]any)
	if !ok {
		return ""
	}
	for _, value := range runs {
		run, ok := value.(map[string]any)
		if !ok {
			continue
		}
		endpoint, ok := run["navigationEndpoint"].(map[string]any)
		if !ok {
			continue
		}
		if browse, ok := endpoint["browseEndpoint"].(map[string]any); ok {
			if id, _ := browse["browseId"].(string); id != "" {
				return id
			}
		}
	}
	return ""
}

// rendererArtistBrowseID reads an artist browse endpoint from a track's
// subtitle or second flex column, without treating album links as artists.
func rendererArtistBrowseID(renderer map[string]any) string {
	if id := artistBrowseEndpoint(renderer["subtitle"]); id != "" {
		return id
	}
	columns, ok := renderer["flexColumns"].([]any)
	if !ok || len(columns) < 2 {
		return ""
	}
	return artistBrowseEndpoint(columns[1])
}

func artistBrowseEndpoint(value any) string {
	switch node := value.(type) {
	case []any:
		for _, child := range node {
			if id := artistBrowseEndpoint(child); id != "" {
				return id
			}
		}
	case map[string]any:
		if endpoint, ok := node["browseEndpoint"].(map[string]any); ok {
			if id, _ := endpoint["browseId"].(string); strings.HasPrefix(id, "UC") || strings.Contains(id, "privately_owned_artist") {
				return id
			}
		}
		for _, key := range sortedKeys(node) {
			if id := artistBrowseEndpoint(node[key]); id != "" {
				return id
			}
		}
	}
	return ""
}

// trailingDuration reads a track length from the end of a card's subtitle,
// where it trails the artist as "Video • Artist • 8.4M views • 2:05".
func trailingDuration(subtitle string) string {
	for _, part := range strings.Split(subtitle, " • ") {
		if isDurationText(strings.TrimSpace(part)) {
			return strings.TrimSpace(part)
		}
	}
	return ""
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
		return "video", true
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

func parseMusicItem(kind string, renderer map[string]any, keep bool) MusicItem {
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
	if item.Duration == "" {
		// Search and library rows keep the length at the end of their last
		// flex column, as "Artist • Album • 3:42".
		item.Duration = rendererFlexDuration(renderer)
		if item.Duration != "" {
			item.Subtitle = strings.TrimSuffix(item.Subtitle, " • "+item.Duration)
		}
	}
	item.VideoID, _ = renderer["videoId"].(string)
	if item.VideoID == "" {
		item.VideoID = rendererVideoID(renderer)
	}
	item.PlaylistID, _ = renderer["playlistId"].(string)
	item.BrowseID = navigationID(renderer["navigationEndpoint"])
	if kind == "track" || kind == "video" {
		if artistID := rendererArtistBrowseID(renderer); artistID != "" {
			item.BrowseID = artistID
		}
	}
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
	if item.Thumbnail == "" {
		// Two-row items keep their art under thumbnailRenderer instead of
		// thumbnail.
		item.Thumbnail = rendererThumbnail(renderer)
	}
	if keep {
		item.Raw = marshalRenderer(renderer)
	}
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
	// A flex column holds a musicResponsiveListItemFlexColumnRenderer and a
	// fixed column a musicResponsiveListItemFixedColumnRenderer.
	for _, key := range []string{"musicResponsiveListItemFlexColumnRenderer", "musicResponsiveListItemFixedColumnRenderer"} {
		if columnRenderer, ok := column[key].(map[string]any); ok {
			return rendererText(columnRenderer["text"])
		}
	}
	return ""
}

// rendererVideoID finds a renderer's video ID where a track's own taps
// keep it: its data, its play overlay and its columns. The menu stays out:
// its mix and shuffle entries carry queue IDs, not the track's.
func rendererVideoID(renderer map[string]any) string {
	for _, key := range []string{"playlistItemData", "overlay", "flexColumns", "thumbnailOverlay", "navigationEndpoint", "title", "onTap"} {
		value, ok := renderer[key]
		if !ok {
			continue
		}
		if id := nestedString(value, "videoId"); id != "" {
			return id
		}
	}
	return ""
}

// rendererTitle reads a shelf's title, which newer responses put in the
// shelf's header instead of the shelf itself.
func rendererTitle(renderer map[string]any) string {
	if title := rendererText(renderer["title"]); title != "" {
		return title
	}
	header, ok := renderer["header"].(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range sortedKeys(header) {
		headerRenderer, ok := header[key].(map[string]any)
		if !ok {
			continue
		}
		if title := rendererText(headerRenderer["title"]); title != "" {
			return title
		}
	}
	return ""
}

// nestedString returns the first non-empty string stored under name anywhere
// in value. It visits maps in sorted key order, so the result is stable.
func nestedString(value any, name string) string {
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
			if text, ok := node[name].(string); ok && text != "" {
				result = text
				return
			}
			for _, key := range sortedKeys(node) {
				walk(node[key])
			}
		}
	}
	walk(value)
	return result
}

// rendererFlexDuration reads a track length from the trailing run of a
// musicResponsiveListItemRenderer's last flex column, where search and
// library rows keep it.
func rendererFlexDuration(renderer map[string]any) string {
	columns, ok := renderer["flexColumns"].([]any)
	if !ok {
		return ""
	}
	for index := len(columns) - 1; index >= 0; index-- {
		column, ok := columns[index].(map[string]any)
		if !ok {
			continue
		}
		columnRenderer, ok := column["musicResponsiveListItemFlexColumnRenderer"].(map[string]any)
		if !ok {
			continue
		}
		text, ok := columnRenderer["text"].(map[string]any)
		if !ok {
			continue
		}
		runs, ok := text["runs"].([]any)
		if !ok {
			continue
		}
		for runIndex := len(runs) - 1; runIndex >= 0; runIndex-- {
			run, ok := runs[runIndex].(map[string]any)
			if !ok {
				continue
			}
			if _, navigates := run["navigationEndpoint"]; navigates {
				continue
			}
			if text, _ := run["text"].(string); isDurationText(text) {
				return text
			}
		}
	}
	return ""
}

// isDurationText reports whether text reads as a track length, as "3:42" or
// "1:02:03".
func isDurationText(text string) bool {
	parts := strings.Split(text, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 2 {
			return false
		}
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return false
			}
		}
	}
	return true
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
			// Queue taps name a watch endpoint, not a page: their playlist
			// is the mix being offered, and the track itself carries the
			// video ID. navigationID only names browsable pages.
			if _, watching := node["watchEndpoint"]; watching {
				return
			}
			for _, name := range []string{"browseId", "playlistId"} {
				if value, ok := node[name].(string); ok && value != "" {
					if name == "playlistId" && !isBrowsablePlaylist(value) {
						continue
					}
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

// isBrowsablePlaylist reports whether an ID names a page the app can open.
// Mix and radio queues ride along in taps and menus, but browsing one is
// empty: only library, community and product playlists open.
func isBrowsablePlaylist(id string) bool {
	return strings.HasPrefix(id, "VL") || strings.HasPrefix(id, "PL")
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
			for _, key := range sortedKeys(node) {
				walk(node[key])
			}
		}
	}
	walk(value)
	return result
}
