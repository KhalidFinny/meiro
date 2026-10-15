package main

import (
	"context"
	"log"
	"math"
	"time"

	"github.com/elianiva/meiro/player"
)

// eqTimeout bounds one loudness request.
const eqTimeout = 8 * time.Second

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

// loudnessResult carries one track's loudness off the request that read it.
type loudnessResult struct {
	videoID string
	db      float64
	ok      bool
}

// normalizeGain returns the fixed gain for a track YouTube measures at
// loudnessDb above its -14 LUFS reference, reserving headroom for the
// equaliser's strongest boost so the shaped signal cannot clip. It only ever
// attenuates: a quiet track with a flat EQ plays at its own level.
func normalizeGain(loudnessDb, headroom float64) float64 {
	db := min(0, -loudnessDb-headroom)
	return math.Pow(10, db/20)
}

// eqHeadroom is the gain the equaliser's strongest boost adds, which the
// normalization leaves room for. Bands the equaliser does not touch add
// nothing.
func eqHeadroom(bands []float64) float64 {
	headroom := 0.0
	for _, gain := range bands {
		headroom = max(headroom, gain)
	}
	return headroom
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
// decode at the current position when a track plays, and re-derives the
// normalization gain, whose headroom follows the strongest boost.
func (a *app) applyEQ() {
	a.player.SetEQ(a.eqBands())
	a.applyNormalization()
	a.saveSettings()
}

// applyNormalization sets the player's gain for the track playing: the
// loudness YouTube reported, less the equaliser's headroom, and none of it
// without the setting or the value. A track whose loudness is not known yet
// gets its value from a request that comes back on the main thread.
func (a *app) applyNormalization() {
	if !a.settings.Normalize {
		a.player.SetGain(1)
		return
	}
	if db, ok := a.loudness.Peek(a.current.VideoID); ok {
		a.player.SetGain(normalizeGain(db, eqHeadroom(a.settings.EQBands)))
		return
	}
	a.player.SetGain(1)
	if !a.signedIn() || a.current.VideoID == "" || a.stream.busy() {
		return
	}
	client, videoID := a.client(), a.current.VideoID
	spawn(a, func() loudnessResult {
		ctx, cancel := context.WithTimeout(context.Background(), eqTimeout)
		defer cancel()
		db, ok, err := client.Loudness(ctx, videoID)
		if err != nil {
			log.Printf("playback: could not read loudness video_id=%s: %v", videoID, err)
		}
		return loudnessResult{videoID: videoID, db: db, ok: ok}
	}, func(result loudnessResult) {
		if !result.ok {
			return
		}
		a.loudness.Put(result.videoID, result.db)
		if a.current.VideoID != result.videoID {
			return // another track took over while this was on its way
		}
		a.player.SetGain(normalizeGain(result.db, eqHeadroom(a.settings.EQBands)))
	})
}
