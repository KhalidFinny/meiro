package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

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

// stream resolves a track's audio off the main thread and plays it.
func (a *app) stream(item youtube.MusicItem) {
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		streamURL, total, err := a.resolveStream(ctx, item)
		a.update(func() {
			if a.current.VideoID != item.VideoID {
				return // another track was chosen while this one resolved
			}
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
	path, err := exec.LookPath("yt-dlp")
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
	if a.index+1 < len(a.queue) {
		a.index++
		a.start(a.queue[a.index])
		return
	}
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
	if a.playErr != "" || !a.player.Active() {
		if a.current.VideoID != "" {
			a.start(a.current)
		}
		return
	}
	a.player.Toggle()
}

// playerBar shows the current track and its controls, along the bottom of
// the window.
func (a *app) playerBar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Height(74).Padding(10, 16).Gap(14).AlignItems(ui.Center).
		Background(t.Background).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		ui.Image(c, a.thumbs.bitmap(a.current.Thumbnail, 96)).Size(46, 46).Fit(ui.Cover).Radius(6).Background(t.Surface).Shrink(0)
		ui.Column(c).Width(200).Shrink(0).Gap(2).Children(func() {
			ui.Text(c, a.current.Title).SingleLine()
			line, color := a.current.Subtitle, t.TextMuted
			if a.playErr != "" {
				line, color = a.playErr, t.Danger
			}
			ui.Text(c, line).SingleLine().FontSize(12).TextColor(color)
		})
		if transportButton(c, "Previous", "Previous", previousIcon, false) {
			a.previous()
		}
		if transportButton(c, "Play or pause", playPauseTip(a), playPauseIcon(a), a.player.Playing()) {
			a.togglePlay()
		}
		if transportButton(c, "Next", "Next", nextIcon, false) {
			a.advance()
		}
		a.scrubber(c)
		ui.Text(c, clock(a.player.Position())+" / "+clock(a.total)).FontSize(12).TextColor(t.TextMuted).Shrink(0)
		ui.Icon(c, volumeIcon).FontSize(15).TextColor(t.TextMuted).Shrink(0)
		if ui.Slider(c, &a.volume, 0, 100).Label("Volume").Width(80).Changed() {
			a.player.SetVolume(a.volume / 100)
		}
	})
}

// scrubber is the position slider. It follows the player until the user
// takes it, and seeks when they let go.
func (a *app) scrubber(c *ui.Context) {
	known := a.total > 0
	total := a.total.Seconds()
	if !known {
		total, a.scrub = 1, 0
	}
	if a.scrub > total {
		a.scrub = total
	}
	slider := ui.Slider(c, &a.scrub, 0, total).Label("Position").Grow(1).MinWidth(80).Disabled(!known)
	if slider.Pressed() {
		a.scrubbing = true
	}
	if a.scrubbing && !slider.Pressed() {
		a.scrubbing = false
		a.player.Seek(time.Duration(a.scrub * float64(time.Second)))
	}
}

func playPauseIcon(a *app) *ui.SVG {
	if a.player.Playing() {
		return pauseIcon
	}
	return playIcon
}

func playPauseTip(a *app) string {
	if a.player.Playing() {
		return "Pause"
	}
	return "Play"
}

// transportButton shows one of the player's controls as an icon button.
func transportButton(c *ui.Context, label, tip string, shape *ui.SVG, primary bool) bool {
	var button *ui.Element
	if primary {
		button = ui.PrimaryButton(c, "")
	} else {
		button = ui.Button(c, "")
	}
	button.Label(label).Tooltip(tip).Radius(999).Padding(8)
	button.Children(func() { ui.Icon(c, shape).FontSize(15) })
	return button.Clicked()
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
