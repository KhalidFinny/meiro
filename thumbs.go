package main

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg" // artwork decoders, for the colour taken from it
	_ "image/png"
	"io"
	"net/http"
	neturl "net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	_ "golang.org/x/image/webp"

	"github.com/elianiva/meiro/m3"
)

// thumbBudget is how many bytes of pixels the cache keeps before dropping the
// bitmaps that went longest without being drawn. MyGo holds a bitmap on the
// GPU as long as the app uses it, so the cache must not grow without end
// while the user browses. It counts bytes, not bitmaps, because a cover is
// tens of kilobytes at one size and over a megabyte at another.
const thumbBudget = 128 << 20

// thumbSourceBudget bounds compressed pictures kept for rebuilding evicted
// bitmaps without downloading them again.
const thumbSourceBudget = 32 << 20

// thumbRetry is how long a failed download waits before the next try.
const thumbRetry = 10 * time.Second

// thumbCache keeps decoded artwork and a smaller cache of compressed sources.
type thumbCache struct {
	client *http.Client
	notify func()
	// slots bounds downloads and in-memory decodes that are under way.
	slots chan struct{}
	// synth, when set, answers every request without the network: tests give
	// pages artwork of their own making.
	synth func(url string, size int) *ui.Bitmap

	mu      sync.Mutex
	bitmaps map[string]*thumb
	sources map[string]*thumbSource
	// held and sourceHeld count their respective caches' bytes. tick orders
	// bitmap touches and source-cache reads and writes for both LRUs.
	held       int
	sourceHeld int
	tick       uint64
	pending    map[string]bool
	// failed holds when each download last failed. A failure is only held
	// for thumbRetry, so a dropped connection does not leave a cover blank
	// for the rest of the session.
	failed map[string]time.Time
	// sizes remembers which sizes of each picture are in bitmaps, by the
	// picture's URL without its size, so a small copy can stand in while a
	// larger one downloads.
	sizes map[string][]string
}

// thumb is one cached bitmap, the colour taken from it, and what it costs.
type thumb struct {
	bitmap    *ui.Bitmap
	colour    ui.Color
	hasColour bool
	bytes     int
	used      uint64
}

// thumbSource is the compressed picture kept after its bitmap is evicted.
type thumbSource struct {
	data []byte
	used uint64
}

// thumbBytes estimates the memory a bitmap takes: its pixels, and the
// smaller copies MyGo makes of them for drawing it small, which add up to a
// third more.
func thumbBytes(b *ui.Bitmap) int {
	w, h := b.Size()
	return w * h * 4 * 4 / 3
}

// newThumbCache returns a cache that calls notify when a bitmap lands, so
// the window draws it.
func newThumbCache(notify func()) *thumbCache {
	return &thumbCache{
		client:  &http.Client{Timeout: 30 * time.Second},
		notify:  notify,
		bitmaps: make(map[string]*thumb),
		sources: make(map[string]*thumbSource),
		pending: make(map[string]bool),
		failed:  make(map[string]time.Time),
		sizes:   make(map[string][]string),
		slots:   make(chan struct{}, thumbFetches),
	}
}

// bitmap returns the artwork at url, asked for at size pixels across. While
// it downloads or rebuilds from a compressed source, it returns another size
// of the same picture when one is loaded, and nil otherwise.
func (t *thumbCache) bitmap(url string, size int) *ui.Bitmap {
	return t.bitmapIf(url, size, true)
}

// bitmapIf returns a picture only when it may be fetched. Carousel cards
// outside the visible range use this to avoid touching cache entries or
// starting work for clipped children.
func (t *thumbCache) bitmapIf(url string, size int, fetch bool) *ui.Bitmap {
	if url == "" {
		return nil
	}
	if !fetch {
		return nil
	}
	if t.synth != nil {
		return t.synth(url, size)
	}
	url = thumbnailURL(url, size)
	t.mu.Lock()
	defer t.mu.Unlock()
	if entry, ok := t.bitmaps[url]; ok {
		return t.touch(entry)
	}
	if !t.pending[url] {
		if source, ok := t.source(url); ok {
			t.pending[url] = true
			go t.rebuild(url, source)
		} else if failedAt, ok := t.failed[url]; !ok || time.Since(failedAt) > thumbRetry {
			delete(t.failed, url)
			t.pending[url] = true
			go t.fetch(url)
		}
	}
	for _, other := range t.sizes[sizeless(url)] {
		if entry, ok := t.bitmaps[other]; ok {
			return t.touch(entry)
		}
	}
	return nil
}

