# Meiro

A small desktop client for YouTube Music, built with
[MyGo](https://mygo.egoist.dev/). Its window is native UI that MyGo draws
itself, so the app starts at once and needs no web frontend.

## What it does

- Browses the public catalogue: the home and explore feeds, search, albums,
  playlists and artists.
- Signs in with a Google account through the OAuth device flow, and keeps
  the session across restarts.
- Shows the account's library when signed in.
- Plays music: play, pause, seek, previous, next and volume.

The interface is black and white, and follows the desktop's light or dark
appearance.

## Use

The sidebar names four pages: **Home** and **Explore** show the feeds YouTube
Music offers, **Search** finds songs, albums, artists and playlists, and
**Library** shows your own collection. Opening an album, an artist or a
playlist from any of them shows its page.

Click a song to play it, from that point in the page; double-click or press
Enter to do the same from the keyboard. **Play** in a page's heading plays
everything on it. The player bar below the pages has the transport, a
scrubber, and the volume.

Every page that lists items draws them in the layout the heading's picker
chooses: **Rows**, denser **Compact** rows, **Columns**, a **Grid** of
cards, or a **Split** list beside a pane describing the item you are on.
The choice applies to every page.

| Keys | What they do |
| --- | --- |
| Space | Play or pause |
| Cmd+← and Cmd+→ | Previous and next track |

## Requirements

- Go 1.27.1 or later.
- `ffmpeg` to decode audio.
- `yt-dlp` to resolve a stream URL when the `youtube` package cannot
  decipher YouTube's player script, which is the usual case today.

On macOS 12 or later, MyGo draws the interface with Metal and needs no
other system libraries. On Linux, the app needs GTK 3; `.agents/setup`
installs it in an Amp orb.

## Run

```sh
go tool mygo dev
```

## Sign in

Choose **Sign in with Google**. The dialog shows a code: open the page it
links to and enter the code there. Meiro keeps the tokens in your login
keychain on macOS, through the `security` tool, and in the Secret Service
on Linux, through `secret-tool`, so the next start is already signed in. A
system with neither keeps them in a file in the app's data directory that
only your user can read. **Sign out** removes them, here and at Google.

## Test and build

```sh
go test ./...
go tool mygo build
```

Almost every test runs without the network. Three are held back behind an
environment variable: they play a real track, which also needs `ffmpeg` and
`yt-dlp`, and write to your own keychain.

```sh
MEIRO_LIVE_PLAYBACK=1 go test -run TestLivePlayback -v ./...
MEIRO_LIVE_KEYCHAIN=1 go test -run TestLiveKeychain -v .
```

## Packages

### `youtube`

A read-only YouTube Music client. It supports OAuth device login and token
refresh, account details, music search, home and explore feeds, account and
channel details, complete available library pages, artist/album/playlist
browsing, track metadata and stream formats, lyrics, related tracks,
listening recap, the up-next queue, and search suggestions. Browse and
search results expose continuation tokens and typed sections. InnerTube
responses are also exposed as raw JSON to keep the package useful as
upstream formats evolve.

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

Public music metadata does not require OAuth. Account and library calls do.
The client does not make playlist or account changes.

For sign-in across app restarts, provide an implementation of
`youtube.TokenStore`. `OAuth.Restore` loads saved tokens, and device login
and token refresh save them through that store. Meiro implements the store
with the system's credential store, and falls back to a file only its user
can read where the system has none.

Cookie auth can list all channels available to the account. OAuth returns
only the active channel. Cookies are sensitive credentials; do not log them
or put them in source control.

### `player`

Plays one stream at a time. It asks `ffmpeg`, in a child process, to decode
a stream into signed 16-bit stereo PCM, and hands those samples to
[oto](https://github.com/ebitengine/oto), which writes them to the audio
device. Decoding in a child process keeps the app free of cgo and of codecs
of its own, so it plays whatever `ffmpeg` reads. Seeking restarts the decode
at the new position, which is why the app asks for a direct media URL rather
than downloading the track first.
