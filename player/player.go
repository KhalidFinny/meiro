// Package player plays one audio stream at a time.
//
// It asks ffmpeg, in a child process, to decode a stream into signed 16-bit
// stereo PCM, and hands those samples to oto, which writes them to the
// system's audio device. Decoding in a child process keeps the app free of
// cgo and of codecs of its own, and lets it play anything ffmpeg reads.
//
// The zero value is not usable; call New. All methods are safe for
// concurrent use, so the user interface can drive the player from the main
// thread while oto reads samples in the background.
package player

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ebitengine/oto/v3"
)

const (
	// sampleRate, channelCount and bytesPerSample are the PCM format the
	// player asks ffmpeg for and gives oto. 48 kHz stereo is what most
	// YouTube Music audio decodes from.
	sampleRate     = 48000
	channelCount   = 2
	bytesPerSample = 2
	// bytesPerSecond is the size of one second of that PCM stream.
	bytesPerSecond = sampleRate * channelCount * bytesPerSample
	// userAgent is what ffmpeg sends when it opens the stream. YouTube's
	// media servers answer some requests without one with an error.
	userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
)

// ErrNoFFmpeg reports that ffmpeg is not on PATH, which the player needs to
// decode a stream.
var ErrNoFFmpeg = errors.New("player: ffmpeg is not installed or not on PATH")

// Player plays one stream at a time.
type Player struct {
	mu      sync.Mutex
	session *session
	volume  float64

	lookOnce   sync.Once
	ffmpegPath string
	lookErr    error

	audioOnce sync.Once
	audioCtx  *oto.Context
	audioErr  error
}

// New creates a player that is not yet playing anything. It does not touch
// the audio device or the file system.
func New() *Player {
	return &Player{volume: 1}
}

// Available reports whether the player can play at all: it needs ffmpeg.
func (p *Player) Available() bool {
	_, err := p.resolveFFmpeg()
	return err == nil
}

// Play starts url from its beginning, replacing anything playing. It returns
// once ffmpeg has started, not when the stream ends.
func (p *Player) Play(url string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.startLocked(url, 0, false)
}

// Resume starts a paused stream again, and does nothing when it plays or has
// ended.
func (p *Player) Resume() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s := p.session; s != nil && s.paused {
		s.paused = false
		s.out.Play()
	}
}

// Pause holds the stream where it is, keeping a little of it decoded so a
// resume does not stutter.
func (p *Player) Pause() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s := p.session; s != nil && !s.paused {
		s.paused = true
		s.out.Pause()
	}
}

// Toggle pauses a playing stream and resumes a paused one.
func (p *Player) Toggle() {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.session
	if s == nil {
		return
	}
	if s.paused {
		s.paused = false
		s.out.Play()
		return
	}
	s.paused = true
	s.out.Pause()
}

// Seek moves to at within the stream by restarting the decode there. It
// keeps the stream paused when it was paused.
func (p *Player) Seek(at time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.session == nil || at < 0 {
		return
	}
	url, paused := p.session.url, p.session.paused
	_ = p.startLocked(url, at, paused)
}

// Stop ends the stream and forgets it.
func (p *Player) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopLocked()
}

// SetVolume sets the playback gain, 1 for the stream's own level. Values
// below 0 are 0 and above 1 may clip.
func (p *Player) SetVolume(volume float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.volume = max(volume, 0)
	if p.session != nil {
		p.session.out.SetVolume(p.volume)
	}
}

// Volume returns the current gain, 1 by default.
func (p *Player) Volume() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.volume
}

// Active reports whether a stream is loaded, playing, paused or ended.
func (p *Player) Active() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.session != nil
}

// Playing reports whether the stream is playing now.
func (p *Player) Playing() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.session
	return s != nil && !s.paused && !p.endedLocked(s)
}

// Paused reports whether the stream is loaded and held.
func (p *Player) Paused() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.session != nil && p.session.paused
}

// Ended reports whether the stream has been played to its end.
func (p *Player) Ended() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.session != nil && p.endedLocked(p.session)
}

// Position returns how far playback has come, which leads where the user
// hears by the audio device's buffer, a few tens of milliseconds.
func (p *Player) Position() time.Duration {
	p.mu.Lock()
	s := p.session
	p.mu.Unlock()
	if s == nil {
		return 0
	}
	played := s.source.read.Load() - int64(s.out.BufferedSize())
	if played < 0 {
		played = 0
	}
	return s.offset + time.Duration(played*int64(time.Second)/bytesPerSecond)
}