// touch marks a bitmap as drawn now and returns it.
func (t *thumbCache) touch(entry *thumb) *ui.Bitmap {
	t.tick++
	entry.used = t.tick
	return entry.bitmap
}

// source returns compressed bytes from memory and marks them recently used.
// The returned bytes are immutable and remain valid if the entry is evicted.
// The caller holds t.mu.
func (t *thumbCache) source(url string) ([]byte, bool) {
	entry, ok := t.sources[url]
	if !ok {
		return nil, false
	}
	t.tick++
	entry.used = t.tick
	return entry.data, true
}

// thumbFetches limits concurrent downloads and bitmap rebuilds.
const thumbFetches = 6

// thumbLimit is the largest picture the cache will take.
const thumbLimit = 8 << 20

// download fetches the picture at url.
func (t *thumbCache) download(url string) ([]byte, error) {
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := t.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	// One byte more than the limit tells a picture that fits from one cut off.
	data, err := io.ReadAll(io.LimitReader(response.Body, thumbLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > thumbLimit {
		return nil, fmt.Errorf("larger than %d bytes", thumbLimit)
	}
	return data, nil
}

// fetch downloads and decodes the picture at url, and stores it, or notes that
// it failed.
func (t *thumbCache) fetch(url string) {
	t.slots <- struct{}{}
	defer func() { <-t.slots }()
	data, err := t.download(url)
	if err != nil {
		t.failedFetch(url)
		return
	}
	t.decode(url, data, true)
}

// rebuild decodes a cached picture without fetching it again.
func (t *thumbCache) rebuild(url string, data []byte) {
	t.slots <- struct{}{}
	defer func() { <-t.slots }()
	t.decode(url, data, false)
}

// decode creates a bitmap and colour, retaining downloaded source bytes.
func (t *thumbCache) decode(url string, data []byte, keepSource bool) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.failedFetch(url)
		return
	}
	bitmap := ui.NewBitmap(img)
	colour, hasColour := dominantColour(img)
	t.mu.Lock()
	delete(t.pending, url)
	delete(t.failed, url)
	if keepSource {
		t.storeSource(url, data)
	}
	t.store(url, &thumb{bitmap: bitmap, colour: colour, hasColour: hasColour, bytes: thumbBytes(bitmap)})
	t.mu.Unlock()
	t.notify()
}

// failedFetch notes a failed download or decode and schedules a retry.
func (t *thumbCache) failedFetch(url string) {
	t.mu.Lock()
	delete(t.pending, url)
	t.failed[url] = time.Now()
	t.mu.Unlock()
	// Draw again once the wait is over, so the next frame tries again.
	time.AfterFunc(thumbRetry+time.Second, t.notify)
}

// storeSource keeps compressed bytes under their own LRU budget. The caller
// holds the lock.
func (t *thumbCache) storeSource(url string, data []byte) {
	if len(data) > thumbSourceBudget {
		return
	}
	if old, ok := t.sources[url]; ok {
		t.sourceHeld -= len(old.data)
	}
	t.tick++
	t.sources[url] = &thumbSource{data: data, used: t.tick}
	t.sourceHeld += len(data)
	for t.sourceHeld > thumbSourceBudget {
		oldest, found := "", false
		for key, entry := range t.sources {
			if key != url && (!found || entry.used < t.sources[oldest].used) {
				oldest, found = key, true
			}
		}
		if !found {
			break
		}
		t.sourceHeld -= len(t.sources[oldest].data)
		delete(t.sources, oldest)
	}
}

