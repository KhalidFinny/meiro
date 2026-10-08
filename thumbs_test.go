package main

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
		w.Write(buf.Bytes())
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
			w.Write(buf.Bytes())
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
