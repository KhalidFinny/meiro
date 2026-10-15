package main

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestTheSoundCardSwitchAndPreset drives the equaliser the way a user does:
// the switch turns it on, and a preset writes its bands and sends them to
// the player.
func TestTheSoundCardSwitchAndPreset(t *testing.T) {
	a := newTestApp()
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 4000)
	tt.Frame()

	// The preset sits under the switch, and disabled presets ignore clicks,
	// so the switch goes first.
	if err := tt.Click("Equaliser on"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.settings.EQOn {
		t.Fatal("the equaliser switch did not turn the equaliser on")
	}

	var rock []float64
	for _, preset := range eqPresets {
		if preset.name == "Rock" {
			rock = preset.bands
		}
	}
	if err := tt.Click("Rock"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !slices.Equal(a.settings.EQBands, rock) {
		t.Errorf("bands = %v, want the Rock preset %v", a.settings.EQBands, rock)
	}
	if !slices.Equal(a.eqApplied[:], rock) {
		t.Errorf("eqApplied = %v, want the Rock preset %v", a.eqApplied, rock)
	}

	// Switching the equaliser off sends nil, which the settings keep and the
	// player clears (covered in the player package too).
	if err := tt.Click("Equaliser on"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.settings.EQOn {
		t.Error("the equaliser switch did not turn the equaliser off")
	}
	if got := a.eqBands(); got != nil {
		t.Errorf("eqBands with the equaliser off = %v, want nil", got)
	}
}
