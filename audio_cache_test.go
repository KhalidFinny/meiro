package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAudioCacheDownloadsTheOriginalAudio(t *testing.T) {
	args := audioCacheDownloadArgs("video-id", t.TempDir(), "--cookies", "cookies.txt")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-f bestaudio") {
		t.Errorf("yt-dlp options do not request the best audio: %s", joined)
	}
	// The cache keeps what YouTube serves. Transcoding or remuxing it spends
	// CPU and loses quality for metadata the player never reads.
	for _, unwanted := range []string{"--extract-audio", "--audio-format", "--audio-quality", "--embed-metadata"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("yt-dlp still post-processes the cached audio (%s): %s", unwanted, joined)
		}
	}
	if got := args[len(args)-1]; got != "https://music.youtube.com/watch?v=video-id" {
		t.Errorf("yt-dlp URL = %q, want it after all options", got)
	}
}

func TestAudioCacheReturnsHitsAndEvictsLeastRecentlyUsed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audio")
	cache, err := newAudioCache(dir, 5)
	if err != nil {
		t.Fatal(err)
	}
	cache.setLimit(5)
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		installTestAudio(t, cache, id)
		path, ok := cache.get(id)
		if !ok {
			t.Fatalf("cached track %q was not found", id)
		}
		used := time.Unix(int64(i+1), 0)
		if err := os.Chtimes(path, used, used); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := cache.get("a"); !ok {
		t.Fatal("the first cached track was not found")
	}
	installTestAudio(t, cache, "f")
	if _, ok := cache.get("b"); ok {
		t.Error("the least recently used track was not evicted")
	}
	if _, ok := cache.get("a"); !ok {
		t.Error("a recently replayed track was evicted")
	}
	if got := len(cache.filesLocked()); got != 5 {
		t.Errorf("cache has %d files, want 5", got)
	}
}

func TestAudioCacheAtRootUsesPrivateAudioSubdirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	userAudio := filepath.Join(root, "audio")
	if err := os.Mkdir(userAudio, 0o755); err != nil {
		t.Fatal(err)
	}
	userFile := filepath.Join(userAudio, "favorite.mp3")
	if err := os.WriteFile(userFile, []byte("keep this"), 0o600); err != nil {
		t.Fatal(err)
	}
	cache, gotRoot, err := newAudioCacheAtRoot(root, 3)
	if err != nil {
		t.Fatal(err)
	}
	if gotRoot != root {
		t.Errorf("cache root = %q, want %q", gotRoot, root)
	}
	if cache.dir != audioCachePath(root) {
		t.Errorf("cache directory = %q, want %q", cache.dir, audioCachePath(root))
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if rootInfo.Mode().Perm() != 0o755 {
		t.Errorf("selected folder permissions changed to %04o", rootInfo.Mode().Perm())
	}
	cacheInfo, err := os.Stat(cache.dir)
	if err != nil {
		t.Fatal(err)
	}
	if cacheInfo.Mode().Perm() != 0o700 {
		t.Errorf("app-owned cache folder permissions = %04o, want 0700", cacheInfo.Mode().Perm())
	}
	if contents, err := os.ReadFile(userFile); err != nil || string(contents) != "keep this" {
		t.Errorf("cache setup changed unrelated audio file: contents %q, error %v", contents, err)
	}
}

func TestAudioCacheDisablesAndClearsAtZero(t *testing.T) {
	cache, err := newAudioCache(t.TempDir(), 5)
	if err != nil {
		t.Fatal(err)
	}
	installTestAudio(t, cache, "cached")
	cache.setLimit(0)
	if got := len(cache.filesLocked()); got != 0 {
		t.Fatalf("turning the cache off left %d files", got)
	}
	if _, ok := cache.get("cached"); ok {
		t.Fatal("the disabled cache returned a file")
	}
}

