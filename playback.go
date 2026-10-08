package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/elianiva/meiro/youtube"
)

// play starts item, with the items around it as the queue.
func (a *app) play(item youtube.MusicItem, queue []youtube.MusicItem, index int) {
	if item.VideoID == "" {
		return
	}
	a.queue = queue
	if len(a.queue) == 0 {
		a.queue = []youtube.MusicItem{item}
	}
	if index < 0 || index >= len(a.queue) || a.queue[index].VideoID != item.VideoID {
		index = 0
		for i := range a.queue {
			if a.queue[i].VideoID == item.VideoID {
				index = i
				break
			}
		}
	}
	a.index = index
	if a.resolving && a.current.VideoID == item.VideoID {
		return // this track is already on its way
	}
	a.start(item)
}

// playAll plays everything on the page shown.
func (a *app) playAll() {
	if len(a.playable) == 0 {
		return
	}
	a.play(a.playable[0], a.playable, 0)
}

// start makes item the current track and resolves its audio.
func (a *app) start(item youtube.MusicItem) {
	a.current = item
	a.total = parseDuration(item.Duration)
	a.scrub, a.playErr = 0, ""
	a.player.Stop()
	a.stream(item)
}

// loading reports whether the track chosen is not making sound yet: its audio
// is being found, or ffmpeg is still opening it.
func (a *app) loading() bool {
	return a.resolving || a.player.Buffering()
}

// stream resolves a track's audio off the main thread and plays it.
func (a *app) stream(item youtube.MusicItem) {
	a.streamGen++
	gen := a.streamGen
	a.resolving = true
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		streamURL, total, err := a.resolveStream(ctx, item)
		a.update(func() {
			if gen != a.streamGen {
				return // another track was chosen while this one resolved
			}
			a.resolving = false
			if err != nil {
				a.playErr = err.Error()
				return
			}
			if total > 0 {
				a.total = total
			}
			if err := a.player.Play(streamURL); err != nil {
				a.playErr = err.Error()
			}
		})
	})
}

// resolveStream finds a URL ffmpeg can read. It asks YouTube through the
// package first, and falls back to yt-dlp, which keeps working when
// YouTube's player script has moved past what the package can decipher.
func (a *app) resolveStream(ctx context.Context, item youtube.MusicItem) (string, time.Duration, error) {
	if client := a.client(); client != nil {
		if info, err := client.GetTrackInfo(ctx, item.VideoID); err == nil {
			if format, ok := info.BestAudioFormat(); ok {
				return format.PlayableURL(), parseDuration(info.VideoDetails.Length), nil
			}
		}
	}
	return ytDlpStream(ctx, item.VideoID)
}

// ytDlpStream asks yt-dlp for a direct audio URL.
func ytDlpStream(ctx context.Context, videoID string) (string, time.Duration, error) {
	path, err := toolPath("yt-dlp")
	if err != nil {
		return "", 0, errors.New("playing needs ffmpeg and yt-dlp on PATH")
	}
	command := exec.CommandContext(ctx, path,
		"-f", "bestaudio", "-g", "--no-playlist", "--no-warnings",
		"https://music.youtube.com/watch?v="+videoID)
	output, err := command.Output()
	if err != nil {
		return "", 0, fmt.Errorf("could not resolve the audio: %w", err)
	}
	streamURL, _, _ := strings.Cut(string(output), "\n")
	streamURL = strings.TrimSpace(streamURL)
	if streamURL == "" {
		return "", 0, errors.New("could not resolve the audio")
	}
	return streamURL, durationFromURL(streamURL), nil
}

// durationFromURL reads the track length YouTube puts in its media URLs.
func durationFromURL(streamURL string) time.Duration {
	parsed, err := url.Parse(streamURL)
	if err != nil {
		return 0
	}
	seconds, err := strconv.ParseFloat(parsed.Query().Get("dur"), 64)
	if err != nil {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

// parseDuration reads a "3:42" or "1:02:03" track length.
func parseDuration(text string) time.Duration {
	parts := strings.Split(strings.TrimSpace(text), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0
	}
	total := time.Duration(0)
	for _, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil {
			return 0
		}
		total = total*60 + time.Duration(value)*time.Second
	}
	return total
}

// advance plays the next track of the queue, and stops at the end of it.
func (a *app) advance() {
	if next, ok := a.nextIndex(); ok {
		a.index = next
		a.start(a.queue[a.index])
		return
	}
	a.streamGen++ // drop a track still being found
	a.resolving = false
	a.player.Stop()
}

// previous restarts the track, or plays the one before it when the track
// has only just started.
func (a *app) previous() {
	if a.index == 0 || a.player.Position() > 3*time.Second {
		a.player.Seek(0)
		return
	}
	a.index--
	a.start(a.queue[a.index])
}

// togglePlay pauses a playing track, resumes a paused one, and starts the
// current track again after it failed or ended.
func (a *app) togglePlay() {
	if a.resolving {
		return // the track is already on its way
	}
	if a.playErr != "" || !a.player.Active() {
		if a.current.VideoID != "" {
			a.start(a.current)
		}
		return
	}
	a.player.Toggle()
}

// clock formats a position as "3:42" or "1:02:03".
func clock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	hours, minutes, seconds := total/3600, (total%3600)/60, total%60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%d:%02d", minutes, seconds)
}

// shufflePage turns shuffle on and plays the page from a song picked at
// random.
func (a *app) shufflePage() {
	if len(a.playable) == 0 {
		return
	}
	a.shuffle = true
	i := rand.IntN(len(a.playable))
	a.play(a.playable[i], a.playable, i)
}

// setVolume sets the volume, from 0 to 100, and keeps it for the next run.
func (a *app) setVolume(v float64) {
	a.volume = min(max(v, 0), 100)
	a.player.SetVolume(a.volume / 100)
	a.settings.Volume = a.volume
}

// toggleMute silences the player, or brings the volume back.
func (a *app) toggleMute() {
	if a.volume > 0 {
		a.muted = a.volume
		a.setVolume(0)
		return
	}
	a.setVolume(max(a.muted, 40))
}

// cycleRepeat goes from not repeating to repeating the queue to repeating the
// track and back.
func (a *app) cycleRepeat() { a.repeat = (a.repeat + 1) % 3 }

// lyricsState is the lyrics of the track playing, as far as they are loaded.
type lyricsState struct {
	videoID string
	loading bool
	text    string
	footer  string
	err     string
}

// loadLyrics fetches the lyrics of the current track, once.
func (a *app) loadLyrics() {
	id := a.current.VideoID
	if id == "" || a.lyrics.videoID == id || a.client() == nil {
		return
	}
	a.lyrics = lyricsState{videoID: id, loading: true}
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		lyrics, err := a.client().GetLyrics(ctx, id)
		a.update(func() {
			if a.lyrics.videoID != id {
				return
			}
			a.lyrics.loading = false
			if err != nil {
				a.lyrics.err = "No lyrics for this song."
				return
			}
			a.lyrics.text, a.lyrics.footer = strings.TrimSpace(lyrics.Description), lyrics.Footer
			if a.lyrics.text == "" {
				a.lyrics.err = "No lyrics for this song."
			}
		})
	})
}

// followArtwork takes the theme's seed from the artwork of the track playing,
// when the user asked for it.
func (a *app) followArtwork() {
	if !a.settings.Dynamic || a.current.Thumbnail == "" {
		return
	}
	if colour, ok := a.thumbs.colour(a.current.Thumbnail, playerArt); ok {
		a.dynamic, a.dynamicOK = colour, true
	}
}
