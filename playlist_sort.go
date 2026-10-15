package main

import (
	"cmp"
	"sort"
	"strconv"
	"strings"

	"github.com/elianiva/meiro/youtube"
)

// playlistSortField is the value the playlist page orders its tracks by.
type playlistSortField uint8

const (
	playlistSortOriginal playlistSortField = iota
	playlistSortTitle
	playlistSortArtist
	playlistSortDuration
)

// playlistSort is one playlist page's ordering choice. Original is the zero
// value, so an untouched page keeps YouTube Music's order.
type playlistSort struct {
	field playlistSortField
	desc  bool
}

func (s playlistSort) active() bool { return s.field != playlistSortOriginal }

func (s playlistSort) label() string {
	switch s.field {
	case playlistSortOriginal:
		return "Original order"
	case playlistSortTitle:
		return "Title"
	case playlistSortArtist:
		return "Artist"
	case playlistSortDuration:
		return "Duration"
	}
	return "Original order"
}

func (s playlistSort) directionLabel() string {
	switch s.field {
	case playlistSortTitle, playlistSortArtist:
		if s.desc {
			return "Z–A"
		}
		return "A–Z"
	case playlistSortDuration:
		if s.desc {
			return "Longest first"
		}
		return "Shortest first"
	case playlistSortOriginal:
		return "Original order"
	}
	return "Original order"
}

func (s *playlistSort) choose(field playlistSortField) {
	if field == playlistSortOriginal {
		*s = playlistSort{}
		return
	}
	if s.field != field {
		s.field, s.desc = field, false
		return
	}
	s.desc = !s.desc
}

// sortPlaylistSections sorts track slots independently within each section.
// Section metadata and non-track items stay where YouTube placed them.
func sortPlaylistSections(sections []youtube.MusicSection, s playlistSort) []youtube.MusicSection {
	if !s.active() {
		return sections
	}
	sorted := make([]youtube.MusicSection, len(sections))
	for i, section := range sections {
		sorted[i] = section
		sorted[i].Items = append([]youtube.MusicItem(nil), section.Items...)
		tracks := make([]youtube.MusicItem, 0, len(section.Items))
		for _, item := range section.Items {
			if item.IsTrack() {
				tracks = append(tracks, item)
			}
		}
		sort.SliceStable(tracks, func(i, j int) bool {
			order := comparePlaylistItems(tracks[i], tracks[j], s.field)
			if s.desc {
				return order > 0
			}
			return order < 0
		})
		track := 0
		for j, item := range sorted[i].Items {
			if item.IsTrack() {
				sorted[i].Items[j] = tracks[track]
				track++
			}
		}
	}
	return sorted
}

func comparePlaylistItems(left, right youtube.MusicItem, field playlistSortField) int {
	var order int
	switch field {
	case playlistSortOriginal:
		return 0
	case playlistSortTitle:
		order = strings.Compare(strings.ToLower(left.Title), strings.ToLower(right.Title))
	case playlistSortArtist:
		order = strings.Compare(strings.ToLower(playlistArtist(left)), strings.ToLower(playlistArtist(right)))
	case playlistSortDuration:
		order = cmp.Compare(durationSeconds(left.Duration), durationSeconds(right.Duration))
	}
	if order != 0 {
		return order
	}
	return strings.Compare(strings.ToLower(left.Title), strings.ToLower(right.Title))
}

// playlistArtist follows the text convention already used by the parser:
// artist, album and duration are separated by " • ".
func playlistArtist(item youtube.MusicItem) string {
	artist, _, _ := strings.Cut(item.Subtitle, " • ")
	return strings.TrimSpace(artist)
}

func durationSeconds(value string) int {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) == 0 || len(parts) > 3 {
		return 0
	}
	seconds := 0
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return 0
		}
		seconds = seconds*60 + n
	}
	return seconds
}
