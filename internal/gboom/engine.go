package gboom

import "math"

// Distance fog factor: shade = 1 / (1 + dist * fog).
const fog = 0.16

// Sprite height in world units (walls are 1.0 tall).
const impWorldHeight = 0.72

// Camera half-FOV tangent (0.66 ≈ the classic 66° FOV).
const planeLen = 0.66

// Corner-vignette strength (0 = none); subtle, ~0.8 at the extreme corners.
const vignette = 0.11

// RGB framebuffer with reusable scratch buffers.
type FrameBuffer struct {
	W, H   int
	Pixels []byte // RGB8, row-major
	zbuf   []float32
	// Per-column wall strip bounds [top, bottom) in screen rows.
	wallTop     []int32
	wallBottom  []int32
	spriteOrder []spriteOrder
	vigX        []float32
	vigY        []float32
}

type spriteOrder struct {
	i  int
	d2 float32
}

func NewFrameBuffer() *FrameBuffer {
	return &FrameBuffer{}
}

func (fb *FrameBuffer) Resize(w, h int) {
	if fb.W == w && fb.H == h {
		return
	}
	fb.W = w
	fb.H = h
	fb.Pixels = make([]byte, w*h*3)
	fb.zbuf = make([]float32, w)
	for i := range fb.zbuf {
		fb.zbuf[i] = math.MaxFloat32
	}
	fb.wallTop = make([]int32, w)
	fb.wallBottom = make([]int32, w)
	fb.vigX = make([]float32, w)
	fb.vigY = make([]float32, h)
	axis := func(i, n int) float32 {
		t := float32(0)
		if n > 1 {
			t = 2*float32(i)/float32(n-1) - 1
		}
		return 1 - vignette*t*t
	}
	for x := 0; x < w; x++ {
		fb.vigX[x] = axis(x, w)
	}
	for y := 0; y < h; y++ {
		fb.vigY[y] = axis(y, h)
	}
}

func (fb *FrameBuffer) put(x, y int, c Rgb) {
	i := (y*fb.W + x) * 3
	fb.Pixels[i] = c[0]
	fb.Pixels[i+1] = c[1]
	fb.Pixels[i+2] = c[2]
}

func (fb *FrameBuffer) darken(x, y int, f float32) {
	i := (y*fb.W + x) * 3
	fb.Pixels[i] = byte(float32(fb.Pixels[i]) * f)
	fb.Pixels[i+1] = byte(float32(fb.Pixels[i+1]) * f)
	fb.Pixels[i+2] = byte(float32(fb.Pixels[i+2]) * f)
}

func (fb *FrameBuffer) applyVignette() {
	for y := 0; y < fb.H; y++ {
		vy := fb.vigY[y]
		row := fb.Pixels[y*fb.W*3 : (y+1)*fb.W*3]
		for x := 0; x < fb.W; x++ {
			f := fb.vigX[x] * vy
			px := row[x*3 : x*3+3]
			px[0] = byte(float32(px[0]) * f)
			px[1] = byte(float32(px[1]) * f)
			px[2] = byte(float32(px[2]) * f)
		}
	}
}

func shade(c Rgb, f float32) Rgb {
	cl := func(v float32) uint8 {
		if v > 255 {
			return 255
		}
		if v < 0 {
			return 0
		}
		return uint8(v)
	}
	return Rgb{cl(float32(c[0]) * f), cl(float32(c[1]) * f), cl(float32(c[2]) * f)}
}

func lerpColor(a, b Rgb, t float32) Rgb {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return Rgb{
		uint8(float32(a[0]) + (float32(b[0])-float32(a[0]))*t),
		uint8(float32(a[1]) + (float32(b[1])-float32(a[1]))*t),
		uint8(float32(a[2]) + (float32(b[2])-float32(a[2]))*t),
	}
}

