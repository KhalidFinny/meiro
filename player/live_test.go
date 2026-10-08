package player

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestLivePlayback(t *testing.T) {
	if os.Getenv("MEIRO_LIVE_PLAYBACK") == "" {
		t.Skip("set MEIRO_LIVE_PLAYBACK=1 to play a real stream")
	}
	out, err := exec.Command("yt-dlp", "-f", "bestaudio", "-g", "--no-playlist",
		"https://music.youtube.com/watch?v=dQw4w9WgXcQ").Output()
	if err != nil {
		t.Skipf("yt-dlp unavailable: %v", err)
	}
	url := strings.TrimSpace(strings.Split(string(out), "\n")[0])
	if url == "" {
		t.Skip("no url")
	}
	p := New()
	if !p.Available() {
		t.Fatal("ffmpeg not available")
	}
	if err := p.Play(url); err != nil {
		t.Fatalf("Play: %v", err)
	}
	t.Cleanup(p.Stop)
	time.Sleep(3 * time.Second)
	t.Logf("after 3s: playing=%v ended=%v pos=%v failure=%q", p.Playing(), p.Ended(), p.Position(), p.Failure())
	if p.Position() < 1*time.Second {
		t.Errorf("position did not advance: %v", p.Position())
	}
	p.Pause()
	t.Logf("paused: playing=%v paused=%v", p.Playing(), p.Paused())
	time.Sleep(500 * time.Millisecond)
	pos := p.Position()
	time.Sleep(500 * time.Millisecond)
	if p.Position() > pos+500*time.Millisecond {
		t.Errorf("position moved while paused: %v -> %v", pos, p.Position())
	}
	p.Resume()
	p.Seek(120 * time.Second)
	time.Sleep(2 * time.Second)
	t.Logf("after seek to 120s: pos=%v playing=%v failure=%q", p.Position(), p.Playing(), p.Failure())
	if p.Position() < 119*time.Second {
		t.Errorf("seek did not land: %v", p.Position())
	}
	p.SetVolume(0.2)
	t.Logf("volume=%v", p.Volume())
}
