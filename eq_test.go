package main

import (
	"math"
	"testing"
)

// normalizeGainTests derive their expectations from the arithmetic itself:
// gain is 10^(dB/20) for the attenuated dB, which is
// min(0, -loudnessDb - headroom). The issue's own track, Bohemian Rhapsody,
// measures loudnessDb 2.29; an 8 dB equaliser boost reserves the same 8 dB.
var normalizeGainTests = []struct {
	name       string
	loudnessDb float64
	headroom   float64
	want       float64
}{
	{"loud track, flat eq", 2.29, 0, math.Pow(10, -2.29/20)},
	{"loud track under an 8 dB boost", 2.29, 8, math.Pow(10, -10.29/20)},
	{"quiet track, flat eq does not boost", -5, 0, 1},
	{"quiet track, boost still makes room", -5, 8, math.Pow(10, -3.0/20)},
	{"silence measured at the reference", 0, 0, 1},
	{"boost larger than the loudness margin", 1, 4, math.Pow(10, -5.0/20)},
}

func TestNormalizeGain(t *testing.T) {
	for _, test := range normalizeGainTests {
		t.Run(test.name, func(t *testing.T) {
			gain := normalizeGain(test.loudnessDb, test.headroom)
			if math.Abs(gain-test.want) > 1e-9 {
				t.Errorf("normalizeGain(%v, %v) = %v, want %v", test.loudnessDb, test.headroom, gain, test.want)
			}
			if gain > 1 {
				t.Errorf("normalizeGain(%v, %v) = %v boosts, want attenuation only", test.loudnessDb, test.headroom, gain)
			}
		})
	}
}

func TestEqHeadroom(t *testing.T) {
	for _, test := range []struct {
		name  string
		bands []float64
		want  float64
	}{
		{"empty", nil, 0},
		{"flat", make([]float64, 10), 0},
		{"cuts are not headroom", []float64{-12, -6, 0, 0, 0, 0, 0, 0, 0, 0}, 0},
		{"the strongest boost", []float64{0, 0, 3, 0, 8, 0, 0, 0, 0, 2}, 8},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := eqHeadroom(test.bands); got != test.want {
				t.Errorf("eqHeadroom(%v) = %v, want %v", test.bands, got, test.want)
			}
		})
	}
}
