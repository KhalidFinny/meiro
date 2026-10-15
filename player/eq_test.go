package player

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestEqFilterShapesOnlyTheRaisedBands(t *testing.T) {
	if got := eqFilter(nil); got != "" {
		t.Errorf("eqFilter(nil) = %q, want no chain", got)
	}
	if got := eqFilter(make([]float64, EQBandCount)); got != "" {
		t.Errorf("eqFilter(flat) = %q, want no chain", got)
	}
	bands := make([]float64, EQBandCount)
	bands[0], bands[4], bands[9] = -3, 6, 1.5
	got := eqFilter(bands)
	want := "volume=-6.0dB," +
		"equalizer=f=31:t=q:w=1.4:g=-3.0," +
		"equalizer=f=500:t=q:w=1.4:g=6.0," +
		"equalizer=f=16000:t=q:w=1.4:g=1.5"
	if got != want {
		t.Errorf("eqFilter = %q, want %q", got, want)
	}
	// Cuts only attenuate, so they need no headroom.
	cuts := make([]float64, EQBandCount)
	cuts[0], cuts[4] = -3, -6
	if got := eqFilter(cuts); strings.Contains(got, "volume=") {
		t.Errorf("eqFilter(cuts) = %q, want no headroom filter", got)
	}
}

func TestStreamArgsCarriesTheChain(t *testing.T) {
	plain := streamArgs("https://example.invalid/audio", 0, nil)
	if slices.Contains(plain, "-af") {
		t.Errorf("streamArgs without bands = %v, want no filter argument", plain)
	}
	bands := make([]float64, EQBandCount)
	bands[2] = 4
	shaped := streamArgs("https://example.invalid/audio", 0, bands)
	i := slices.Index(shaped, "-af")
	if i < 0 || i+1 >= len(shaped) {
		t.Fatalf("streamArgs with bands = %v, want a filter argument", shaped)
	}
	if !strings.Contains(shaped[i+1], "equalizer=f=125") {
		t.Errorf("streamArgs chain = %q, want a 125 Hz band", shaped[i+1])
	}
	if got := shaped[len(shaped)-1]; got != "pipe:1" {
		t.Errorf("last argument = %q, want the output pipe", got)
	}
}

func TestSetEQIgnoresAWrongLengthTable(t *testing.T) {
	p := New(withAudio(&sharedAudio{open: func() (device, error) {
		return nil, errTestDevice
	}}))
	p.SetEQ(make([]float64, EQBandCount-1))
	if p.eqBands != nil {
		t.Errorf("SetEQ with a short table stored %v, want nothing", p.eqBands)
	}
	p.SetEQ(make([]float64, EQBandCount))
	if len(p.eqBands) != EQBandCount {
		t.Errorf("SetEQ stored %d bands, want %d", len(p.eqBands), EQBandCount)
	}
	p.SetEQ(nil)
	if p.eqBands != nil {
		t.Errorf("SetEQ(nil) left %v in the bands, want the equaliser off", p.eqBands)
	}
}

// errTestDevice fails the audio device, which no test here needs.
var errTestDevice = errors.New("player: no audio device in this test")
