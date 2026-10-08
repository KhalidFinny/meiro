package player

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCountingReaderCountsAndRemembersTheEnd(t *testing.T) {
	reader := newCountingReader(strings.NewReader("abcdef"))
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "abcdef" || reader.read.Load() != 6 {
		t.Errorf("read %q, counted %d", data, reader.read.Load())
	}
	if !reader.eof.Load() {
		t.Error("the end of the stream was not remembered")
	}
}

func TestBoundedBufferKeepsTheStart(t *testing.T) {
	buffer := &boundedBuffer{limit: 4}
	if _, err := buffer.Write([]byte("abcdefgh")); err != nil {
		t.Fatal(err)
	}
	if got := buffer.String(); got != "abcd" {
		t.Errorf("boundedBuffer = %q, want %q", got, "abcd")
	}
}

func TestIdlePlayer(t *testing.T) {
	p := New()
	if p.Active() || p.Playing() || p.Paused() || p.Ended() {
		t.Error("a new player is not idle")
	}
	if p.Position() != 0 || p.Failure() != "" {
		t.Errorf("position %v, failure %q", p.Position(), p.Failure())
	}
	p.Pause()
	p.Resume()
	p.Toggle()
	p.Seek(time.Minute)
	p.Stop()
}

func TestPlayRejectsAnEmptyURL(t *testing.T) {
	if err := New().Play("  "); err == nil {
		t.Fatal("playing an empty URL should fail")
	}
}

func TestAvailableFollowsFFmpeg(t *testing.T) {
	p := New()
	_, err := p.resolveFFmpeg()
	if got := p.Available(); got != (err == nil) {
		t.Errorf("Available = %v, but resolving ffmpeg gave %v", got, err)
	}
	if err != nil && !errors.Is(err, ErrNoFFmpeg) {
		t.Errorf("resolveFFmpeg error = %v, want ErrNoFFmpeg", err)
	}
}

func TestVolumeIsClamped(t *testing.T) {
	p := New()
	p.SetVolume(-1)
	if p.Volume() != 0 {
		t.Errorf("volume = %v after a negative value", p.Volume())
	}
	p.SetVolume(0.5)
	if p.Volume() != 0.5 {
		t.Errorf("volume = %v", p.Volume())
	}
}

func TestBoundedBufferNeverGrowsPastItsLimit(t *testing.T) {
	buffer := &boundedBuffer{limit: 8}
	for range 100 {
		_, _ = buffer.Write(bytes.Repeat([]byte("x"), 32))
	}
	if len(buffer.String()) != 8 {
		t.Errorf("buffer holds %d bytes, want 8", len(buffer.String()))
	}
}

func TestFailureIgnoresACleanExitAfterSound(t *testing.T) {
	done := make(chan struct{})
	close(done)
	source := newCountingReader(strings.NewReader(""))
	s := &session{source: source, stderr: &boundedBuffer{limit: 16}, done: done}
	p := &Player{session: s}

	if got := p.Failure(); got == "" {
		t.Error("ffmpeg exiting before any sound should be a failure")
	}
	source.read.Store(1024)
	if got := p.Failure(); got != "" {
		t.Errorf("a clean exit after sound is the track ending, got failure %q", got)
	}
	s.waitErr = errors.New("exit status 1")
	if got := p.Failure(); got == "" {
		t.Error("ffmpeg dying with an error is a failure")
	}
}

// ffmpeg exits while samples are still in the pipe; the reader must still get
// all of them, and then the end.
func TestSessionReadsEverythingAfterTheProcessExits(t *testing.T) {
	const size = 1 << 20
	cmd := exec.Command("head", "-c", strconv.Itoa(size), "/dev/zero")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := &session{
		cmd: cmd, pipe: pipe, stderr: &boundedBuffer{limit: 16},
		source: newCountingReader(pipe), done: make(chan struct{}), quit: make(chan struct{}),
	}
	p := &Player{session: s}
	go p.reap(s)

	// Take all but the last few bytes, and let the process exit with those
	// still in the pipe, as ffmpeg does a moment before the track ends.
	head := make([]byte, size-1024)
	if _, err := io.ReadFull(s.source, head); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	rest, err := io.ReadAll(s.source)
	if err != nil || len(rest) != 1024 {
		t.Fatalf("read %d of the last 1024 bytes, err %v", len(rest), err)
	}
	if !s.source.eof.Load() {
		t.Error("the end of the stream was not reached")
	}
	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
		t.Fatal("the process was never reaped")
	}
	if got := p.Failure(); got != "" {
		t.Errorf("failure %q after a clean end", got)
	}
}
