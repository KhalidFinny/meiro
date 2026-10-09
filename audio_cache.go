package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// defaultAudioCacheLimit keeps a short run of recent songs for quick replay.
const defaultAudioCacheLimit = 10

// audioCachePath keeps cache files in an app-owned child directory, so
// choosing a parent folder does not change that folder's permissions.
func audioCachePath(root string) string { return filepath.Join(root, "Meiro Audio Cache") }

func newAudioCacheAtRoot(root string, limit int) (*audioCache, string, error) {
	if strings.TrimSpace(root) == "" {
		return nil, "", errors.New("cache directory is empty")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, "", err
	}
	if !info.IsDir() {
		return nil, "", fmt.Errorf("%q is not a directory", absolute)
	}
	cache, err := newAudioCache(audioCachePath(absolute), limit)
	if err != nil {
		return nil, "", err
	}
	return cache, absolute, nil
}

// audioCache keeps completed audio files for recently played tracks. It is a
// private app cache, not a user-facing download library.
type audioCache struct {
	mu       sync.Mutex
	dir      string
	limit    int
	queue    []audioCacheDownload
	pending  map[string]struct{}
	running  bool
	closed   bool
	cancel   context.CancelFunc
	worker   sync.WaitGroup
	download func(context.Context, string, string, string) (string, error)
}

type audioCacheDownload struct {
	videoID string
	cookie  string
}

// newAudioCache prepares a private directory and removes any files over the
// configured limit left by an earlier run.
func newAudioCache(dir string, limit int) (*audioCache, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	c := &audioCache{
		dir:      dir,
		limit:    validAudioCacheLimit(limit),
		pending:  make(map[string]struct{}),
		download: downloadAudio,
	}
	c.mu.Lock()
	c.trimLocked("")
	c.mu.Unlock()
	return c, nil
}

// validAudioCacheLimit rejects negative cache sizes. The user may choose any
// non-negative count; zero disables the cache.
func validAudioCacheLimit(limit int) int {
	if limit < 0 {
		return defaultAudioCacheLimit
	}
	return limit
}

// setLimit applies a new cache size and evicts old files immediately.
func (c *audioCache) setLimit(limit int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.limit = validAudioCacheLimit(limit)
	if c.limit == 0 && c.cancel != nil {
		c.cancel()
	}
	c.trimLocked("")
	c.mu.Unlock()
}

// warmable reports whether the cache keeps enough tracks for one downloaded
// ahead of time to survive until it plays. At a limit of one, the warmed
// track evicts the track that is playing, so nothing is gained.
func (c *audioCache) warmable() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed && c.limit > 1
}

// get returns a cached file and moves it to the most-recently-used end.
func (c *audioCache) get(videoID string) (string, bool) {
	if c == nil || videoID == "" {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.limit == 0 {
		return "", false
	}
	path := c.findLocked(videoID)
	if path == "" {
		return "", false
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	return path, true
}

// enqueue downloads videoID in the background unless it is already cached or
// queued. One worker keeps disk use within the configured item limit.
func (c *audioCache) enqueue(videoID, cookie string) {
	if c == nil || videoID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.limit == 0 || c.findLocked(videoID) != "" {
		return
	}
	if _, exists := c.pending[videoID]; exists {
		return
	}
	c.pending[videoID] = struct{}{}
	c.queue = append(c.queue, audioCacheDownload{videoID: videoID, cookie: cookie})
	if c.running {
		return
	}
	c.running = true
	c.worker.Add(1)
	go func() {
		defer c.worker.Done()
		c.drain()
	}()
}

// close cancels the active download and waits for the worker to exit before
// the app process shuts down.
func (c *audioCache) close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.closed = true
	c.queue = nil
	c.pending = make(map[string]struct{})
	if c.cancel != nil {
		c.cancel()
	}
	c.mu.Unlock()
	c.worker.Wait()
}

func (c *audioCache) drain() {
	for {
		c.mu.Lock()
		if len(c.queue) == 0 {
			c.running = false
			c.mu.Unlock()
			return
		}
		download := c.queue[0]
		c.queue[0] = audioCacheDownload{}
		c.queue = c.queue[1:]
		limit := c.limit
		var ctx context.Context
		var cancel context.CancelFunc
		if limit > 0 {
			ctx, cancel = context.WithTimeout(context.Background(), 30*time.Minute)
			c.cancel = cancel
		}
		c.mu.Unlock()

		if limit > 0 {
			temp, err := c.download(ctx, download.videoID, c.dir, download.cookie)
			cancelled := ctx.Err() != nil
			cancel()
			c.mu.Lock()
			c.cancel = nil
			if err == nil && c.limit > 0 {
				if installErr := c.installLocked(download.videoID, temp); installErr != nil {
					log.Printf("caching audio video_id=%s: %v", download.videoID, installErr)
				}
			} else if err != nil && !cancelled {
				log.Printf("caching audio video_id=%s: %v", download.videoID, err)
			}
			c.mu.Unlock()
			if temp != "" {
				_ = os.RemoveAll(filepath.Dir(temp))
			}
		}

		c.mu.Lock()
		delete(c.pending, download.videoID)
		c.mu.Unlock()
	}
}

// installLocked moves a completed temporary download into the cache and
// evicts the least recently used files until the limit is met.
func (c *audioCache) installLocked(videoID, temp string) error {
	if temp == "" {
		return errors.New("yt-dlp returned no audio file")
	}
	if c.findLocked(videoID) != "" {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(temp))
	if !audioCacheExtension(ext) {
		return fmt.Errorf("yt-dlp returned an unsupported audio file %q", ext)
	}
	if err := os.Chmod(temp, 0o600); err != nil {
		return fmt.Errorf("secure audio file: %w", err)
	}
	now := time.Now()
	if err := os.Chtimes(temp, now, now); err != nil {
		return fmt.Errorf("mark audio as recently used: %w", err)
	}
	c.trimLocked("")
	final := filepath.Join(c.dir, audioCacheKey(videoID)+ext)
	if err := os.Rename(temp, final); err != nil {
		return fmt.Errorf("store audio file: %w", err)
	}
	c.trimLocked(audioCacheKey(videoID))
	return nil
}

// findLocked returns the audio file for videoID. The caller holds c.mu.
func (c *audioCache) findLocked(videoID string) string {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return ""
	}
	prefix := audioCacheKey(videoID) + "."
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) || !audioCacheExtension(filepath.Ext(entry.Name())) {
			continue
		}
		return filepath.Join(c.dir, entry.Name())
	}
	return ""
}

