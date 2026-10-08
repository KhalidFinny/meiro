package youtube

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestResolveEncryptedAudioFormat(t *testing.T) {
	playerScript := []byte(`var OPS={swap:function(a,b){var c=a[0];a[0]=a[b%a.length];a[b%a.length]=c},splice:function(a,b){a.splice(0,b)},reverse:function(a){a.reverse()}};function decode(a){a=a.split("");OPS.swap(a,2);OPS.reverse(a,0);OPS.splice(a,1);return a.join("")}if(x.get("n"))&&(b=ABC[0].x||NFn);NFn=function(a){return a.split("").reverse().join("")}`)
	cipher := url.Values{
		"url": {"https://stream.example/videoplayback?foo=bar&n=abc"},
		"s":   {"abcdef"},
		"sp":  {"sig"},
	}.Encode()
	decoder := newPlayerDecipher(playerScript)
	resolved, err := decoder.resolveFormatURL(context.Background(), AudioFormat{SignatureCipher: cipher})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(resolved)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if parsed.Host != "stream.example" || query.Get("foo") != "bar" {
		t.Errorf("resolved URL = %q", resolved)
	}
	if query.Get("sig") != "edabc" || query.Get("n") != "cba" {
		t.Errorf("resolved signature parameters = %#v", query)
	}
}

func TestResolveDirectAudioFormatUnthrottlesN(t *testing.T) {
	playerScript := []byte(`if(x.get("n"))&&(b=ABC[0].x||NFn);NFn=function(a){return a.split("").reverse().join("")}`)
	decoder := newPlayerDecipher(playerScript)
	resolved, err := decoder.resolveFormatURL(context.Background(), AudioFormat{
		URL: "https://stream.example/videoplayback?itag=140&n=throttled",
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("n") != "delttorht" {
		t.Errorf("n parameter = %q, resolved URL %q", parsed.Query().Get("n"), resolved)
	}
}

func TestResolveCombinedPlayerTransform(t *testing.T) {
	playerScript := []byte(`var reverse=function(value){return value.split("").reverse().join("")};var a_=function(a,b,value){return reverse(value)};var e$=function(a,b,value){return value};var hi=function(a,b,value){return value};var q={15:"set"};Ka=function(S,x="",Q=""){S=new g.$R(S,!0);var n=S.get("n");if(n)S.set("n",reverse(n));S.set("alr","yes");Q&&(Q=a_(3,385,e$(15,3720,Q)),S[q[15]](x,hi(3,4133,Q)));return S};`)
	decoder := newPlayerDecipher(playerScript)
	cipher := url.Values{
		"url": {"https://stream.example/videoplayback?itag=140&n=abc"},
		"s":   {"abcdef"},
		"sp":  {"sig"},
	}.Encode()
	resolved, err := decoder.resolveFormatURL(context.Background(), AudioFormat{SignatureCipher: cipher})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(resolved)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("sig") != "fedcba" || query.Get("n") != "cba" || query.Get("alr") != "yes" {
		t.Errorf("combined player transform query = %#v", query)
	}
}

func TestResolvedURLAddsClientVersionAndOptionalPoToken(t *testing.T) {
	decoder := newPlayerDecipher(nil)
	resolved, err := decoder.resolveFormatURLForClient(context.Background(), AudioFormat{
		URL: "https://stream.example/videoplayback?itag=140",
	}, "1.2026.test", "po-token")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("cver") != "1.2026.test" || parsed.Query().Get("pot") != "po-token" {
		t.Errorf("resolved query = %#v", parsed.Query())
	}

	resolved, err = decoder.resolveFormatURLForClient(context.Background(), AudioFormat{
		URL: "https://stream.example/videoplayback?sabr=1",
	}, "1.2026.test", "po-token")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err = url.Parse(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("pot") != "" {
		t.Errorf("SABR URL should not include PO token, query = %#v", parsed.Query())
	}
}

func TestOpenAudioStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/audio" || r.URL.Query().Get("cpn") != "track-cpn" {
			t.Errorf("audio request = %s %s", r.Method, r.URL.String())
		}
		_, _ = io.WriteString(w, "audio payload")
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL})
	response, err := client.OpenAudioStream(context.Background(), AudioFormat{
		MimeType:    "audio/webm",
		ResolvedURL: server.URL + "/audio?cpn=track-cpn",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "audio payload" {
		t.Errorf("stream body = %q", body)
	}
}

func TestSignatureCipherWithoutSupportedPlayerTransform(t *testing.T) {
	decoder := newPlayerDecipher([]byte(`var something=function(){}`))
	cipher := url.Values{
		"url": {"https://stream.example/videoplayback"},
		"s":   {"encrypted"},
	}.Encode()
	if _, err := decoder.resolveFormatURL(context.Background(), AudioFormat{SignatureCipher: cipher}); err == nil {
		t.Fatal("expected an unsupported player signature transform error")
	}
}

func TestPlayerNTransformHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err := evaluatePlayerFunction(ctx, `N=function(a){for(;;){}}`, "input")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("infinite player function error = %v, want context deadline", err)
	}
}
