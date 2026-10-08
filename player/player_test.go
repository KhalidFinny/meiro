package player

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestCountingReaderCountsAndRemembersTheEnd(t *testing.T) {
	reader := &countingReader{reader: strings.NewReader("abcdef")}
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
