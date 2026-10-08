package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
)

// settings is what the user chose, kept between runs.
type settings struct {
	// Seed is the colour the theme grows from, as "#rrggbb".
	Seed string `json:"seed"`
	// Mode is "system", "light" or "dark".
	Mode string `json:"mode"`
	// Style is how the seed is spent: see m3.Style.
	Style int `json:"style"`
	// Dynamic takes the seed from the artwork of the track playing.
	Dynamic bool `json:"dynamic"`
	// RailExpanded keeps the navigation rail open, with labels beside icons.
	RailExpanded bool `json:"railExpanded"`
	// Volume is the player's, from 0 to 100.
	Volume float64 `json:"volume"`
	// Recent holds the searches submitted, newest first.
	Recent []string `json:"recent,omitempty"`
}

func defaultSettings() settings {
	return settings{Seed: "#6750a4", Mode: "system", Volume: 70}
}

// config returns the theme configuration the settings describe.
func (s settings) config() m3.Config {
	seed := m3.DefaultSeed
	if parsed, ok := parseSeed(s.Seed); ok {
		seed = parsed
	}
	mode := m3.System
	switch s.Mode {
	case "light":
		mode = m3.Light
	case "dark":
		mode = m3.Dark
	}
	style := m3.Style(s.Style)
	if style < 0 || int(style) >= len(m3.Styles) {
		style = m3.TonalSpot
	}
	return m3.Config{Seed: seed, Mode: mode, Style: style}
}

func setMode(s *settings, mode m3.Mode) {
	s.Mode = strings.ToLower(mode.String())
}

// parseSeed reads "#rrggbb" without panicking on what a file holds.
func parseSeed(text string) (c ui.Color, ok bool) {
	if len(text) != 7 || text[0] != '#' {
		return c, false
	}
	var r, g, b uint8
	if _, err := fmt.Sscanf(text[1:], "%02x%02x%02x", &r, &g, &b); err != nil {
		return c, false
	}
	return ui.RGB(r, g, b), true
}

func seedHex(c ui.Color) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// remember puts a submitted search at the top of the recent ones.
func (s *settings) remember(query string) {
	query = strings.TrimSpace(query)
	if query == "" {
		return
	}
	s.Recent = slices.DeleteFunc(s.Recent, func(q string) bool { return strings.EqualFold(q, query) })
	s.Recent = append([]string{query}, s.Recent...)
	if len(s.Recent) > 8 {
		s.Recent = s.Recent[:8]
	}
}

// loadSettings reads the settings file, and returns the defaults for one that
// is missing or unreadable.
func loadSettings(path string) settings {
	s := defaultSettings()
	if path == "" {
		return s
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var read settings
	if json.Unmarshal(data, &read) != nil {
		return s
	}
	if read.Volume < 0 || read.Volume > 100 {
		read.Volume = s.Volume
	}
	if _, ok := parseSeed(read.Seed); !ok {
		read.Seed = s.Seed
	}
	if read.Mode == "" {
		read.Mode = s.Mode
	}
	return read
}

// save writes the settings, atomically, so a crash never leaves half a file.
func (s settings) save(path string) error {
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
