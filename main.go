// koi: a procedural pixel-art koi pond for your terminal greeting.
//
// The pond is a small pixel canvas. Each terminal cell shows two pixels
// using the upper-half-block glyph ▀ (foreground = top pixel, background =
// bottom pixel), so pixels come out square.
//
// Every frame is computed from scratch:
//
//   - water:  Voronoi cells give faceted patches of blue with bright caustic
//     edges that slowly drift
//
//   - koi:    a spine that follows a figure-eight path; the body is every
//     pixel within a radius profile of that spine (a distance field)
//
//   - fins:   translucent capsules blended over the water
//
//   - pads:   lily pads with a notch, veins and the odd flower, drawn on top
//
//     koi                 animate for 5s, then leave the last frame
//     koi -s 8            animate for 8 seconds (or $KOI_SECONDS)
//     koi -loop           animate until Ctrl-C
//     koi -frame 2        print one frame at t=2s
//     koi -png pond.png   save one frame as a PNG (scaled up 8x)
//     koi -gif pond.gif   save the animation (-s seconds) as a looping GIF (scaled up 4x)
//     koi -clear          erase the pond when the animation ends
//     koi -w 80 -h 18     size in columns and rows (default: $COLUMNS, 16 rows)
//     koi -256            force 256-colour output (auto when $COLORTERM lacks truecolor)
//     koi -seed 42        fixed layout and colours
package main

