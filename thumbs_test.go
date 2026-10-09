package main

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestThumbCacheRetriesAfterFailure(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			http.Error(w, "busy", http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write(buf.Bytes())
	}))
	defer server.Close()

	landed := make(chan struct{}, 4)
	cache := newThumbCache(func() { landed <- struct{}{} })
	url := server.URL + "/cover=w60-h60-l90-rj"

	if cache.bitmap(url, 640) != nil {
		t.Fatal("bitmap returned before it downloaded")
	}
	waitFor(t, func() bool { cache.mu.Lock(); defer cache.mu.Unlock(); return len(cache.failed) == 1 })

	// Inside the retry wait the failure stands.
	cache.bitmap(url, 640)
	if hits.Load() != 1 {
		t.Fatalf("retried too soon: %d requests", hits.Load())
	}

	// After it, the next draw asks again.
	cache.mu.Lock()
	for key := range cache.failed {
		cache.failed[key] = time.Now().Add(-2 * thumbRetry)
	}
	cache.mu.Unlock()
	cache.bitmap(url, 640)
	waitFor(t, func() bool { return cache.bitmap(url, 640) != nil })
}

func TestThumbCacheStandsInWithSmallerSize(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "w128-h128-l90-rj") {
			_, _ = w.Write(buf.Bytes())
			return
		}
		http.Error(w, "no", http.StatusNotFound)
	}))
	defer server.Close()

	cache := newThumbCache(func() {})
	url := server.URL + "/cover=w60-h60-l90-rj"
	small := func() bool { return cache.bitmap(url, 128) != nil }
	cache.bitmap(url, 128)
	waitFor(t, small)

	// The large size is not there, and may fail, yet the cover shows.
	if cache.bitmap(url, 640) == nil {
		t.Fatal("the small copy did not stand in for the large one")
	}
}

func TestThumbCacheRebuildsEvictedBitmapWithoutDownloading(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(buf.Bytes())
	}))
	defer server.Close()

	cache := newThumbCache(func() {})
	url := server.URL + "/cover=w60-h60"
	cache.bitmap(url, 60)
	waitFor(t, func() bool { return cache.bitmap(url, 60) != nil })
	if hits.Load() != 1 {
		t.Fatalf("initial downloads = %d, want 1", hits.Load())
	}

	cache.mu.Lock()
	cache.evictLeastRecent("")
	if len(cache.bitmaps) != 0 || len(cache.sources) != 1 {
		cache.mu.Unlock()
		t.Fatalf("after eviction: bitmaps = %d, sources = %d; want 0 and 1", len(cache.bitmaps), len(cache.sources))
	}
	cache.mu.Unlock()

	if got := cache.bitmap(url, 60); got != nil {
		t.Fatal("evicted bitmap was returned before it was rebuilt")
	}
	waitFor(t, func() bool { return cache.bitmap(url, 60) != nil })
	if hits.Load() != 1 {
		t.Errorf("rebuilding the bitmap made %d downloads, want 1 total", hits.Load())
	}
}

func TestThumbCacheDoesNotFetchOutsideCarouselRange(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))
	defer server.Close()

	cache := newThumbCache(func() {})
	if got := cache.bitmapIf(server.URL+"/cover=w60-h60", 60, false); got != nil {
		t.Fatal("off-screen request returned a bitmap")
	}
	cache.mu.Lock()
	pending := len(cache.pending)
	cache.mu.Unlock()
	if pending != 0 || hits.Load() != 0 {
		t.Errorf("off-screen request: pending = %d, downloads = %d; want 0 and 0", pending, hits.Load())
	}
}

func TestThumbnailURLChoosesSmallerYouTubeVariants(t *testing.T) {
	cases := []struct {
		name string
		url  string
		size int
		want string
	}{
		{
			name: "card",
			url:  "https://i.ytimg.com/vi/video/hq720.jpg?sqp=token",
			size: 320,
			want: "https://i.ytimg.com/vi/video/mqdefault.jpg?sqp=token",
		},
		{
			name: "row",
			url:  "https://i.ytimg.com/vi/video/hq720.jpg",
			size: 128,
			want: "https://i.ytimg.com/vi/video/default.jpg",
		},
		{
			name: "hero",
			url:  "https://i.ytimg.com/vi/video/maxresdefault.jpg",
			size: 512,
			want: "https://i.ytimg.com/vi/video/hqdefault.jpg",
		},
		{
			name: "do not upscale",
			url:  "https://i.ytimg.com/vi/video/default.jpg",
			size: 320,
			want: "https://i.ytimg.com/vi/video/default.jpg",
		},
		{
			name: "unrecognized host",
			url:  "https://images.example/video/hq720.jpg",
			size: 320,
			want: "https://images.example/video/hq720.jpg",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := thumbnailURL(test.url, test.size); got != test.want {
				t.Errorf("thumbnailURL(%q, %d) = %q, want %q", test.url, test.size, got, test.want)
			}
		})
	}
	if got, want := sizeless("https://i.ytimg.com/vi/video/mqdefault.jpg?sqp=token"), "https://i.ytimg.com/vi/video/thumbnail.jpg?sqp=token"; got != want {
		t.Errorf("sizeless YouTube URL = %q, want %q", got, want)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestThumbCacheDropsTheBitmapDrawnLeastRecently(t *testing.T) {
	cache := newThumbCache(func() {})
	cover := func(n string) string { return "https://covers.example/" + n + "=w320-h320" }
	size := thumbBudget/3 + 1
	for _, name := range []string{"a", "b", "c"} {
		cache.mu.Lock()
		cache.store(cover(name), &thumb{bitmap: &ui.Bitmap{}, bytes: size})
		cache.mu.Unlock()
		if name == "b" {
			// a is drawn again, so b is the one that has gone unseen longest.
			cache.mu.Lock()
			cache.touch(cache.bitmaps[cover("a")])
			cache.mu.Unlock()
		}
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if _, ok := cache.bitmaps[cover("b")]; ok {
		t.Error("b was kept although it was drawn least recently")
	}
	if _, ok := cache.bitmaps[cover("a")]; !ok {
		t.Error("a was dropped although it was drawn again")
	}
	if _, ok := cache.bitmaps[cover("c")]; !ok {
		t.Error("c was dropped on landing")
	}
	if cache.held != 2*size || len(cache.sizes) != 2 {
		t.Errorf("held = %d (want %d), sizes = %d (want 2)", cache.held, 2*size, len(cache.sizes))
	}
}

// A page asks for dozens of covers at once; only a few may download together.
func TestThumbCacheLimitsConcurrentDownloads(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	var active, peak atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		_, _ = w.Write(buf.Bytes())
	}))
	defer server.Close()

	cache := newThumbCache(func() {})
	const covers = 30
	for i := range covers {
		cache.bitmap(server.URL+"/cover"+strconv.Itoa(i)+"=w60-h60", 60)
	}
	waitFor(t, func() bool { cache.mu.Lock(); defer cache.mu.Unlock(); return len(cache.bitmaps) == covers })
	if got := peak.Load(); got > thumbFetches {
		t.Errorf("%d downloads ran together, want at most %d", got, thumbFetches)
	}
}