// Renderer holds immutable render resources, built once per game.
type Renderer struct {
	textures []Texture
	floor    Texture
	ceiling  Texture
	imps     ImpSprites
	guns     GunSprites
}

func NewRenderer() *Renderer {
	return &Renderer{
		textures: buildTextures(),
		floor:    buildFloorTexture(),
		ceiling:  buildCeilingTexture(),
		imps:     buildImpSprites(),
		guns:     buildGunSprites(),
	}
}

// RenderGame draws one gameplay frame into fb.
func (r *Renderer) RenderGame(fb *FrameBuffer, game *Game) {
	w, h := fb.W, fb.H
	if w == 0 || h == 0 {
		return
	}
	lightBoost := float32(1)
	if game.Player.Muzzle > 0 {
		lightBoost = 1.35
	}
	r.drawWalls(fb, game, lightBoost)
	r.drawFloorCeiling(fb, game, lightBoost)
	r.drawImps(fb, game, lightBoost)
	fb.applyVignette()
	r.drawGun(fb, game)

	if game.Player.DamageFlash > 0 {
		t := game.Player.DamageFlash * 0.45
		if t > 0.45 {
			t = 0.45
		}
		for i := 0; i+2 < len(fb.Pixels); i += 3 {
			fb.Pixels[i] = byte(float32(fb.Pixels[i]) + (220-float32(fb.Pixels[i]))*t)
			fb.Pixels[i+1] = byte(float32(fb.Pixels[i+1]) * (1 - t*0.8))
			fb.Pixels[i+2] = byte(float32(fb.Pixels[i+2]) * (1 - t*0.8))
		}
	}
	if game.Player.HP <= 25 && !game.Dead() {
		pulse := 0.10 + 0.06*float32(math.Sin(float64(game.Time*5)))
		for i := 0; i+2 < len(fb.Pixels); i += 3 {
			fb.Pixels[i] = byte(float32(fb.Pixels[i]) + (160-float32(fb.Pixels[i]))*pulse)
		}
	}
}

func (r *Renderer) drawFloorCeiling(fb *FrameBuffer, game *Game, light float32) {
	w, h := fb.W, fb.H
	p := &game.Player
	dirX, dirY := p.Dir()
	planeX, planeY := -dirY*planeLen, dirX*planeLen
	ray0X, ray0Y := dirX-planeX, dirY-planeY
	ray1X, ray1Y := dirX+planeX, dirY+planeY
	camZ := 0.5 * float32(h)

	for y := h / 2; y < h; y++ {
		row := float32(y - h/2)
		if row < 1 {
			row = 1
		}
		rowDist := camZ / row
		fogShade := (1 / (1 + rowDist*fog)) * light
		stepX := rowDist * (ray1X - ray0X) / float32(w)
		stepY := rowDist * (ray1Y - ray0Y) / float32(w)
		worldX := p.X + rowDist*ray0X
		worldY := p.Y + rowDist*ray0Y
		ceilY := h - 1 - y
		yi, ceilYi := int32(y), int32(ceilY)
		for x := 0; x < w; x++ {
			wx, wy := worldX, worldY
			worldX += stepX
			worldY += stepY
			floorVis := yi >= fb.wallBottom[x]
			ceilVis := ceilYi < fb.wallTop[x]
			if !(floorVis || ceilVis) {
				continue
			}
			tx := int(remEuclid1(wx) * TexSize)
			ty := int(remEuclid1(wy) * TexSize)
			if floorVis {
				fb.put(x, y, shade(r.floor.Sample(tx, ty), fogShade))
			}
			if ceilVis {
				fb.put(x, ceilY, shade(r.ceiling.Sample(tx, ty), fogShade))
			}
		}
	}
}

