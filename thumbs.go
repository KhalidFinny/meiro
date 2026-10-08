package main

import (
	"bytes"
	"context"
	"image"
	_ "image/jpeg" // artwork decoders, for the colour taken from it
	_ "image/png"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	_ "golang.org/x/image/webp"

	"github.com/elianiva/meiro/m3"
)

// thumbLimit is how many bitmaps the cache keeps before dropping the ones it
// loaded first. MyGo holds a bitmap on the GPU as long as the app uses it,
// so the cache must not grow without end while the user browses.
const thumbLimit = 400

// thumbCache downloads and decodes artwork, once for each URL.
type thumbCache struct {
	client *http.Client
	notify func()
	// synth, when set, answers every request without the network: tests give
	// pages artwork of their own making.
	synth func(url string, size int) *ui.Bitmap

	mu      sync.Mutex
	bitmaps map[string]*ui.Bitmap
	colours map[string]ui.Color
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
		colours: make(map[string]ui.Color),
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
	if t.synth != nil {
		return t.synth(url, size)
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
	if colour, ok := dominantColour(data); ok {
		t.colours[url] = colour
	}
	t.order = append(t.order, url)
	for len(t.order) > thumbLimit {
		delete(t.bitmaps, t.order[0])
		delete(t.colours, t.order[0])
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

// colour returns the colour that stands out in the artwork at url, asked for
// at size pixels across, once the artwork has landed.
func (t *thumbCache) colour(url string, size int) (ui.Color, bool) {
	if url == "" {
		return ui.Color{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	c, ok := t.colours[thumbnailURL(url, size)]
	return c, ok
}

// dominantColour finds the colour of a picture that a theme should grow from:
// the most vivid hue among the pixels that are neither near black nor near
// white, weighted by how much of the picture it fills and how saturated it is.
func dominantColour(data []byte) (ui.Color, bool) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return ui.Color{}, false
	}
	const bins, grid = 24, 28
	var weight [bins]float64
	var sumR, sumG, sumB [bins]float64
	b := img.Bounds()
	for gy := 0; gy < grid; gy++ {
		for gx := 0; gx < grid; gx++ {
			x := b.Min.X + (gx*2+1)*b.Dx()/(grid*2)
			y := b.Min.Y + (gy*2+1)*b.Dy()/(grid*2)
			r, g, bl, _ := img.At(x, y).RGBA()
			colour := ui.RGB(uint8(r>>8), uint8(g>>8), uint8(bl>>8))
			hue, chroma := m3.HueOf(colour)
			light := float64(colour.R)*0.2126 + float64(colour.G)*0.7152 + float64(colour.B)*0.0722
			if chroma < 0.04 || light < 28 || light > 238 {
				continue
			}
			bin := int(hue/360*bins) % bins
			w := chroma * chroma
			weight[bin] += w
			sumR[bin] += float64(colour.R) * w
			sumG[bin] += float64(colour.G) * w
			sumB[bin] += float64(colour.B) * w
		}
	}
	best := -1
	for i := range weight {
		// A hue spreads over its neighbours, so a smooth gradient wins over
		// one stray pixel.
		w := weight[i] + 0.5*(weight[(i+1)%bins]+weight[(i+bins-1)%bins])
		if w > 0 && (best < 0 || w > weight[best]+0.5*(weight[(best+1)%bins]+weight[(best+bins-1)%bins])) {
			best = i
		}
	}
	if best < 0 || weight[best] == 0 {
		return ui.Color{}, false
	}
	return ui.RGB(uint8(sumR[best]/weight[best]), uint8(sumG[best]/weight[best]), uint8(sumB[best]/weight[best])), true
}
