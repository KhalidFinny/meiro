package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/elianiva/meiro/youtube"
)

// TestLivePlayback resolves and plays a real track through the same path the
// app uses. It needs the network, ffmpeg and yt-dlp:
//
//	MEIRO_LIVE_PLAYBACK=1 go test -run TestLivePlayback -v .
func TestLivePlayback(t *testing.T) {
	if os.Getenv("MEIRO_LIVE_PLAYBACK") == "" {
		t.Skip("set MEIRO_LIVE_PLAYBACK=1 to resolve and play a real track")
	}
	a := newApp()
	a.public = youtube.NewClient(youtube.Options{})
	item := youtube.MusicItem{VideoID: "khnokW3Mw24", Title: "Instant Crush"}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	streamURL, total, err := a.resolveStream(ctx, item)
	if err != nil {
		t.Fatalf("resolveStream: %v", err)
	}
	t.Logf("resolved %d bytes of URL, total %v", len(streamURL), total)
	if err := a.player.Play(streamURL); err != nil {
		t.Fatalf("play: %v", err)
	}
	defer a.player.Stop()
	time.Sleep(4 * time.Second)
	t.Logf("playing=%v ended=%v position=%v failure=%q", a.player.Playing(), a.player.Ended(), a.player.Position(), a.player.Failure())
	if a.player.Position() < 2*time.Second {
		t.Errorf("playback did not advance: %v", a.player.Position())
	}
}