func (r *Renderer) drawWalls(fb *FrameBuffer, game *Game, light float32) {
	w, h := fb.W, fb.H
	p := &game.Player
	dirX, dirY := p.Dir()
	planeX, planeY := -dirY*planeLen, dirX*planeLen

	for x := 0; x < w; x++ {
		cameraX := 2*float32(x)/float32(w) - 1
		rdX := dirX + planeX*cameraX
		rdY := dirY + planeY*cameraX
		mapX := int32(math.Floor(float64(p.X)))
		mapY := int32(math.Floor(float64(p.Y)))
		deltaX := float32(math.MaxFloat32)
		if rdX != 0 {
			deltaX = float32(math.Abs(1 / float64(rdX)))
		}
		deltaY := float32(math.MaxFloat32)
		if rdY != 0 {
			deltaY = float32(math.Abs(1 / float64(rdY)))
		}
		var stepX int32
		var sideX float32
		if rdX < 0 {
			stepX = -1
			sideX = (p.X - float32(mapX)) * deltaX
		} else {
			stepX = 1
			sideX = (float32(mapX) + 1 - p.X) * deltaX
		}
		var stepY int32
		var sideY float32
		if rdY < 0 {
			stepY = -1
			sideY = (p.Y - float32(mapY)) * deltaY
		} else {
			stepY = 1
			sideY = (float32(mapY) + 1 - p.Y) * deltaY
		}

		side := 0
		texID := uint8(1)
		for range 256 {
			if sideX < sideY {
				sideX += deltaX
				mapX += stepX
				side = 0
			} else {
				sideY += deltaY
				mapY += stepY
				side = 1
			}
			cell := game.Map.Cell(mapX, mapY)
			if cell != 0 {
				texID = cell
				break
			}
		}

		var perp float32
		if side == 0 {
			perp = (float32(mapX) - p.X + float32(1-stepX)/2) / rdX
		} else {
			perp = (float32(mapY) - p.Y + float32(1-stepY)/2) / rdY
		}
		if perp < 1e-4 {
			perp = 1e-4
		}
		fb.zbuf[x] = perp

		lineH := int32(float32(h) / perp)
		drawStart := (int32(h) - lineH) / 2
		if drawStart < 0 {
			drawStart = 0
		}
		drawEnd := (int32(h) + lineH) / 2
		if drawEnd > int32(h) {
			drawEnd = int32(h)
		}
		fb.wallTop[x] = drawStart
		fb.wallBottom[x] = drawEnd

		wallX := p.X + perp*rdX
		if side == 0 {
			wallX = p.Y + perp*rdY
		}
		wallX = wallX - float32(math.Floor(float64(wallX)))
		texX := int(wallX * TexSize)
		if (side == 0 && rdX > 0) || (side == 1 && rdY < 0) {
			if texX > TexSize-1 {
				texX = TexSize - 1
			}
			texX = TexSize - 1 - texX
		}
		texIdx := int(texID) - 1
		if texIdx < 0 {
			texIdx = 0
		}
		if texIdx >= len(r.textures) {
			texIdx = len(r.textures) - 1
		}
		texture := r.textures[texIdx]
		sideShade := float32(1)
		if side == 1 {
			sideShade = 0.72
		}
		fogShade := (1 / (1 + perp*fog)) * sideShade * light
		lh := lineH
		if lh < 1 {
			lh = 1
		}
		texStep := float32(TexSize) / float32(lh)
		texPos := (float32(drawStart) - float32(h)/2 + float32(lineH)/2) * texStep
		for y := drawStart; y < drawEnd; y++ {
			texY := int(texPos)
			if texY > TexSize-1 {
				texY = TexSize - 1
			}
			texPos += texStep
			fb.put(x, int(y), shade(texture.Sample(texX, texY), fogShade))
		}
	}
}