import (
	"bufio"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"image/png"
	"math"
	"math/rand"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ------------------------------------------------------------ colour

type rgb struct{ r, g, b float64 }

func hex(h uint32) rgb {
	return rgb{float64(h >> 16 & 255), float64(h >> 8 & 255), float64(h & 255)}
}

func mix(a, b rgb, t float64) rgb {
	return rgb{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t}
}

func (c rgb) mul(f float64) rgb { return rgb{c.r * f, c.g * f, c.b * f} }

var (
	waterTones = []rgb{hex(0x5577aa), hex(0x5f84b8), hex(0x6a90c4), hex(0x4f6fa0)}
	causticLo  = hex(0x7ea3d2)
	causticHi  = hex(0x9dbde3)
	padDark    = hex(0x16704f)
	padMid     = hex(0x1f8a5e)
	padLight   = hex(0x35a571)
	petal      = hex(0xf2f0d8)
	pollen     = hex(0xe8d86a)
	eyeCol     = hex(0x14161c)
)

type variety struct {
	name             string
	base, patch, lit rgb
	thresh           float64 // higher = fewer patches
	crown            bool    // tancho: one red spot on the head
}

var varieties = []variety{
	{"kohaku", hex(0xf1ece6), hex(0xe5483c), hex(0xffffff), 0.2, false},
	{"orenji", hex(0xef8a2c), hex(0xf6f2ea), hex(0xf9c95a), 0.75, false},
	{"tancho", hex(0xf1ece6), hex(0xd8332f), hex(0xffffff), 0, true},
	{"showa", hex(0x2c2c33), hex(0xe2502e), hex(0x55555f), 0.05, false},
	{"yamabuki", hex(0xf2c440), hex(0xf8ecb0), hex(0xfff2a0), 0.9, false},
}

// ------------------------------------------------------------ canvas

type canvas struct {
	w, h int
	px   []rgb
}

func newCanvas(w, h int) *canvas { return &canvas{w, h, make([]rgb, w*h)} }

func (c *canvas) in(x, y int) bool    { return x >= 0 && y >= 0 && x < c.w && y < c.h }
func (c *canvas) at(x, y int) rgb     { return c.px[y*c.w+x] }
func (c *canvas) set(x, y int, v rgb) { c.px[y*c.w+x] = v }
func (c *canvas) blend(x, y int, v rgb, a float64) {
	if c.in(x, y) {
		c.set(x, y, mix(c.at(x, y), v, a))
	}
}

// ------------------------------------------------------------ water

func hash2(x, y int, s uint32) float64 {
	h := uint32(x)*374761393 + uint32(y)*668265263 + s*2246822519
	h = (h ^ h>>13) * 1274126177
	return float64(h^h>>16) / 4294967295.0
}

func drawWater(c *canvas, t float64, seed uint32) {
	const cell = 9.0
	for y := 0; y < c.h; y++ {
		for x := 0; x < c.w; x++ {
			fx, fy := (float64(x)+0.5)/cell, (float64(y)+0.5)/cell
			ix, iy := int(math.Floor(fx)), int(math.Floor(fy))
			f1, f2, id := 9.0, 9.0, 0.0
			for oy := -1; oy <= 1; oy++ {
				for ox := -1; ox <= 1; ox++ {
					cx, cy := ix+ox, iy+oy
					h1, h2, h3 := hash2(cx, cy, seed), hash2(cx, cy, seed+1), hash2(cx, cy, seed+2)
					// feature points wander slowly
					px := float64(cx) + 0.5 + 0.38*math.Sin(t*0.7+h1*6.28) + (h2-0.5)*0.3
					py := float64(cy) + 0.5 + 0.38*math.Cos(t*0.6+h2*6.28) + (h3-0.5)*0.3
					d := math.Hypot(fx-px, fy-py)
					if d < f1 {
						f2, f1, id = f1, d, h3
					} else if d < f2 {
						f2 = d
					}
				}
			}
			col := waterTones[int(id*float64(len(waterTones)))%len(waterTones)]
			edge := (f2 - f1) * cell
			switch {
			case edge < 0.5:
				col = mix(col, causticHi, 0.75)
			case edge < 1.1:
				col = mix(col, causticLo, 0.4)
			}
			c.set(x, y, col)
		}
	}
}

// ------------------------------------------------------------ lily pads

type pad struct {
	x, y, r float64
	notch   float64
	flower  bool
}

func makePads(w, h int, r *rand.Rand) []pad {
	var pads []pad
	want := w * h / 420
	for tries := 0; len(pads) < want && tries < 400; tries++ {
		p := pad{
			x:      r.Float64() * float64(w),
			y:      r.Float64() * float64(h),
			r:      2.5 + r.Float64()*r.Float64()*6,
			notch:  r.Float64() * 6.28,
			flower: r.Float64() < 0.3,
		}
		ok := true
		for _, q := range pads {
			if math.Hypot(p.x-q.x, p.y-q.y) < p.r+q.r+2 {
				ok = false
				break
			}
		}
		// keep the middle of the pond open so the koi stay visible
		if math.Abs(p.x-float64(w)/2) < float64(w)*0.18 && math.Abs(p.y-float64(h)/2) < float64(h)*0.25 {
			ok = false
		}
		if ok {
			pads = append(pads, p)
		}
	}
	return pads
}

func angDiff(a, b float64) float64 {
	d := math.Mod(a-b+3*math.Pi, 2*math.Pi) - math.Pi
	return math.Abs(d)
}

func drawPad(c *canvas, p pad, t float64) {
	bob := 0.25 * math.Sin(t*0.8+p.notch) // tiny drift
	for y := int(p.y - p.r - 1); y <= int(p.y+p.r+1); y++ {
		for x := int(p.x - p.r - 1); x <= int(p.x+p.r+1); x++ {
			if !c.in(x, y) {
				continue
			}
			dx, dy := float64(x)+0.5-p.x-bob, float64(y)+0.5-p.y
			d := math.Hypot(dx, dy)
			if d > p.r {
				continue
			}
			a := math.Atan2(dy, dx)
			if angDiff(a, p.notch) < 0.32 && d > 0.6 {
				continue // the wedge cut every lily pad has
			}
			col := padMid
			light := (-dx - dy) / p.r
			switch {
			case d > p.r-1 && light < 0.2:
				col = padDark
			case light > 0.55:
				col = padLight
			}
			if p.r >= 5 && d > 1.5 && d < p.r-1.2 { // veins
				for k := 1; k < 6; k++ {
					if angDiff(a, p.notch+float64(k)*1.05) < 0.35/d {
						col = padDark
					}
				}
			}
			c.set(x, y, col)
		}
	}
	if p.flower {
		fx, fy := int(p.x-p.r*0.35), int(p.y-p.r*0.35)
		for _, o := range [][2]int{{0, -1}, {-1, 0}, {1, 0}, {0, 1}} {
			if c.in(fx+o[0], fy+o[1]) {
				c.set(fx+o[0], fy+o[1], petal)
			}
		}
		if c.in(fx, fy) {
			c.set(fx, fy, pollen)
		}
	}
}

// ------------------------------------------------------------ koi

type pt struct{ x, y float64 }

type koi struct {
	v          variety
	n          int
	seg, R     float64
	s1, s2, s3 float64

	// steering state (advanced by scene.step)
	x, y, heading float64
	baseSpeed     float64
	speed         float64
	wf1, wf2      float64 // wander frequencies, different for each fish
	beat          float64 // tail-beat phase; runs faster when swimming faster
	tx, ty        float64 // where this koi is heading next
	trail         []pt    // recent head positions, newest last
	cur           []pt    // spine for the current frame
}

// radius: half-width at u (0 = nose, 1 = tail tip)
func (k *koi) radius(u float64) float64 {
	R := k.R
	switch {
	case u < 0.13:
		return R * (0.5 + 0.5*math.Sin(u/0.13*math.Pi/2))
	case u < 0.4:
		return R
	case u < 0.74:
		return R * (1 - 0.82*(u-0.4)/0.34)
	default: // flowing tail fin
		return R * (0.2 + 0.7*math.Pow((u-0.74)/0.26, 0.7))
	}
}

// spine walks back along the head's trail, one segment length at a time,
// then adds a side-to-side swimming wave.
func (k *koi) spine() []pt {
	pts := make([]pt, 0, k.n+1)
	last := len(k.trail) - 1
	prev := k.trail[last]
	pts = append(pts, prev)
	acc := 0.0
	for i := last - 1; i >= 0 && len(pts) <= k.n; i-- {
		p := k.trail[i]
		acc += math.Hypot(p.x-prev.x, p.y-prev.y)
		prev = p
		if acc >= k.seg {
			pts = append(pts, p)
			acc = 0
		}
	}
	bx, by := -math.Cos(k.heading), -math.Sin(k.heading)
	for len(pts) <= k.n { // trail too short: continue straight back
		p := pts[len(pts)-1]
		pts = append(pts, pt{p.x + bx*k.seg, p.y + by*k.seg})
	}
	out := make([]pt, len(pts))
	for i := range pts {
		a, b := pts[max(0, i-1)], pts[min(len(pts)-1, i+1)]
		tx, ty := b.x-a.x, b.y-a.y
		L := math.Hypot(tx, ty) + 1e-9
		u := float64(i) / float64(k.n)
		w := (0.1 + 1.1*u*u) * math.Sin(k.beat-float64(i)*0.6) * k.R / 2.8
		out[i] = pt{pts[i].x - ty/L*w, pts[i].y + tx/L*w}
	}
	return out
}

// sdf returns signed distance to the body, position along it (u) and
// signed sideways offset (v).
func (k *koi) sdf(pts []pt, x, y float64) (float64, float64, float64) {
	best, bu, bv := 1e9, 0.0, 0.0
	for i := 0; i < k.n; i++ {
		ax, ay := pts[i].x, pts[i].y
		dx, dy := pts[i+1].x-ax, pts[i+1].y-ay
		L2 := dx*dx + dy*dy + 1e-9
		s := math.Max(0, math.Min(1, ((x-ax)*dx+(y-ay)*dy)/L2))
		dist := math.Hypot(x-ax-dx*s, y-ay-dy*s)
		u := (float64(i) + s) / float64(k.n)
		if d := dist - k.radius(u); d < best {
			best, bu = d, u
			bv = math.Copysign(dist, dx*(y-ay)-dy*(x-ax))
		}
	}
	return best, bu, bv
}

func (k *koi) patterned(u, vn float64) bool {
	if k.v.crown {
		return (u-0.1)*(u-0.1)*90+vn*vn*0.8 < 0.5
	}
	n := math.Sin(u*12+k.s1) + 0.8*math.Sin(u*5.3+vn*2.2+k.s2) + 0.5*math.Sin(vn*3.1-u*4+k.s3)
	return n > k.v.thresh+0.4
}

func bbox(pts []pt, pad float64, c *canvas) (int, int, int, int) {
	x0, y0, x1, y1 := 1e9, 1e9, -1e9, -1e9
	for _, p := range pts {
		x0, x1 = math.Min(x0, p.x), math.Max(x1, p.x)
		y0, y1 = math.Min(y0, p.y), math.Max(y1, p.y)
	}
	return max(0, int(x0-pad)), max(0, int(y0-pad)), min(c.w-1, int(x1+pad)), min(c.h-1, int(y1+pad))
}

func (k *koi) drawShadow(c *canvas) {
	pts := k.cur
	const ox, oy = 2.0, 3.0 // sun from the upper left
	x0, y0, x1, y1 := bbox(pts, k.R*2+4, c)
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			if d, u, _ := k.sdf(pts, float64(x)+0.5-ox, float64(y)+0.5-oy); d < 0 && u < 0.8 {
				c.set(x, y, c.at(x, y).mul(0.8))
			}
		}
	}
}

