# Meiro Counter

A small native desktop counter built with [MyGo](https://mygo.egoist.dev/).

## YouTube Music client

The independent [`youtube`](youtube) package is a read-only YouTube Music
client. It supports OAuth device login and token refresh, account details,
music search, home and explore feeds, account and channel details, complete
available library pages, artist/album/playlist browsing, track metadata and
stream formats, lyrics, related tracks, listening recap, the up-next queue, and
search suggestions. Browse and search results expose continuation tokens and
typed sections. InnerTube responses are also exposed as raw JSON to keep the
package useful as upstream formats evolve.

```go
import (
	"context"
	"fmt"

	"github.com/elianiva/meiro/youtube"
)

func searchMusic(ctx context.Context) error {
	oauth := youtube.NewOAuth(youtube.OAuthConfig{})
	code, err := oauth.BeginDeviceFlow(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("Open %s and enter %s\n", code.VerificationURL, code.UserCode)
	if _, err := oauth.PollForTokens(ctx, code); err != nil {
		return err
	}

	music := youtube.NewClient(youtube.Options{OAuth: oauth})
	results, err := music.Search(ctx, "ambient", youtube.SearchOptions{Type: youtube.SearchSongs})
	if err != nil {
		return err
	}
	fmt.Printf("Found %d songs\n", len(results.Items))
	return nil
}
```

Public music metadata does not require OAuth. Account and library calls do. The
client does not make playlist or account changes. YouTube may return encrypted
stream signatures. `GetTrackInfo` attempts the combined signature/`n` player
transform with safe dependency extraction and supports common legacy
transforms. It reports unsupported or
unsafe player dependencies through each format's `DecipherError` rather than
running them. It also adds the client version and a playback nonce (`cpn`) to
resolved URLs. Use `BestAudioFormat` to select the highest-bitrate playable format, then
`OpenAudioStream` to request its bytes. DASH, HLS, and server-ABR manifest URLs
are exposed but are not parsed; SABR and DRM playback are not implemented.
Some playback requires a PO token; pass a precomputed value with
`Options.PlayerPoToken`. The package does not include an audio decoder/player
or generate PO tokens.

For sign-in across app restarts, provide an implementation of `youtube.TokenStore`
that uses the operating system keychain. `OAuth.Restore` loads saved tokens, and
device login and token refresh save them through that store. The package does
not store OAuth secrets in a plain-text file.

Cookie auth can list all channels available to the account. OAuth returns only
the active channel. Cookies are sensitive credentials; do not log them or put
them in source control.

```go
cookieAuth, err := youtube.NewCookieAuth(browserCookieHeader, youtube.CookieOptions{
	AccountIndex: 0,
})
if err != nil {
	return err
}
music := youtube.NewClient(youtube.Options{CookieAuth: cookieAuth})
accounts, err := music.GetAccounts(ctx)
if err != nil {
	return err
}
fmt.Printf("Found %d accounts\n", len(accounts.Items))
```

## Run

```sh
go tool mygo dev
```

Use `+` and `−` to change the count.

## Test and build

```sh
go test ./...
go tool mygo build
```

## Requirements

This module requires Go 1.27.1 or later. The counter uses MyGo's native UI, so it does not need Bun or a web frontend.

On macOS 12 or later, MyGo draws the native UI with Metal and needs no additional system libraries. Install [Go](https://go.dev/dl/) on macOS. `.agents/setup` only covers Linux.

On Linux, the app needs GTK 3. In an Amp orb, `.agents/setup` installs GTK 3 and the pinned Go 1.27.1 toolchain.

See the MyGo [getting started guide](https://mygo.egoist.dev/docs/getting-started) for platform requirements and the [native UI guide](https://mygo.egoist.dev/docs/ui) for how MyGo draws the interface.