// trimLocked evicts the least-recently-used files until the cache is under
// its limit, except for keep. The caller holds c.mu.
func (c *audioCache) trimLocked(keep string) {
	for {
		files := c.filesLocked()
		if len(files) <= c.limit {
			return
		}
		oldest := -1
		for i := range files {
			if files[i].key == keep {
				continue
			}
			if oldest < 0 || files[i].used.Before(files[oldest].used) {
				oldest = i
			}
		}
		if oldest < 0 {
			return
		}
		if err := os.Remove(files[oldest].path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return
		}
	}
}

type audioCacheFile struct {
	key  string
	path string
	used time.Time
}

func (c *audioCache) filesLocked() []audioCacheFile {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return nil
	}
	files := make([]audioCacheFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !audioCacheExtension(filepath.Ext(entry.Name())) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		key, _, ok := strings.Cut(entry.Name(), ".")
		if !ok || len(key) != sha256.Size*2 {
			continue
		}
		if _, err := hex.DecodeString(key); err != nil {
			continue
		}
		files = append(files, audioCacheFile{key: key, path: filepath.Join(c.dir, entry.Name()), used: info.ModTime()})
	}
	return files
}

func audioCacheKey(videoID string) string {
	sum := sha256.Sum256([]byte(videoID))
	return hex.EncodeToString(sum[:])
}

// audioCacheExtension accepts the containers yt-dlp saves an audio-only
// format in: WebM audio arrives as .webm or .weba, MP4 audio as .m4a or .mp4.
func audioCacheExtension(ext string) bool {
	switch strings.ToLower(ext) {
	case ".aac", ".flac", ".m4a", ".mkv", ".mp3", ".mp4", ".ogg", ".opus", ".wav", ".weba", ".webm":
		return true
	default:
		return false
	}
}

// downloadAudio asks yt-dlp to save a song into a temporary directory inside
// the cache. The caller atomically installs it after the download succeeds.
func downloadAudio(ctx context.Context, videoID, cacheDir, cookie string) (string, error) {
	path, err := toolPath("yt-dlp")
	if err != nil {
		return "", errors.New("yt-dlp is not installed or not on PATH")
	}
	tempDir, err := os.MkdirTemp(cacheDir, ".audio-*")
	if err != nil {
		return "", fmt.Errorf("prepare audio cache: %w", err)
	}
	_, runtimeArgs := ytDlpJSRuntimeArgs()
	options := runtimeArgs
	if cookie != "" {
		cookieFile, err := ytDlpCookieFile(cookie)
		if err != nil {
			_ = os.RemoveAll(tempDir)
			return "", fmt.Errorf("prepare yt-dlp authentication: %w", err)
		}
		defer os.Remove(cookieFile)
		options = append(options, "--cookies", cookieFile)
	}
	args := audioCacheDownloadArgs(videoID, tempDir, options...)
	command := exec.CommandContext(ctx, path, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		_ = os.RemoveAll(tempDir)
		if reason := lastError(string(output)); reason != "" {
			return "", fmt.Errorf("yt-dlp could not cache the audio: %s", reason)
		}
		return "", fmt.Errorf("yt-dlp could not cache the audio: %w", err)
	}
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return "", fmt.Errorf("read downloaded audio: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".part") || !strings.HasPrefix(entry.Name(), "audio.") {
			continue
		}
		return filepath.Join(tempDir, entry.Name()), nil
	}
	_ = os.RemoveAll(tempDir)
	return "", errors.New("yt-dlp finished without an audio file")
}

// audioCacheDownloadArgs asks yt-dlp for the track's own audio stream. It
// keeps that stream as it is: YouTube Music already serves Opus in WebM or
// AAC in MP4, which the player reads, and re-encoding it through ffmpeg
// would spend CPU and lose a generation of quality for nothing.
func audioCacheDownloadArgs(videoID, tempDir string, options ...string) []string {
	args := []string{
		"-f", "bestaudio",
		"--no-playlist", "--no-warnings", "--no-progress",
		"-o", filepath.Join(tempDir, "audio.%(ext)s"),
	}
	args = append(args, options...)
	return append(args, "https://music.youtube.com/watch?v="+url.QueryEscape(videoID))
}