func (k *koi) draw(c *canvas, t float64) {
	pts := k.cur
	head, neck := pts[0], pts[2]
	fx, fy := head.x-neck.x, head.y-neck.y
	fl := math.Hypot(fx, fy) + 1e-9
	fx, fy = fx/fl, fy/fl // forward
	finCol := mix(k.v.base, hex(0xffffff), 0.55)

	// pectoral and pelvic fins: translucent, swept back, flapping
	type fin struct{ px, py, qx, qy, w, a float64 }
	var fins []fin
	for _, spec := range []struct{ u, len, w, a float64 }{{0.22, 2.6, 1.5, 0.55}, {0.5, 1.6, 0.9, 0.45}} {
		i := int(spec.u * float64(k.n))
		a, b := pts[i], pts[i+1]
		tx, ty := a.x-b.x, a.y-b.y
		L := math.Hypot(tx, ty) + 1e-9
		tx, ty = tx/L, ty/L
		nx, ny := -ty, tx
		flap := 1 + 0.45*math.Sin(t*5+k.s3+spec.u*4)
		for _, side := range []float64{1, -1} {
			r := k.radius(spec.u) * 0.8
			px, py := a.x+nx*side*r, a.y+ny*side*r
			l := spec.len * k.R / 2.8
			qx := px - tx*l*1.2 + nx*side*l*flap
			qy := py - ty*l*1.2 + ny*side*l*flap
			fins = append(fins, fin{px, py, qx, qy, spec.w * k.R / 2.8, spec.a})
		}
	}

	x0, y0, x1, y1 := bbox(pts, k.R*2+4, c)
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			wx, wy := float64(x)+0.5, float64(y)+0.5
			for _, f := range fins {
				dx, dy := f.qx-f.px, f.qy-f.py
				s := math.Max(0, math.Min(1, ((wx-f.px)*dx+(wy-f.py)*dy)/(dx*dx+dy*dy+1e-9)))
				if math.Hypot(wx-f.px-dx*s, wy-f.py-dy*s) < f.w*(1-0.55*s)+0.2 {
					c.blend(x, y, finCol, f.a)
					break
				}
			}
			d, u, v := k.sdf(pts, wx, wy)
			if d >= 0 {
				continue
			}
			r := k.radius(u)
			vn := v / r
			if u > 0.74 { // tail fin: translucent, forked, streaked
				a := 0.5
				if int(math.Abs(vn)*3+u*9)%2 == 0 {
					a = 0.65
				}
				c.blend(x, y, mix(finCol, k.v.base, 0.35), a)
				continue
			}
			col := k.v.base
			if k.patterned(u, vn) {
				col = k.v.patch
			}
			switch an := math.Abs(vn); {
			case an > 0.72:
				col = col.mul(0.8) // rounded flank in shade
			case vn > -0.05 && vn < 0.38 && u > 0.12:
				col = mix(col, k.v.lit, 0.45) // light catching the back
			}
			if u < 0.13 {
				col = col.mul(0.93)
			}
			c.set(x, y, col)
		}
	}

	nx, ny := -fy, fx
	// eyes on both sides of the head
	er := k.radius(0.08) * 0.75
	for _, side := range []float64{1, -1} {
		ex := head.x - fx*k.R*0.55 + nx*side*er
		ey := head.y - fy*k.R*0.55 + ny*side*er
		if c.in(int(ex), int(ey)) {
			c.set(int(ex), int(ey), eyeCol)
		}
	}
	// barbels: thin whiskers that sway
	whisk := k.v.base.mul(0.7)
	for _, side := range []float64{1, -1} {
		sway := 0.6 * math.Sin(t*3+side+k.s1)
		for s := 0.4; s <= 2.4; s += 0.4 {
			bx := head.x + fx*(0.5+s*0.6) + nx*side*(0.6+s*0.7+sway*s*0.3)
			by := head.y + fy*(0.5+s*0.6) + ny*side*(0.6+s*0.7+sway*s*0.3)
			c.blend(int(bx), int(by), whisk, 0.75)
		}
	}
}

