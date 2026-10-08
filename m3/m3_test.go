package m3

import (
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestAnimatedPaintIntervalCapsPlaybackAnimationRate(t *testing.T) {
	if animatedPaintInterval != 33*time.Millisecond {
		t.Errorf("animated paint interval = %v, want 33 ms (~30 fps)", animatedPaintInterval)
	}
}

// Every role a component draws on a surface must stay readable, whatever the
// seed, the style and the appearance.
func TestSchemesKeepTheirContrast(t *testing.T) {
	for hue := 0.0; hue < 360; hue += 20 {
		seed := FromHue(hue)
		for _, style := range Styles {
			for _, dark := range []bool{false, true} {
				s := NewScheme(NewPalettes(seed, style), dark)
				pairs := []struct {
					name    string
					fg, bg  ui.Color
					minimum float64
				}{
					{"onPrimary", s.OnPrimary, s.Primary, 4.5},
					{"onPrimaryContainer", s.OnPrimaryContainer, s.PrimaryContainer, 4.5},
					{"onSecondary", s.OnSecondary, s.Secondary, 4.5},
					{"onSecondaryContainer", s.OnSecondaryContainer, s.SecondaryContainer, 4.5},
					{"onTertiary", s.OnTertiary, s.Tertiary, 4.5},
					{"onTertiaryContainer", s.OnTertiaryContainer, s.TertiaryContainer, 4.5},
					{"onError", s.OnError, s.Error, 4.5},
					{"onSurface", s.OnSurface, s.Surface, 7},
					{"onSurface/high", s.OnSurface, s.SurfaceContainerHigh, 7},
					{"onSurfaceVariant", s.OnSurfaceVariant, s.SurfaceContainerHighest, 4.5},
					{"inverse", s.InverseOnSurface, s.InverseSurface, 7},
					{"primary on surface", s.Primary, s.Surface, 3},
				}
				for _, p := range pairs {
					if got := Contrast(p.fg, p.bg); got < p.minimum {
						t.Errorf("hue %.0f %v dark=%v: %s contrast %.2f < %.1f", hue, style, dark, p.name, got, p.minimum)
					}
				}
			}
		}
	}
}

func TestTonesRunFromBlackToWhite(t *testing.T) {
	p := Palette{Hue: 260, Chroma: 0.12}
	if c := p.Tone(0); c != ui.RGB(0, 0, 0) {
		t.Errorf("tone 0 = %v", c)
	}
	if c := p.Tone(100); c != ui.RGB(255, 255, 255) {
		t.Errorf("tone 100 = %v", c)
	}
	last := -1.0
	for tone := 0.0; tone <= 100; tone += 5 {
		l := luminance(p.Tone(tone))
		if l < last {
			t.Fatalf("tone %.0f is darker than the one before it", tone)
		}
		last = l
	}
	// A tone's luminance is what L* says it is.
	if l := luminance(p.Tone(50)); math.Abs(l-0.184) > 0.02 {
		t.Errorf("tone 50 has luminance %.3f, want about 0.184", l)
	}
}

func TestHuesSurviveTheGamutMapping(t *testing.T) {
	for hue := 0.0; hue < 360; hue += 30 {
		got, chroma := HueOf(Palette{Hue: hue, Chroma: 0.1}.Tone(55))
		diff := math.Abs(got - hue)
		if diff > 180 {
			diff = 360 - diff
		}
		if diff > 4 || chroma < 0.04 {
			t.Errorf("hue %.0f came back as %.1f at chroma %.3f", hue, got, chroma)
		}
	}
}

func TestMonochromeHasNoColour(t *testing.T) {
	s := NewScheme(NewPalettes(ui.Hex("#d81b78"), Monochrome), false)
	for _, c := range []ui.Color{s.Primary, s.Secondary, s.Tertiary, s.Surface, s.SurfaceContainer} {
		if math.Max(math.Max(float64(c.R), float64(c.G)), float64(c.B))-math.Min(math.Min(float64(c.R), float64(c.G)), float64(c.B)) > 3 {
			t.Errorf("monochrome has the colour %v", c)
		}
	}
}

func TestSpringsSettleAndOvershootAsTheyShould(t *testing.T) {
	for name, spring := range map[string]Spring{"spatial": SpatialDefault, "fast": SpatialFast, "effects": EffectsDefault} {
		ease := spring.Ease()
		if ease(0) != 0 || math.Abs(float64(ease(1))-1) > 1e-6 {
			t.Errorf("%s: ease(0) = %v, ease(1) = %v", name, ease(0), ease(1))
		}
		peak := float32(0)
		for i := 0; i <= 200; i++ {
			peak = max(peak, ease(float32(i)/200))
		}
		overshoots := peak > 1.001
		if want := spring.Damping < 1; overshoots != want {
			t.Errorf("%s: peak %.3f, overshoot %v, want %v", name, peak, overshoots, want)
		}
		if d := spring.Duration().Milliseconds(); d < 50 || d > 1500 {
			t.Errorf("%s: duration %dms", name, d)
		}
	}
}

func TestMixingSchemesBlendsEveryRole(t *testing.T) {
	light := NewScheme(NewPalettes(DefaultSeed, TonalSpot), false)
	dark := NewScheme(NewPalettes(DefaultSeed, TonalSpot), true)
	if got := light.Mix(dark, 0); got != light {
		t.Errorf("mixing at 0 changed the scheme")
	}
	if got := light.Mix(dark, 1); got != dark {
		t.Errorf("mixing at 1 did not reach the other scheme")
	}
	mid := light.Mix(dark, 0.5)
	if mid.Surface == light.Surface || mid.Surface == dark.Surface || mid.OnSurface == light.OnSurface {
		t.Errorf("mixing halfway left roles at their ends")
	}
}

func TestThemeModes(t *testing.T) {
	if New(Config{Mode: Light}, true).Dark {
		t.Errorf("Light mode followed a dark desktop")
	}
	if !New(Config{Mode: Dark}, false).Dark {
		t.Errorf("Dark mode followed a light desktop")
	}
	if !New(Config{}, true).Dark || New(Config{}, false).Dark {
		t.Errorf("System mode did not follow the desktop")
	}
}
