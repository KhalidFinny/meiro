package main

import (
	"context"
	"time"

	"github.com/elianiva/meiro/player"
)

// eqLabels names the equaliser's bands the way a music player does.
var eqLabels = [player.EQBandCount]string{"31", "62", "125", "250", "500", "1k", "2k", "4k", "8k", "16k"}

// eqBoostRange is how far a band may be raised or cut, in dB.
const eqBoostRange = 12.0

// eqPresets are the curves an audio player's equaliser usually offers, one
// gain per EQFrequencies entry.
var eqPresets = []struct {
	name  string
	bands []float64
}{
	{"Flat", []float64{0, 0, 0, 0, 0, 0, 0, 0, 0, 0}},
	{"Bass Boost", []float64{7.5, 7, 5.5, 3.5, 1, 0, 0, 0, 0, 0}},
	{"Treble Boost", []float64{0, 0, 0, 0, 0, 1, 2.5, 4, 5.5, 7}},
	{"Vocal", []float64{-2, -1.5, 0, 2.5, 4.5, 4.5, 3, 1.5, 0, -1}},
	{"Acoustic", []float64{4, 3.5, 2, 1, 1.5, 1.5, 2.5, 3, 3.5, 3}},
	{"Pop", []float64{-1.5, -1, 0, 2, 4, 4, 2.5, 0, -1, -1.5}},
	{"Rock", []float64{5.5, 4.5, 3.5, 1.5, -1.5, -1.5, 1.5, 4, 5, 5.5}},
	{"Jazz", []float64{4, 3, 1.5, 2, -1.5, -1.5, 0, 1.5, 3, 4}},
	{"Classical", []float64{4, 3, 2, 0, -1, -1, 0, 2, 3, 4}},
	{"Electronic", []float64{6, 5.5, 2, 0, -2, -1, 0, 2, 4, 5.5}},
}

// eqBands returns the equaliser's gains as the player takes them: exactly
// one per band, nil when the equaliser is off.
func (a *app) eqBands() []float64 {
	if !a.settings.EQOn || len(a.settings.EQBands) != player.EQBandCount {
		return nil
	}
	return a.settings.EQBands
}

// applyEQ sends the equaliser's bands to the player, which restarts the
// decode at the current position when a track plays.
func (a *app) applyEQ() {
	a.eqDebounce.stop()
	a.player.SetEQ(a.eqBands())
	a.saveSettings()
}

// scheduleEQ applies the equaliser's bands once the sliders have been quiet
// for a moment. A drag and a walk of the arrow keys then cost one decode
// restart instead of one per tick; a start replaces the waiting one, and a
// test, whose sleep returns at once, applies immediately.
func (a *app) scheduleEQ() {
	bands := a.eqBands()
	startTask(a, &a.eqDebounce, noTimeout,
		func(ctx context.Context) ([]float64, error) {
			if err := a.sleep(ctx, 250*time.Millisecond); err != nil {
				return nil, err
			}
			return bands, nil
		},
		func(bands []float64, err error) {
			if err != nil {
				return
			}
			a.player.SetEQ(bands)
			a.saveSettings()
		})
}