// ------------------------------------------------------------ scene

type scene struct {
	c    *canvas
	fish []*koi
	pads []pad
	seed uint32
	simT float64
	sc   float64
	w, h float64
	rng  *rand.Rand
}

const simDT = 1.0 / 60

func newScene(w, h int, r *rand.Rand) *scene {
	sc := math.Min(float64(h)/32, float64(w)/60) // fish size relative to the pond
	s := &scene{c: newCanvas(w, h), sc: sc, w: float64(w), h: float64(h), rng: rand.New(rand.NewSource(r.Int63()))}
	perm := r.Perm(len(varieties))
	for i := 0; i < 2; i++ {
		side := float64(i*2 - 1) // start on opposite sides, facing opposite ways
		k := &koi{
			v: varieties[perm[i]], n: 18, seg: 1.45 * sc, R: 3.1 * sc,
			s1: r.Float64() * 6.28, s2: r.Float64() * 6.28, s3: r.Float64() * 6.28,
			x:         s.w/2 + side*s.w*0.28,
			y:         s.h * (0.35 + 0.3*r.Float64()),
			heading:   math.Pi/2*(1+side) + (r.Float64()-0.5)*0.8,
			baseSpeed: (7 + 2.5*r.Float64() + 1.5*float64(i)) * sc,
			wf1:       0.35 + 0.3*r.Float64(),
			wf2:       0.9 + 0.5*r.Float64(),
		}
		k.trail = []pt{{k.x, k.y}}
		s.fish = append(s.fish, k)
	}
	for i := range s.fish {
		s.pickTarget(i)
	}
	s.pads = makePads(w, h, r)
	s.seed = r.Uint32()
	// let them swim for a few seconds first so frame 0 already looks natural
	s.simT = -4
	s.advance(0)
	return s
}

