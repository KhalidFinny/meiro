# Third-party software

A release of Meiro ships three programs next to the app. Meiro runs them as
separate processes.

## ffmpeg

Decodes the audio. This is the portable GPL build of
[jellyfin-ffmpeg](https://github.com/jellyfin/jellyfin-ffmpeg), which is
FFmpeg, licensed under the GNU General Public License version 3 (the text is
in `licenses/ffmpeg-COPYING.GPLv3`). Its source, and the exact configuration
it was built with, are at
<https://github.com/jellyfin/jellyfin-ffmpeg/releases>, under the tag of the
version shipped (`ffmpeg -version` prints it). FFmpeg is a trademark of
Fabrice Bellard.

## yt-dlp

Finds a stream to play and reads your browser's YouTube session. This is the
standalone build from <https://github.com/yt-dlp/yt-dlp>, released into the
public domain under the Unlicense. It bundles other free software; its
repository lists it.

## QuickJS

Runs the JavaScript that yt-dlp needs to solve YouTube's challenges. This is
the QuickJS engine from <https://github.com/quickjs-ng/quickjs>, licensed under
the MIT License (the text is in `licenses/quickjs-MIT.txt`).