func (r *Renderer) drawImps(fb *FrameBuffer, game *Game, light float32) {
	w, h := fb.W, fb.H
	p := &game.Player
	dirX, dirY := p.Dir()
	planeX, planeY := -dirY*planeLen, dirX*planeLen
	invDet := 1 / (planeX*dirY - dirX*planeY)

	order := fb.spriteOrder[:0]
	for i, imp := range game.Imps {
		dx := imp.X - p.X
		dy := imp.Y - p.Y
		order = append(order, spriteOrder{i: i, d2: dx*dx + dy*dy})
	}
	for i := 0; i < len(order); i++ {
		for j := i + 1; j < len(order); j++ {
			if order[j].d2 > order[i].d2 {
				order[i], order[j] = order[j], order[i]
			}
		}
	}

	for _, item := range order {
		imp := game.Imps[item.i]
		relX := imp.X - p.X
		relY := imp.Y - p.Y
		tx := invDet * (dirY*relX - dirX*relY)
		ty := invDet * (-planeY*relX + planeX*relY)
		if ty <= 0.08 {
			continue
		}
		var sprite Sprite
		switch imp.Visual() {
		case VisualWalkA:
			sprite = r.imps.WalkA
		case VisualWalkB:
			sprite = r.imps.WalkB
		case VisualAttack:
			sprite = r.imps.Attack
		case VisualPain:
			sprite = r.imps.Pain
		case VisualDieA:
			sprite = r.imps.DieA
		case VisualDieB:
			sprite = r.imps.DieB
		default:
			sprite = r.imps.Corpse
		}
		screenX := (float32(w) / 2) * (1 + tx/ty)
		yFeet := float32(h)/2 + 0.5*float32(h)/ty
		yHead := float32(h)/2 + (0.5-impWorldHeight)*float32(h)/ty
		spriteH := yFeet - yHead
		if spriteH < 1 {
			spriteH = 1
		}
		spriteW := spriteH * float32(sprite.W) / float32(sprite.H)
		bobPx := float32(0)
		if phase, ok := imp.WalkBob(); ok {
			bobPx = phase * spriteH * 0.02
		}
		x0 := int32(math.Floor(float64(screenX - spriteW/2)))
		x1 := int32(math.Ceil(float64(screenX + spriteW/2)))
		y0 := int32(math.Floor(float64(yHead + bobPx)))
		y1 := int32(math.Ceil(float64(yFeet + bobPx)))
		fogShade := (1 / (1 + ty*fog)) * light
		if imp.Visual() != VisualCorpse {
			drawContactShadow(fb, screenX, yFeet, spriteW, spriteH, ty)
		}
		painFlash := imp.Visual() == VisualPain
		sx0 := x0
		if sx0 < 0 {
			sx0 = 0
		}
		sx1 := x1
		if sx1 > int32(w) {
			sx1 = int32(w)
		}
		sy0 := y0
		if sy0 < 0 {
			sy0 = 0
		}
		sy1 := y1
		if sy1 > int32(h) {
			sy1 = int32(h)
		}
		spanX := x1 - x0
		if spanX < 1 {
			spanX = 1
		}
		spanY := y1 - y0
		if spanY < 1 {
			spanY = 1
		}
		for sx := sx0; sx < sx1; sx++ {
			if fb.zbuf[sx] <= ty {
				continue
			}
			u := float32(sx-x0) / float32(spanX)
			for sy := sy0; sy < sy1; sy++ {
				v := float32(sy-y0) / float32(spanY)
				c, ok := sprite.Sample(u, v)
				if !ok {
					continue
				}
				lit := c
				if c != eyeGlow {
					lit = shade(c, fogShade)
				}
				if painFlash {
					lit = lerpColor(lit, Rgb{255, 255, 255}, 0.40)
				}
				fb.put(int(sx), int(sy), lit)
			}
		}
	}
	fb.spriteOrder = order
}