// store keeps a bitmap that has landed, and drops the ones drawn least
// recently while the cache is over its budget. The caller holds the lock.
func (t *thumbCache) store(url string, entry *thumb) {
	t.bitmaps[url] = entry
	t.touch(entry)
	t.held += entry.bytes
	base := sizeless(url)
	t.sizes[base] = append(t.sizes[base], url)
	for t.held > thumbBudget && len(t.bitmaps) > 1 {
		t.evictLeastRecent(url)
	}
}

// evictLeastRecent drops the bitmap that went longest without being drawn,
// other than keep, which has only just landed.
func (t *thumbCache) evictLeastRecent(keep string) {
	oldest, found := "", false
	for url, entry := range t.bitmaps {
		if url != keep && (!found || entry.used < t.bitmaps[oldest].used) {
			oldest, found = url, true
		}
	}
	if !found {
		return
	}
	t.held -= t.bitmaps[oldest].bytes
	delete(t.bitmaps, oldest)
	base := sizeless(oldest)
	kept := t.sizes[base][:0]
	for _, other := range t.sizes[base] {
		if other != oldest {
			kept = append(kept, other)
		}
	}
	if len(kept) == 0 {
		delete(t.sizes, base)
	} else {
		t.sizes[base] = kept
	}
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
	parsed, err := neturl.Parse(url)
	if err != nil {
		return url
	}
	variant, ext, ok := youtubeThumbnailVariant(parsed)
	if !ok {
		return url
	}
	wanted := "hqdefault"
	if size <= 128 {
		wanted = "default"
	} else if size <= 320 {
		wanted = "mqdefault"
	}
	if youtubeVariantRank(variant) > youtubeVariantRank(wanted) {
		parsed.Path = path.Join(path.Dir(parsed.Path), wanted+ext)
		return parsed.String()
	}
	return url
}

// youtubeThumbnailVariant finds a standard YouTube thumbnail filename.
func youtubeThumbnailVariant(parsed *neturl.URL) (variant, ext string, ok bool) {
	host := parsed.Hostname()
	if host != "ytimg.com" && !strings.HasSuffix(host, ".ytimg.com") {
		return "", "", false
	}
	ext = path.Ext(parsed.Path)
	if ext != ".jpg" && ext != ".webp" {
		return "", "", false
	}
	variant = strings.TrimSuffix(path.Base(parsed.Path), ext)
	return variant, ext, youtubeVariantRank(variant) >= 0
}

// youtubeVariantRank orders the standard thumbnail sizes, from smallest to
// largest, so requests only downgrade a source that is larger than needed.
func youtubeVariantRank(variant string) int {
	switch variant {
	case "default":
		return 0
	case "mqdefault":
		return 1
	case "hqdefault":
		return 2
	case "sddefault":
		return 3
	case "hq720":
		return 4
	case "maxresdefault":
		return 5
	default:
		return -1
	}
}

// sizeless is url without the size Google's image host was asked for.
func sizeless(url string) string {
	if thumbnailSizePattern.MatchString(url) {
		return thumbnailSizePattern.ReplaceAllString(url, "=")
	}
	if thumbnailScalePattern.MatchString(url) {
		return thumbnailScalePattern.ReplaceAllString(url, "=")
	}
	parsed, err := neturl.Parse(url)
	if err != nil {
		return url
	}
	if _, ext, ok := youtubeThumbnailVariant(parsed); ok {
		parsed.Path = path.Join(path.Dir(parsed.Path), "thumbnail"+ext)
		return parsed.String()
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
	if entry, ok := t.bitmaps[thumbnailURL(url, size)]; ok && entry.hasColour {
		return entry.colour, true
	}
	return ui.Color{}, false
}

// dominantColour finds the colour of a picture that a theme should grow from:
// the most vivid hue among the pixels that are neither near black nor near
// white, weighted by how much of the picture it fills and how saturated it is.
func dominantColour(img image.Image) (ui.Color, bool) {
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
