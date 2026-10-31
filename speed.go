package main

import (
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/elianiva/meiro/m3"
)

// speedLabel names a playback speed as "1.5×".
func speedLabel(speed float64) string {
	return strconv.FormatFloat(speed, 'f', -1, 64) + "×"
}

// speedCard chooses how fast songs and podcast episodes play. The choice is a
// setting, so each track starts at the speed of its own kind.
func (a *app) speedCard(c *ui.Context) {
	a.settingsCard(c, "Playback speed", m3.IconSpeed, func() {
		for _, kind := range []struct {
			title   string
			speed   float64
			episode bool
		}{
			{"Songs", knownSpeed(a.settings.MusicSpeed), false},
			{"Podcast episodes", knownSpeed(a.settings.PodcastSpeed), true},
		} {
			ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
				settingLabel(c, kind.title, "").Grow(1)
				if chosen, picked := a.speedPicker(c, "speed-"+kind.title, kind.title+" speed: ", kind.speed); picked {
					a.setSpeedFor(kind.episode, chosen)
				}
			})
		}
	})
}

// speedPicker draws a button that shows a speed and the menu that picks
// another; key tells the buttons apart, and name is what the button is called
// beyond the speed it shows. It reports the speed picked, if one was.
func (a *app) speedPicker(c *ui.Context, key, name string, speed float64) (float64, bool) {
	sc := m3.Of(c).Scheme
	anchor := m3.Button(c, m3.ButtonSpec{Label: speedLabel(speed), Kind: m3.TextOnly, Size: m3.Small40, Key: key}).
		Label(name + speedLabel(speed))
	// A menu ignores the press that lands on the button that opened it, so
	// the button closes it, as the account menu's button does.
	if anchor.Clicked() {
		if a.speedMenu == key {
			a.speedMenu = ""
		} else {
			a.speedMenu = key
		}
	}
	open := a.speedMenu == key
	if !open {
		return 0, false
	}
	chosen, picked := 0.0, false
	m3.Menu(c, anchor, &open, 200, func() {
		for _, choice := range playbackSpeeds {
			label := speedLabel(choice)
			if choice == 1 {
				label = "Normal"
			}
			row := ui.ButtonBase(c.Key(key + "-" + label))
			row.Height(36).PaddingX(12).Gap(12).AlignItems(ui.Center).Radius(m3.Medium).Cursor(ui.CursorPointer).Label(label).
				Role(ui.RoleMenuItemRadio).Checked(choice == speed).
				Background(m3.StateFill(ui.Transparent, sc.OnSurface, row.Hovered(), row.Pressed(), row.FocusVisible()))
			row.Children(func() {
				if choice == speed {
					ui.Icon(c, m3.IconCheck).FontSize(22).TextColor(sc.Primary)
				} else {
					ui.Box(c).Size(22, 22)
				}
				m3.Text(c, m3.BodyLarge, label).Grow(1).SingleLine().TextColor(sc.OnSurface)
			})
			if row.Clicked() {
				open = false
				chosen, picked = choice, true
			}
		}
	})
	if !open {
		a.speedMenu = ""
	}
	return chosen, picked
}