// Failure returns what ffmpeg reported when the decode stopped because of an
// error, as an unreachable stream, and an empty string when the stream ended
// or the app stopped it.
func (p *Player) Failure() string {
	p.mu.Lock()
	s := p.session
	p.mu.Unlock()
	if s == nil || s.stopped {
		return ""
	}
	select {
	case <-s.done:
	default:
		return ""
	}
	message := strings.TrimSpace(s.stderr.String())
	if message == "" {
		message = "ffmpeg stopped"
	}
	return message
}

func (p *Player) endedLocked(s *session) bool {
	return s.source.read.Load() > 0 && s.source.eof.Load() && !s.out.IsPlaying()
}

// startLocked replaces the current stream with url, decoded from at, and
// assumes the caller holds the lock.
func (p *Player) startLocked(url string, at time.Duration, paused bool) error {
	if strings.TrimSpace(url) == "" {
		return errors.New("player: stream URL is empty")
	}
	ffmpeg, err := p.resolveFFmpeg()
	if err != nil {
		return err
	}
	audioCtx, err := p.audioContext()
	if err != nil {
		return err
	}
	p.stopLocked()

	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "5",
		"-user_agent", userAgent,
	}
	if at > 0 {
		args = append(args, "-ss", strconv.FormatFloat(at.Seconds(), 'f', 3, 64))
	}
	args = append(args,
		"-i", url,
		"-vn", "-f", "s16le", "-acodec", "pcm_s16le",
		"-ac", strconv.Itoa(channelCount), "-ar", strconv.Itoa(sampleRate),
		"pipe:1",
	)
	cmd := exec.Command(ffmpeg, args...)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("player: %w", err)
	}
	stderr := &boundedBuffer{limit: 4096}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		_ = pipe.Close()
		return fmt.Errorf("player: start ffmpeg: %w", err)
	}

	s := &session{
		url: url, cmd: cmd, pipe: pipe, stderr: stderr,
		source: &countingReader{reader: pipe},
		offset: at, paused: paused, done: make(chan struct{}),
	}
	s.out = audioCtx.NewPlayer(s.source)
	s.out.SetVolume(p.volume)
	if !paused {
		s.out.Play()
	}
	p.session = s
	go func() {
		waitErr := cmd.Wait()
		p.mu.Lock()
		s.waitErr = waitErr
		p.mu.Unlock()
		close(s.done)
	}()
	return nil
}

// stopLocked ends the current stream, if any, and assumes the caller holds
// the lock.
func (p *Player) stopLocked() {
	s := p.session
	if s == nil {
		return
	}
	p.session = nil
	s.stopped = true
	s.out.PauseAndStopReading()
	_ = s.pipe.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

func (p *Player) resolveFFmpeg() (string, error) {
	p.lookOnce.Do(func() {
		p.ffmpegPath, p.lookErr = exec.LookPath("ffmpeg")
	})
	if p.lookErr != nil {
		return "", ErrNoFFmpeg
	}
	return p.ffmpegPath, nil
}

// audioContext opens the audio device once, and reports why it could not.
func (p *Player) audioContext() (*oto.Context, error) {
	p.audioOnce.Do(func() {
		ctx, _, err := oto.NewContext(&oto.NewContextOptions{
			SampleRate:   sampleRate,
			ChannelCount: channelCount,
			Format:       oto.FormatSignedInt16LE,
		})
		if err != nil {
			p.audioErr = fmt.Errorf("player: open the audio device: %w", err)
			return
		}
		p.audioCtx = ctx
	})
	return p.audioCtx, p.audioErr
}

// session is one decode: a child ffmpeg, the pipe it writes PCM to, and the
// oto player reading that pipe.
type session struct {
	url    string
	cmd    *exec.Cmd
	pipe   io.ReadCloser
	stderr *boundedBuffer
	source *countingReader
	out    *oto.Player
	done   chan struct{}

	// offset is where in the track this decode began.
	offset time.Duration
	paused bool
	// stopped is set when the app, not ffmpeg, ended the decode.
	stopped bool
	waitErr error
}

// countingReader counts the bytes oto has taken from the pipe, and remembers
// the end of the stream.
type countingReader struct {
	reader io.Reader
	read   atomic.Int64
	eof    atomic.Bool
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	c.read.Add(int64(n))
	if errors.Is(err, io.EOF) {
		c.eof.Store(true)
	}
	return n, err
}

// boundedBuffer keeps the first limit bytes written to it, which is enough
// of ffmpeg's error output to explain a failed decode.
type boundedBuffer struct {
	buf   []byte
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - len(b.buf); room > 0 {
		b.buf = append(b.buf, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string { return string(b.buf) }