func wrapAngle(a float64) float64 {
	return math.Mod(a+3*math.Pi, 2*math.Pi) - math.Pi
}

// pickTarget chooses a new destination for koi i: somewhere it has to
// travel to, and as far as possible from the other koi and its destination.
func (s *scene) pickTarget(i int) {
	k := s.fish[i]
	mx, my := k.R*3.5, k.R*2.2
	best, bx, by := -1e9, s.w/2, s.h/2
	for c := 0; c < 12; c++ {
		x := mx + s.rng.Float64()*math.Max(1, s.w-2*mx)
		y := my + s.rng.Float64()*math.Max(1, s.h-2*my)
		travel := math.Hypot(x-k.x, y-k.y)
		score := math.Min(travel, s.w*0.5) // go somewhere, not on the spot
		for j, o := range s.fish {
			if j != i {
				score += 1.5 * math.Min(math.Hypot(x-o.x, y-o.y), math.Hypot(x-o.tx, y-o.ty))
			}
		}
		if score > best {
			best, bx, by = score, x, y
		}
	}
	k.tx, k.ty = bx, by
}

// step moves every koi forward by dt: wander, steer away from the pond
// edges, and steer away from the other koi's bodies.
func (s *scene) step(dt float64) {
	T := s.simT
	for i, k := range s.fish {
		dx, dy := math.Cos(k.heading), math.Sin(k.heading)
		look := 9 * s.sc
		lx, ly := k.x+dx*look, k.y+dy*look

		// walls: push back from whichever edge the look-ahead point nears
		mx, my := k.R*3, k.R*2
		var wx, wy float64
		for _, p := range []pt{{lx, ly}, {k.x, k.y}} {
			if p.x < mx {
				wx += (mx - p.x) / mx
			}
			if p.x > s.w-mx {
				wx -= (p.x - (s.w - mx)) / mx
			}
			if p.y < my {
				wy += (my - p.y) / my
			}
			if p.y > s.h-my {
				wy -= (p.y - (s.h - my)) / my
			}
		}

		// other koi: steer away from any part of their body that is close
		var ax, ay, crowd float64
		D := k.R * 4
		for j, o := range s.fish {
			if j == i || o.cur == nil {
				continue
			}
			// their body, plus where their head will be shortly
			olook := pt{o.x + math.Cos(o.heading)*look, o.y + math.Sin(o.heading)*look}
			for n := 0; n <= len(o.cur); n += 2 {
				b := olook
				if n < len(o.cur) {
					b = o.cur[n]
				}
				for _, p := range []pt{{lx, ly}, {k.x, k.y}} {
					ex, ey := p.x-b.x, p.y-b.y
					d := math.Hypot(ex, ey) + 1e-6
					if d < D {
						f := (D - d) / D
						ax += ex / d * f
						ay += ey / d * f
						crowd = math.Max(crowd, f)
					}
				}
			}
		}

		// head for this koi's own destination; pick a new one on arrival
		gx, gy := k.tx-k.x, k.ty-k.y
		gd := math.Hypot(gx, gy) + 1e-6
		if gd < k.R*2.5 {
			s.pickTarget(i)
			gx, gy = k.tx-k.x, k.ty-k.y
			gd = math.Hypot(gx, gy) + 1e-6
		}

		tx := gx/gd + wx*3 + ax*3.5
		ty := gy/gd + wy*3 + ay*3.5
		diff := wrapAngle(math.Atan2(ty, tx) - k.heading)
		wander := 0.35 * (math.Sin(T*k.wf1+k.s1) + 0.6*math.Sin(T*k.wf2+k.s2))
		turn := wander + math.Max(-2.5, math.Min(2.5, diff*2.5))
		turn = math.Max(-2.5, math.Min(2.5, turn))
		k.heading = wrapAngle(k.heading + turn*dt)

		// glide and burst, slow into turns, hurry away when crowded
		// the second koi gives way when they meet; the first swims on
		yield := 1 + 0.4*crowd
		if i > 0 {
			yield = 1 - 0.6*crowd
		}
		k.speed = k.baseSpeed * (1 + 0.25*math.Sin(T*0.5+k.s3)) * (1 - 0.3*math.Abs(turn)/2.5) * yield
		k.x += math.Cos(k.heading) * k.speed * dt
		k.y += math.Sin(k.heading) * k.speed * dt
		k.beat += dt * (3 + k.speed*0.35/s.sc)

		k.trail = append(k.trail, pt{k.x, k.y})
		if len(k.trail) > 600 {
			k.trail = k.trail[len(k.trail)-400:]
		}
	}
	for _, k := range s.fish {
		k.cur = k.spine()
	}
	s.simT += dt
}