func TestAudioCacheTreatsCompletedDownloadAsMostRecentlyUsed(t *testing.T) {
	cache, err := newAudioCache(t.TempDir(), 2)
	if err != nil {
		t.Fatal(err)
	}
	installTestAudio(t, cache, "older")
	installTestAudio(t, cache, "newer")
	for id, used := range map[string]time.Time{
		"older": time.Unix(1, 0),
		"newer": time.Unix(2, 0),
	} {
		path, ok := cache.get(id)
		if !ok {
			t.Fatalf("cached track %q was not found", id)
		}
		if err := os.Chtimes(path, used, used); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	temp := filepath.Join(dir, "audio.webm")
	if err := os.WriteFile(temp, []byte("just downloaded"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(0, 0)
	if err := os.Chtimes(temp, old, old); err != nil {
		t.Fatal(err)
	}
	cache.mu.Lock()
	err = cache.installLocked("downloaded", temp)
	cache.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.get("downloaded"); !ok {
		t.Fatal("the newly completed download was treated as least recently used")
	}
	if _, ok := cache.get("older"); ok {
		t.Error("the track last used first was not evicted")
	}
}

func TestAudioCacheEnqueueDownloadsOnceInBackground(t *testing.T) {
	cache, err := newAudioCache(t.TempDir(), 5)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	cookieSeen := make(chan string, 1)
	cache.download = func(_ context.Context, id, dir, cookie string) (string, error) {
		cookieSeen <- cookie
		close(started)
		<-release
		tempDir, err := os.MkdirTemp(dir, ".test-audio-")
		if err != nil {
			return "", err
		}
		path := filepath.Join(tempDir, "audio.webm")
		if err := os.WriteFile(path, []byte(id), 0o600); err != nil {
			return "", err
		}
		return path, nil
	}
	cookie := "SID=private-session"
	cache.enqueue("track", cookie)
	<-started
	cache.enqueue("track", "")
	close(release)
	if got := <-cookieSeen; got != cookie {
		t.Errorf("download cookie = %q, want the signed-in session", got)
	}
	waitFor(t, func() bool {
		cache.mu.Lock()
		pending := len(cache.pending)
		cache.mu.Unlock()
		if pending != 0 {
			return false
		}
		_, ok := cache.get("track")
		return ok
	})
	if got := len(cache.filesLocked()); got != 1 {
		t.Errorf("one track was queued more than once: %d files", got)
	}
	for _, entry := range mustReadDir(t, cache.dir) {
		if entry.IsDir() {
			t.Errorf("temporary download directory was left behind: %s", entry.Name())
		}
	}
}

func TestTurningAudioCacheOffCancelsTheActiveDownload(t *testing.T) {
	cache, err := newAudioCache(t.TempDir(), 5)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	cache.download = func(ctx context.Context, _, _, _ string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	cache.enqueue("track", "")
	<-started
	cache.setLimit(0)
	waitFor(t, func() bool {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		return len(cache.pending) == 0
	})
	if got := len(cache.filesLocked()); got != 0 {
		t.Errorf("turning the cache off stored %d files", got)
	}
}

func TestClosingAudioCacheStopsDownloadsAndKeepsCompletedFiles(t *testing.T) {
	cache, err := newAudioCache(t.TempDir(), 5)
	if err != nil {
		t.Fatal(err)
	}
	installTestAudio(t, cache, "completed")
	started := make(chan struct{})
	cache.download = func(ctx context.Context, _, _, _ string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	cache.enqueue("in-progress", "")
	<-started
	cache.close()
	if _, ok := cache.get("completed"); !ok {
		t.Error("closing the cache deleted completed audio")
	}
	if _, ok := cache.get("in-progress"); ok {
		t.Error("closing the cache stored an incomplete download")
	}
	cache.enqueue("after-close", "")
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.pending) != 0 {
		t.Errorf("closing the cache left pending downloads: %v", cache.pending)
	}
}

func TestAudioCacheRejectsInvalidLimitsAndSecuresFiles(t *testing.T) {
	if got := validAudioCacheLimit(37); got != 37 {
		t.Errorf("custom cache limit = %d, want 37", got)
	}
	if got := validAudioCacheLimit(-1); got != defaultAudioCacheLimit {
		t.Errorf("negative cache limit = %d, want default %d", got, defaultAudioCacheLimit)
	}
	dir := t.TempDir()
	cache, err := newAudioCache(dir, 5)
	if err != nil {
		t.Fatal(err)
	}
	installTestAudio(t, cache, "private")
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("cache directory permissions = %04o, want 0700", info.Mode().Perm())
	}
	path, ok := cache.get("private")
	if !ok {
		t.Fatal("installed audio was not found")
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("cached audio permissions = %04o, want 0600", info.Mode().Perm())
	}
}

func installTestAudio(t *testing.T, cache *audioCache, id string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "audio.webm")
	if err := os.WriteFile(path, []byte(id), 0o600); err != nil {
		t.Fatal(err)
	}
	cache.mu.Lock()
	err := cache.installLocked(id, path)
	cache.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}

func mustReadDir(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}
