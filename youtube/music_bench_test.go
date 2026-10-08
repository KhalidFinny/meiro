package youtube

import (
	"fmt"
	"strings"
	"testing"
)

func benchResponse() []byte {
	var b strings.Builder
	b.WriteString(`{"contents":{"sectionListRenderer":{"contents":[`)
	for s := 0; s < 12; s++ {
		if s > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"musicCarouselShelfRenderer":{"header":{"musicCarouselShelfBasicHeaderRenderer":{"title":{"runs":[{"text":"Shelf %d"}]}}},"contents":[`, s)
		for i := 0; i < 20; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `{"musicTwoRowItemRenderer":{"title":{"runs":[{"text":"Title %d-%d","navigationEndpoint":{"browseEndpoint":{"browseId":"MPREb_%d_%d"}}}]},"subtitle":{"runs":[{"text":"Artist"}]},"thumbnailRenderer":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://x/a=w60-h60","width":60},{"url":"https://x/a=w544-h544","width":544}]}}},"menu":{"menuRenderer":{"items":[{"menuServiceItemRenderer":{"text":{"runs":[{"text":"Add to queue"}]},"serviceEndpoint":{"queueAddEndpoint":{"queueTarget":{"videoId":"v%d%d"}}}}}]}},"navigationEndpoint":{"browseEndpoint":{"browseId":"MPREb_%d_%d"}}}}`, s, i, s, i, s, i, s, i)
		}
		b.WriteString(`]}}`)
	}
	b.WriteString(`]}}}`)
	return []byte(b.String())
}

// BenchmarkNewBrowseResult is the cost of reading a home page: twelve shelves of
// twenty cards.
func BenchmarkNewBrowseResult(b *testing.B) {
	raw := benchResponse()
	c := NewClient(Options{APIKey: "k"})
	b.SetBytes(int64(len(raw)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.newBrowseResult(raw)
	}
}
