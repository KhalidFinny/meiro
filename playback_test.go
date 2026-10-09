package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elianiva/meiro/youtube"
)

// Warming downloads the next track, which is only worth its bandwidth when
// the cache keeps enough tracks for it to still be there when it plays. At a
// limit of one it would evict the track that is playing.
func TestWarmNextNeedsACacheThatKeepsMoreThanOneTrack(t *testing.T) {
	for _, limit := range []int{0, 1, 2} {
		a := newTestApp()
		cache, err := newAudioCache(filepath.Join(t.TempDir(), "audio"), limit)
		if err != nil {
			t.Fatal(err)
		}
		a.replaceAudioCache(cache)
		a.current = youtube.MusicItem{VideoID: "playing"}
		a.queue = []youtube.MusicItem{{VideoID: "playing"}, {VideoID: "next"}}
		a.index = 0
		queued := 0
		a.run = func(work func()) { queued++ }
		a.warmNext()
		if wantQueued := limit > 1; (queued > 0) != wantQueued {
			t.Errorf("warmNext with a cache limit of %d queued %d downloads, want queued=%v", limit, queued, wantQueued)
		}
		cache.close()
	}
}

func TestYtDlpCookieFileUsesPrivateNetscapeFormat(t *testing.T) {
	path, err := ytDlpCookieFile("SID=session; SAPISID=secret=value")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("cookie file permissions = %04o, want 0600", got)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# Netscape HTTP Cookie File",
		".youtube.com\tTRUE\t/\tTRUE\t0\tSID\tsession",
		".youtube.com\tTRUE\t/\tTRUE\t0\tSAPISID\tsecret=value",
	} {
		if !strings.Contains(string(contents), want) {
			t.Errorf("cookie file does not contain %q: %s", want, contents)
		}
	}
}

func TestYtDlpCookieFileRejectsEmptyCookieHeader(t *testing.T) {
	if path, err := ytDlpCookieFile("; = ;"); err == nil {
		_ = os.Remove(path)
		t.Fatal("cookie file was created without any valid cookies")
	}
}