// advance runs the simulation up to time t.
func (s *scene) advance(t float64) {
	for s.simT+simDT <= t {
		s.step(simDT)
	}
}

func (s *scene) render(t float64) *canvas {
	s.advance(t)
	drawWater(s.c, t, s.seed)
	for _, f := range s.fish {
		f.drawShadow(s.c)
	}
	for _, f := range s.fish {
		f.draw(s.c, t)
	}
	for _, p := range s.pads {
		drawPad(s.c, p, t) // pads float on the surface, above the fish
	}
	return s.c
}

// ------------------------------------------------------------ output

func clamp8(v float64) int { return max(0, min(255, int(v+0.5))) }

func xterm256(c rgb) int {
	q := func(v float64) int { return clamp8(v) * 5 / 255 }
	return 16 + 36*q(c.r) + 6*q(c.g) + q(c.b)
}

func colorCode(c rgb, bg, truecolor bool) string {
	layer := 38
	if bg {
		layer = 48
	}
	if truecolor {
		return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", layer, clamp8(c.r), clamp8(c.g), clamp8(c.b))
	}
	return fmt.Sprintf("\x1b[%d;5;%dm", layer, xterm256(c))
}

// toLines packs two pixel rows into each line of ▀ glyphs.
func toLines(c *canvas, truecolor bool) []string {
	lines := make([]string, c.h/2)
	var sb strings.Builder
	for row := range lines {
		sb.Reset()
		lastF, lastB := "", ""
		for x := 0; x < c.w; x++ {
			f := colorCode(c.at(x, row*2), false, truecolor)
			b := colorCode(c.at(x, row*2+1), true, truecolor)
			if f != lastF {
				sb.WriteString(f)
				lastF = f
			}
			if b != lastB {
				sb.WriteString(b)
				lastB = b
			}
			sb.WriteString("▀")
		}
		sb.WriteString("\x1b[0m")
		lines[row] = sb.String()
	}
	return lines
}

func toRGBA(c *canvas, scale int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, c.w*scale, c.h*scale))
	for y := 0; y < c.h*scale; y++ {
		for x := 0; x < c.w*scale; x++ {
			p := c.at(x/scale, y/scale)
			img.Set(x, y, color.RGBA{uint8(clamp8(p.r)), uint8(clamp8(p.g)), uint8(clamp8(p.b)), 255})
		}
	}
	return img
}

