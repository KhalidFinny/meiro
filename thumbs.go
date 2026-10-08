package main

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
)

// thumbLimit is how many bitmaps the cache keeps before dropping the ones it
// loaded first. MyGo holds a bitmap on the GPU as long as the app uses it,
// so the cache must not grow without end while the user browses.
const thumbLimit = 400

// thumbCache downloads and decodes artwork, once for each URL.
type thumbCache struct {
	client *http.Client
	notify func()

	mu      sync.Mutex
	bitmaps map[string]*ui.Bitmap
	order   []string
	pending map[string]bool
	failed  map[string]bool
}

// newThumbCache returns a cache that calls notify when a bitmap lands, so
// the window draws it.
func newThumbCache(notify func()) *thumbCache {
	return &thumbCache{
		client:  &http.Client{Timeout: 30 * time.Second},
		notify:  notify,
		bitmaps: make(map[string]*ui.Bitmap),
		pending: make(map[string]bool),
		failed:  make(map[string]bool),
	}
}

// bitmap returns the artwork at url, asked for at size pixels across, or nil
// while it downloads or after it failed.
func (t *thumbCache) bitmap(url string, size int) *ui.Bitmap {
	if url == "" {
		return nil
	}
	url = thumbnailURL(url, size)
	t.mu.Lock()
	defer t.mu.Unlock()
	if bitmap, ok := t.bitmaps[url]; ok {
		return bitmap
	}
	if t.pending[url] || t.failed[url] {
		return nil
	}
	t.pending[url] = true
	go t.fetch(url)
	return nil
}

func (t *thumbCache) fetch(url string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var data []byte
	if request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil); err == nil {
		if response, err := t.client.Do(request); err == nil {
			data, _ = io.ReadAll(io.LimitReader(response.Body, 8<<20))
			response.Body.Close()
		}
	}
	var bitmap *ui.Bitmap
	if len(data) > 0 {
		bitmap, _ = ui.DecodeBitmap(data)
	}
	t.mu.Lock()
	delete(t.pending, url)
	if bitmap == nil {
		t.failed[url] = true
		t.mu.Unlock()
		return
	}
	t.bitmaps[url] = bitmap
	t.order = append(t.order, url)
	for len(t.order) > thumbLimit {
		delete(t.bitmaps, t.order[0])
		t.order = t.order[1:]
	}
	t.mu.Unlock()
	t.notify()
}

var (
	thumbnailSizePattern  = regexp.MustCompile(`=w\d+-h\d+`)
	thumbnailScalePattern = regexp.MustCompile(`=s\d+`)
)

// thumbnailURL asks Google's image host for a picture size instead of the
// one the response offered, which is often far larger than the element
// showing it.
func thumbnailURL(url string, size int) string {
	if thumbnailSizePattern.MatchString(url) {
		return thumbnailSizePattern.ReplaceAllString(url, "=w"+strconv.Itoa(size)+"-h"+strconv.Itoa(size))
	}
	if thumbnailScalePattern.MatchString(url) {
		return thumbnailScalePattern.ReplaceAllString(url, "=s"+strconv.Itoa(size))
	}
	return url
}