func (r *Renderer) drawGun(fb *FrameBuffer, game *Game) {
	w, h := fb.W, fb.H
	sprite := r.guns.Idle
	if game.Player.Muzzle > 0 {
		sprite = r.guns.Fire
	}
	gunH := int32(float32(h) * 0.42)
	gunW := gunH * int32(sprite.W) / int32(sprite.H)
	bobX := float32(math.Sin(float64(game.Player.Bob*1.7))) * float32(w) * 0.012
	bobY := float32(math.Abs(math.Cos(float64(game.Player.Bob*3.4)))) * float32(h) * 0.018
	x0 := int32(w)/2 - gunW/2 + int32(bobX)
	y0 := int32(h) - gunH + int32(bobY)

	sy0 := y0
	if sy0 < 0 {
		sy0 = 0
	}
	sx0 := x0
	if sx0 < 0 {
		sx0 = 0
	}
	sx1 := x0 + gunW
	if sx1 > int32(w) {
		sx1 = int32(w)
	}
	gh := gunH
	if gh < 1 {
		gh = 1
	}
	gw := gunW
	if gw < 1 {
		gw = 1
	}
	for sy := sy0; sy < int32(h); sy++ {
		v := float32(sy-y0) / float32(gh)
		for sx := sx0; sx < sx1; sx++ {
			u := float32(sx-x0) / float32(gw)
			if c, ok := sprite.Sample(u, v); ok {
				fb.put(int(sx), int(sy), c)
			}
		}
	}

	cx, cy := w/2, h/2
	_, onTarget := game.TargetInCrosshair()
	chC := Rgb{210, 210, 210}
	if onTarget {
		chC = GboomRed
	}
	for d := 2; d < 5; d++ {
		if cx >= d && cx+d < w {
			fb.put(cx-d, cy, chC)
			fb.put(cx+d, cy, chC)
		}
		if cy >= d && cy+d < h {
			fb.put(cx, cy-d, chC)
			fb.put(cx, cy+d, chC)
		}
	}
	if onTarget {
		fb.put(cx, cy, chC)
	}
}

func drawContactShadow(fb *FrameBuffer, centerX, yFeet, spriteW, spriteH, depth float32) {
	w, h := fb.W, fb.H
	rx := spriteW * 0.38
	if rx < 1 {
		rx = 1
	}
	ry := spriteH * 0.05
	if ry < 1.5 {
		ry = 1.5
	}
	x0 := int32(math.Floor(float64(centerX - rx)))
	x1 := int32(math.Ceil(float64(centerX + rx)))
	y0 := int32(math.Floor(float64(yFeet - ry)))
	y1 := int32(math.Ceil(float64(yFeet + ry)))
	sx0 := x0
	if sx0 < 0 {
		sx0 = 0
	}
	sx1 := x1
	if sx1 > int32(w) {
		sx1 = int32(w)
	}
	sy0 := y0
	if sy0 < 0 {
		sy0 = 0
	}
	sy1 := y1
	if sy1 > int32(h) {
		sy1 = int32(h)
	}
	for sx := sx0; sx < sx1; sx++ {
		if fb.zbuf[sx] <= depth {
			continue
		}
		nx := (float32(sx) - centerX) / rx
		for sy := sy0; sy < sy1; sy++ {
			ny := (float32(sy) - yFeet) / ry
			r2 := nx*nx + ny*ny
			if r2 < 1 {
				fb.darken(int(sx), int(sy), 0.55+0.45*r2)
			}
		}
	}
}

const fireMax = 36

// FireSim is the classic PSX-style fire cellular automaton.
type FireSim struct {
	w, h int
	heat []uint8
	rng  XorShift64
}

func NewFireSim() *FireSim {
	w, h := 160, 84
	heat := make([]uint8, w*h)
	for x := 0; x < w; x++ {
		heat[(h-1)*w+x] = fireMax
	}
	return &FireSim{w: w, h: h, heat: heat, rng: NewXorShift64(0xDEADBEEFCAFEF00D)}
}