func savePNG(c *canvas, path string, scale int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, toRGBA(c, scale))
}

// saveGIF writes secs seconds of animation at 25fps, looping forever.
// ponytail: fixed Plan9 palette + dithering, so water is a bit grainy.
func saveGIF(sc *scene, path string, secs float64, scale int) error {
	g := &gif.GIF{}
	for i := 0; float64(i) < secs*25; i++ {
		src := toRGBA(sc.render(float64(i)/25), scale)
		dst := image.NewPaletted(src.Bounds(), palette.Plan9)
		draw.FloydSteinberg.Draw(dst, src.Bounds(), src, image.Point{})
		g.Image = append(g.Image, dst)
		g.Delay = append(g.Delay, 4)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return gif.EncodeAll(f, g)
}

// ------------------------------------------------------------ main

func isTTY(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func flagSet(name string) bool {
	set := false
	flag.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

func main() {
	secs := flag.Float64("s", 5, "seconds to animate (or $KOI_SECONDS)")
	loop := flag.Bool("loop", false, "animate until Ctrl-C")
	frame := flag.Float64("frame", -1, "print one frame at this time and exit")
	pngOut := flag.String("png", "", "save one frame (at -frame, default 2s) as a PNG and exit")
	gifOut := flag.String("gif", "", "save the animation (-s seconds) as a GIF and exit")
	clear := flag.Bool("clear", false, "erase the pond when done")
	width := flag.Int("w", 0, "width in columns (default $COLUMNS, max 80)")
	rows := flag.Int("h", 16, "height in terminal rows")
	force256 := flag.Bool("256", false, "use 256 colours instead of truecolor")
	seed := flag.Int64("seed", 0, "fixed random seed")
	flag.Parse()

	if env := os.Getenv("KOI_SECONDS"); env != "" && !flagSet("s") {
		if v, err := strconv.ParseFloat(env, 64); err == nil {
			*secs = v
		}
	}
	ct := os.Getenv("COLORTERM")
	truecolor := !*force256 && (ct == "truecolor" || ct == "24bit")

	w := *width
	if w == 0 {
		w, _ = strconv.Atoi(os.Getenv("COLUMNS"))
		w = min(w-1, 80)
	}
	w = max(30, w)
	h := max(8, *rows)

	sd := *seed
	if sd == 0 {
		sd = time.Now().UnixNano()
	}
	sc := newScene(w, h*2, rand.New(rand.NewSource(sd)))

	if *gifOut != "" {
		if err := saveGIF(sc, *gifOut, *secs, 4); err != nil {
			fmt.Fprintln(os.Stderr, "koi:", err)
			os.Exit(1)
		}
		return
	}
	if *pngOut != "" {
		t := *frame
		if t < 0 {
			t = 2
		}
		if err := savePNG(sc.render(t), *pngOut, 8); err != nil {
			fmt.Fprintln(os.Stderr, "koi:", err)
			os.Exit(1)
		}
		return
	}
	if *frame >= 0 {
		fmt.Println(strings.Join(toLines(sc.render(*frame), truecolor), "\n"))
		return
	}
	if !isTTY(os.Stdout) || os.Getenv("NO_COLOR") != "" {
		return // never animate into a pipe or a colourless terminal
	}

	out := bufio.NewWriterSize(os.Stdout, 1<<18)
	restore := func() {
		out.WriteString("\x1b[0m\x1b[?25h")
		out.Flush()
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; restore(); os.Exit(0) }()

	out.WriteString("\x1b[?25l" + strings.Repeat("\n", h)) // hide cursor, reserve rows
	t0 := time.Now()
	tick := time.NewTicker(time.Second / 24)
	defer tick.Stop()
	for range tick.C {
		t := time.Since(t0).Seconds()
		if !*loop && t > *secs {
			break
		}
		fmt.Fprintf(out, "\x1b[%dF", h)
		for _, l := range toLines(sc.render(t), truecolor) {
			out.WriteString(l + "\n")
		}
		out.Flush()
	}
	if *clear {
		fmt.Fprintf(out, "\x1b[%dF%s\x1b[%dF", h, strings.Repeat("\x1b[2K\n", h), h)
	}
	restore()
}