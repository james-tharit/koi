package main

// Time-of-day palette. The pond's water, caustics, sky-glow and lily-pad
// tones all come from one table indexed by `tod`. One palette is picked at
// startup (from the system clock, or forced with -time) and lives in
// `active` for the rest of the run.

import "math"

type tod int

const (
	todMorning tod = iota
	todAfternoon
	todLateAfternoon
	todEarlyNight
	todNight
	todLateNight
)

var todNames = [...]string{
	"morning", "afternoon", "late-afternoon",
	"early-night", "night", "late-night",
}

// pickTOD maps a 24-hour clock hour to a bucket:
//
//	05–11 morning · 12–14 afternoon · 15–17 late-afternoon
//	18–21 early-night · 22–23 night · 00–04 late-night
func pickTOD(h int) tod {
	switch {
	case h < 5:
		return todLateNight
	case h < 12:
		return todMorning
	case h < 15:
		return todAfternoon
	case h < 18:
		return todLateAfternoon
	case h < 22:
		return todEarlyNight
	}
	return todNight
}

func parseTOD(s string) (tod, bool) {
	for i, n := range todNames {
		if n == s {
			return tod(i), true
		}
	}
	return 0, false
}

type theme struct {
	water      [4]rgb  // the four Voronoi water tones
	causticLo  rgb     // soft caustic edge
	causticHi  rgb     // bright caustic edge
	causticAmp float64 // 0..1, scales the caustic mix weights
	skyGlowTop rgb     // color blended into the top rows
	skyGlowAmp float64 // 0..1, 0 disables the gradient
	padDark    rgb
	padMid     rgb
	padLight   rgb
	moon       bool // draw a moon reflection on the water
}

var palettes = [6]theme{
	// todMorning: pale, cool, soft pearl haze at the horizon
	{
		water:      [4]rgb{hex(0x6a92c0), hex(0x74a0cc), hex(0x82acd2), hex(0x5f8ab5)},
		causticLo:  hex(0x9bbede),
		causticHi:  hex(0xbdd4ea),
		causticAmp: 0.85,
		skyGlowTop: hex(0xd8c4c8),
		skyGlowAmp: 0.10,
		padDark:    hex(0x1b7a56),
		padMid:     hex(0x269668),
		padLight:   hex(0x3fb37d),
	},
	// todAfternoon: the original look; the baseline every other mode is relative to
	{
		water:      [4]rgb{hex(0x5577aa), hex(0x5f84b8), hex(0x6a90c4), hex(0x4f6fa0)},
		causticLo:  hex(0x7ea3d2),
		causticHi:  hex(0x9dbde3),
		causticAmp: 1.0,
		skyGlowAmp: 0.0,
		padDark:    hex(0x16704f),
		padMid:     hex(0x1f8a5e),
		padLight:   hex(0x35a571),
	},
	// todLateAfternoon: warmer water, orange horizon
	{
		water:      [4]rgb{hex(0x5878a8), hex(0x6584b0), hex(0x7490bc), hex(0x4e6c98)},
		causticLo:  hex(0x8ba9c8),
		causticHi:  hex(0xb8c8d8),
		causticAmp: 0.9,
		skyGlowTop: hex(0xe8a870),
		skyGlowAmp: 0.14,
		padDark:    hex(0x176a4a),
		padMid:     hex(0x1e8057),
		padLight:   hex(0x339a69),
	},
	// todEarlyNight: dusk, dusky-pink glow, caustics fading
	{
		water:      [4]rgb{hex(0x344878), hex(0x3c548c), hex(0x455f98), hex(0x2e4070)},
		causticLo:  hex(0x5a7098),
		causticHi:  hex(0x7084a8),
		causticAmp: 0.55,
		skyGlowTop: hex(0x9a5878),
		skyGlowAmp: 0.18,
		padDark:    hex(0x0e4a36),
		padMid:     hex(0x165c42),
		padLight:   hex(0x206e50),
	},
	// todNight: deep navy, moon, barely any caustics
	{
		water:      [4]rgb{hex(0x1a2440), hex(0x202c4a), hex(0x263454), hex(0x151e38)},
		causticLo:  hex(0x303c58),
		causticHi:  hex(0x455068),
		causticAmp: 0.25,
		skyGlowTop: hex(0x101424),
		skyGlowAmp: 0.10,
		padDark:    hex(0x0a2e22),
		padMid:     hex(0x0f3a2c),
		padLight:   hex(0x154838),
		moon:       true,
	},
	// todLateNight: darkest, cold, moon still up
	{
		water:      [4]rgb{hex(0x121c34), hex(0x18223c), hex(0x1c2644), hex(0x0f1830)},
		causticLo:  hex(0x263048),
		causticHi:  hex(0x384258),
		causticAmp: 0.20,
		skyGlowTop: hex(0x080c18),
		skyGlowAmp: 0.08,
		padDark:    hex(0x082a1e),
		padMid:     hex(0x0d3426),
		padLight:   hex(0x113e30),
		moon:       true,
	},
}

// active is the palette used for the current run. main() overwrites it from
// the clock or from -time before any drawing happens.
var active = palettes[todAfternoon]

// drawMoon paints a small whitish disc with a soft halo onto the water,
// roughly in the upper-right of the canvas. Position is seed-stable so it
// does not jitter between frames. Called after drawWater, before shadows.
func drawMoon(c *canvas, seed uint32) {
	cx := float64(c.w)*0.72 + float64(seed%17)*0.3
	cy := float64(c.h) * 0.18
	r := 2.6
	core := hex(0xeeead8)
	glow := hex(0xcfc6a4)
	for y := int(cy - r - 3); y <= int(cy+r+3); y++ {
		for x := int(cx - r - 3); x <= int(cx+r+3); x++ {
			if !c.in(x, y) {
				continue
			}
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			switch {
			case d < r:
				c.set(x, y, core)
			case d < r+1.4:
				c.blend(x, y, glow, 0.45)
			case d < r+3:
				c.blend(x, y, glow, 0.14)
			}
		}
	}
}