func (f *FireSim) Step() {
	for y := 1; y < f.h; y++ {
		for x := 0; x < f.w; x++ {
			src := y*f.w + x
			r := f.rng.NextU32()
			decay := int32(r & 1)
			drift := (r >> 2) % 3
			dstX := remEuclidN(int32(x)+int32(drift)-1, int32(f.w))
			dst := (y-1)*f.w + int(dstX)
			v := int32(f.heat[src]) - decay
			if v < 0 {
				v = 0
			}
			f.heat[dst] = uint8(v)
		}
	}
}

func firePalette(heat uint8) Rgb {
	t := float32(heat) / fireMax
	switch {
	case t < 0.02:
		return Rgb{7, 7, 9}
	case t < 0.4:
		return lerpColor(Rgb{24, 8, 6}, Rgb{180, 30, 10}, t/0.4)
	case t < 0.75:
		return lerpColor(Rgb{180, 30, 10}, Rgb{240, 150, 30}, (t-0.4)/0.35)
	default:
		return lerpColor(Rgb{240, 150, 30}, Rgb{255, 250, 200}, (t-0.75)/0.25)
	}
}

func (f *FireSim) Draw(fb *FrameBuffer, frac float32) {
	w, h := fb.W, fb.H
	if w == 0 || h == 0 {
		return
	}
	fireH := int(float32(h) * frac)
	yStart := h - fireH
	if yStart < 0 {
		yStart = 0
	}
	if fireH > h {
		fireH = h
	}
	den := fireH
	if den < 1 {
		den = 1
	}
	for y := yStart; y < h; y++ {
		fy := (y - yStart) * f.h / den
		if fy > f.h-1 {
			fy = f.h - 1
		}
		for x := 0; x < w; x++ {
			fx := x * f.w / w
			if fx > f.w-1 {
				fx = f.w - 1
			}
			heat := f.heat[fy*f.w+fx]
			if heat > 1 {
				fb.put(x, y, firePalette(heat))
			}
		}
	}
}

func clearFB(fb *FrameBuffer, c Rgb) {
	for i := 0; i+2 < len(fb.Pixels); i += 3 {
		fb.Pixels[i] = c[0]
		fb.Pixels[i+1] = c[1]
		fb.Pixels[i+2] = c[2]
	}
}

func textWidth(text string, scale int) int {
	n := 0
	for range text {
		n++
	}
	return n * 6 * scale
}

func drawText(fb *FrameBuffer, text string, x0, y0 int32, scale int, c Rgb) {
	penX := x0
	for _, ch := range text {
		glyph := glyph5x7(ch)
		for row, bits := range glyph {
			for col := 0; col < 5; col++ {
				if bits&(1<<uint(4-col)) == 0 {
					continue
				}
				for dy := 0; dy < scale; dy++ {
					for dx := 0; dx < scale; dx++ {
						px := penX + int32(col*scale+dx)
						py := y0 + int32(row*scale+dy)
						if px >= 0 && py >= 0 && int(px) < fb.W && int(py) < fb.H {
							fb.put(int(px), int(py), c)
						}
					}
				}
			}
		}
		penX += int32(6 * scale)
	}
}

func drawTextCenteredOutlined(fb *FrameBuffer, text string, y0 int32, scale int, c, outline Rgb) {
	x0 := (int32(fb.W) - int32(textWidth(text, scale))) / 2
	o := int32(scale / 2)
	if o < 1 {
		o = 1
	}
	for _, d := range [][2]int32{
		{-o, -o}, {0, -o}, {o, -o},
		{-o, 0}, {o, 0},
		{-o, o}, {0, o}, {o, o},
	} {
		drawText(fb, text, x0+d[0], y0+d[1], scale, outline)
	}
	drawText(fb, text, x0, y0, scale, c)
}

func remEuclid1(v float32) float32 {
	r := float32(math.Mod(float64(v), 1))
	if r < 0 {
		r += 1
	}
	return r
}

func remEuclidN(a, n int32) int32 {
	r := a % n
	if r < 0 {
		r += n
	}
	return r
}
