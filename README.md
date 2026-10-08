# Meiro

A small desktop client for YouTube Music, built with
[MyGo](https://mygo.egoist.dev/). Its window is native UI that MyGo draws
itself, so the app starts at once and needs no web frontend.

## What it does

- Browses the public catalogue: the home and explore feeds, search, albums,
  playlists and artists.
- Signs in with the cookie of your YouTube Music browser session, and keeps
  it across restarts.
- Shows your own home page and library when signed in.
- Plays music: play, pause, seek, previous, next, shuffle, repeat and volume,
  with the queue and the lyrics of the song in a full-screen player.
- Themes itself from one colour you choose, or from the cover of the song
  that is playing.

The interface is [Material 3 Expressive](https://m3.material.io/blog/building-with-m3-expressive):
a navigation rail, content on a rounded tonal sheet, a floating player, shape
that morphs with state (the play button squares off while music plays), a
wavy scrubber, spring motion, and a colour scheme derived from a seed. Text is
set in [Google Sans](https://github.com/googlefonts/googlesans), embedded from
`fonts/` under the SIL Open Font License (`fonts/OFL.txt`).

## Use

The rail names four pages: **Home** and **Explore** show the shelves YouTube
Music offers, **Search** finds songs, albums, artists and playlists, and
**Library** shows your own collection. **Settings** sits at its foot, and the
menu button above it opens the rail into labels beside icons. Opening an
album, an artist or a playlist from any shelf shows its page.

Click a song to play it, with the rest of its shelf or page queued behind it.
Over an album or playlist card, the round button plays it without opening it.
The player floats over the foot of the page; click it to open the full-screen
player, with the queue and the lyrics beside the artwork.

Search runs when you press Enter, never while you type. Picking another
filter (Songs, Albums, …) searches again for what you last submitted, and the
searches you made are listed on the page, newest first.

### Settings

- **Theme**: follow the desktop, or stay light or dark.
- **Colour**: one of twelve seeds, or any hue on the slider. Every colour in
  the app, light and dark, grows from it, and the window glides to the new
  colours as you choose.
- **Palette**: how the seed is spent: Tonal spot, Vibrant, Expressive,
  Neutral or Monochrome.
- **Colour from artwork**: take the seed from the cover of the song playing.

The choices are kept in `settings.json` in the app's data directory.

| Keys | What they do |
| --- | --- |
| Space | Play or pause |
| Cmd+← and Cmd+→ | Previous and next track |
| Cmd+K | Search |
| Cmd+, | Settings |
| Esc | Close the full-screen player |

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

YouTube Music serves a personal home page and library only to a request that
carries the cookie of a signed-in browser; its OAuth tokens are refused by
the Music web API. So Meiro borrows your browser's session.

Sign in at music.youtube.com in your browser, then choose **Sign in** in
Meiro and pick that browser (Chrome, Safari, Firefox, Brave or Edge). Meiro
reads its YouTube cookies with `yt-dlp --cookies-from-browser`, which it
needs for playback anyway; macOS may ask for your keychain password for
Chrome and Brave, and Safari needs Full Disk Access for Meiro. To do it by
hand instead, paste the value of the `Cookie` request header of a request to
music.youtube.com, from the browser's developer tools.

The cookie is a password: Meiro keeps it in your login
keychain on macOS, through the `security` tool, and in the Secret Service
on Linux, through `secret-tool`, so the next start is already signed in. A
system with neither keeps it in a file in the app's data directory that
only your user can read. **Sign out** removes it. Signing out of YouTube in
the browser, or the cookie expiring, ends the session here too.

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

### `m3`

A Material 3 Expressive kit for MyGo, with no dependency on the rest of the
app. A `m3.Theme` is resolved from a seed colour, a palette style and a mode;
`m3.Provide` makes it the theme of the view and sets MyGo's own theme from
it, so every component, and the widgets MyGo draws itself, take its colours.

- **Colour**: tonal palettes in Oklch (tones follow L\*, chroma is pulled in
  to fit sRGB), the full set of colour roles for light and dark, five palette
  styles, and `Scheme.Mix` for gliding between two schemes.
- **Components**: buttons (five kinds, five sizes, round or square, toggles
  that change shape), icon buttons, FABs, chips, connected button groups, a
  navigation rail (collapsed and expanded), a pill search bar, switches,
  Expressive sliders with a wavy active track, a morphing loading indicator,
  menus, dialogs, snackbars, avatars, artwork frames and carousels.
- **Motion**: springs (`SpatialFast`, `SpatialDefault`, `EffectsDefault`, …)
  that drive transitions and shape morphs.
- **Type and shape**: the Material type scale with emphasised weights, and
  the shape scale as corner radii. `m3.FontFamily` sets the typeface; the app
  registers Google Sans and passes it in.

`go test ./m3` checks that every colour role keeps its contrast, for every
seed hue, style and appearance. With `MEIRO_SNAPSHOTS=/some/dir`, `go test
-run Gallery ./m3` also renders every component to PNG files.

### `youtube`

A read-only YouTube Music client. It supports cookie authentication, account
details, music search, home and explore feeds, account and channel details,
complete available library pages, artist/album/playlist browsing, track
metadata and stream formats, lyrics, related tracks, listening recap, the
up-next queue, and search suggestions. Browse and search results expose
continuation tokens and typed sections. InnerTube responses are also exposed
as raw JSON to keep the package useful as upstream formats evolve.

```go
import (
	"context"
	"fmt"

	"github.com/elianiva/meiro/youtube"
)

func searchMusic(ctx context.Context, cookie string) error {
	auth, err := youtube.NewCookieAuth(cookie, youtube.CookieOptions{})
	if err != nil {
		return err
	}
	music := youtube.NewClient(youtube.Options{CookieAuth: auth})
	results, err := music.Search(ctx, "ambient", youtube.SearchOptions{Type: youtube.SearchSongs})
	if err != nil {
		return err
	}
	fmt.Printf("Found %d songs\n", len(results.Items))
	return nil
}
```

Public music metadata does not require signing in. The home page, account and
library calls do: pass the `Cookie` header of a signed-in browser session,
which must contain `SAPISID`. The cookie is a credential; do not log it or put
it in source control. The client does not make playlist or account changes.

Cookie auth can also list all channels available to the account.

### `player`

Plays one stream at a time. It asks `ffmpeg`, in a child process, to decode
a stream into signed 16-bit stereo PCM, and hands those samples to
[oto](https://github.com/ebitengine/oto), which writes them to the audio
device. Decoding in a child process keeps the app free of cgo and of codecs
of its own, so it plays whatever `ffmpeg` reads. Seeking restarts the decode
at the new position, which is why the app asks for a direct media URL rather
than downloading the track first.
