package gboom

import "math"

type Rgb [3]uint8

const TexSize = 64

var GboomRed = Rgb{235, 40, 32}
var eyeGlow = Rgb{255, 216, 0}

type XorShift64 struct{ s uint64 }

func NewXorShift64(seed uint64) XorShift64 {
	if seed == 0 {
		seed = 1
	}
	return XorShift64{s: seed}
}

func (r *XorShift64) NextU32() uint32 {
	r.s ^= r.s >> 12
	r.s ^= r.s << 25
	r.s ^= r.s >> 27
	return uint32((r.s * 0x2545F4914F6CDD1D) >> 33)
}

func (r *XorShift64) NextF32() float32 {
	return float32(r.NextU32()>>8) / float32(uint32(1)<<24)
}

func hash01(x, y, seed uint32) float32 {
	h := x*0x9E3779B9 + y*0x85EBCA6B + seed*0xC2B2AE35
	h ^= h >> 16
	h *= 0x7FEB352D
	h ^= h >> 15
	h *= 0x846CA68B
	h ^= h >> 16
	return float32(h&0xFFFF) / 65536
}

func scale(c Rgb, f float32) Rgb {
	cl := func(v float32) uint8 {
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return uint8(v)
	}
	return Rgb{cl(float32(c[0]) * f), cl(float32(c[1]) * f), cl(float32(c[2]) * f)}
}

type Texture struct{ Pixels []Rgb }

func (t Texture) Sample(x, y int) Rgb {
	return t.Pixels[(y&(TexSize-1))*TexSize+(x&(TexSize-1))]
}

func buildTextures() []Texture {
	return []Texture{brick(), stone(), tech(), hellstone()}
}

func brick() Texture {
	base, mortar := Rgb{148, 64, 44}, Rgb{78, 60, 54}
	pixels := make([]Rgb, TexSize*TexSize)
	i := 0
	for y := 0; y < TexSize; y++ {
		for x := 0; x < TexSize; x++ {
			row := y / 8
			off := 0
			if row%2 != 0 {
				off = 8
			}
			if y%8 == 0 || (x+off)%16 == 0 {
				pixels[i] = mortar
			} else {
				brickID := uint32(row)*31 + uint32((x+off)/16)
				tone := 0.82 + 0.30*hash01(brickID, 7, 1)
				grain := 0.92 + 0.16*hash01(uint32(x), uint32(y), 2)
				pixels[i] = scale(base, tone*grain)
			}
			i++
		}
	}
	return Texture{pixels}
}

func stone() Texture {
	base, grout := Rgb{118, 118, 126}, Rgb{58, 58, 64}
	pixels := make([]Rgb, TexSize*TexSize)
	i := 0
	for y := 0; y < TexSize; y++ {
		for x := 0; x < TexSize; x++ {
			row := y / 16
			off := 0
			if row%2 != 0 {
				off = 16
			}
			if y%16 == 0 || (x+off)%32 == 0 {
				pixels[i] = grout
			} else {
				blockID := uint32(row)*17 + uint32((x+off)/32)
				tone := 0.80 + 0.28*hash01(blockID, 3, 3)
				grain := 0.90 + 0.20*hash01(uint32(x), uint32(y), 4)
				pixels[i] = scale(base, tone*grain)
			}
			i++
		}
	}
	return Texture{pixels}
}

func tech() Texture {
	base, seam, light := Rgb{74, 82, 92}, Rgb{38, 42, 48}, Rgb{110, 240, 130}
	pixels := make([]Rgb, TexSize*TexSize)
	i := 0
	for y := 0; y < TexSize; y++ {
		for x := 0; x < TexSize; x++ {
			inSeam := y%16 == 0 || y%16 == 15 || x%32 == 0
			isLight := y%16 == 8 && x%8 == 4 && hash01(uint32(x/8), uint32(y/16), 5) > 0.35
			switch {
			case isLight:
				pixels[i] = light
			case inSeam:
				pixels[i] = seam
			default:
				pixels[i] = scale(base, 0.88+0.22*hash01(uint32(x), uint32(y), 6))
			}
			i++
		}
	}
	return Texture{pixels}
}

func buildFloorTexture() Texture {
	base, grout := Rgb{96, 86, 74}, Rgb{44, 40, 36}
	pixels := make([]Rgb, TexSize*TexSize)
	i := 0
	for y := 0; y < TexSize; y++ {
		for x := 0; x < TexSize; x++ {
			if y%32 == 0 || x%32 == 0 {
				pixels[i] = grout
			} else {
				tileID := uint32(y/32)*5 + uint32(x/32)
				tone := 0.78 + 0.30*hash01(tileID, 11, 9)
				grime := float32(1)
				if hash01(uint32(x/6), uint32(y/6), 10) > 0.72 {
					grime = 0.78
				}
				grain := 0.90 + 0.20*hash01(uint32(x), uint32(y), 11)
				pixels[i] = scale(base, tone*grime*grain)
			}
			i++
		}
	}
	return Texture{pixels}
}

func buildCeilingTexture() Texture {
	base, seam, lamp := Rgb{52, 56, 66}, Rgb{30, 32, 38}, Rgb{232, 226, 198}
	pixels := make([]Rgb, TexSize*TexSize)
	i := 0
	for y := 0; y < TexSize; y++ {
		for x := 0; x < TexSize; x++ {
			inSeam := y%16 == 0 || x%16 == 0
			hasLamp := hash01(uint32(x/16), uint32(y/16), 12) > 0.84
			inLamp := hasLamp && x%16 >= 4 && x%16 < 12 && y%16 >= 4 && y%16 < 12
			switch {
			case inLamp:
				pixels[i] = lamp
			case inSeam:
				pixels[i] = seam
			default:
				pixels[i] = scale(base, 0.88+0.20*hash01(uint32(x), uint32(y), 13))
			}
			i++
		}
	}
	return Texture{pixels}
}

func hellstone() Texture {
	base, vein := Rgb{120, 30, 28}, Rgb{200, 80, 50}
	pixels := make([]Rgb, TexSize*TexSize)
	i := 0
	for y := 0; y < TexSize; y++ {
		for x := 0; x < TexSize; x++ {
			fx := float32(x) / TexSize
			fy := float32(y) / TexSize
			n := hash01(uint32(x/4), uint32(y/4), 7)
			v := pow3((float32(math.Sin(float64(fx*9+fy*4+n*3)))*0.5 + 0.5))
			grain := 0.85 + 0.25*hash01(uint32(x), uint32(y), 8)
			pixels[i] = Rgb{
				uint8((float32(base[0])*(1-v) + float32(vein[0])*v) * grain),
				uint8((float32(base[1])*(1-v) + float32(vein[1])*v) * grain),
				uint8((float32(base[2])*(1-v) + float32(vein[2])*v) * grain),
			}
			i++
		}
	}
	return Texture{pixels}
}

func pow3(v float32) float32 { return v * v * v }

func spriteColor(ch byte) (Rgb, bool) {
	switch ch {
	case '.':
		return Rgb{}, false
	case 'B':
		return Rgb{146, 90, 50}, true
	case 'b':
		return Rgb{104, 62, 34}, true
	case 'H':
		return Rgb{222, 214, 188}, true
	case 'E':
		return eyeGlow, true
	case 'M':
		return Rgb{34, 20, 16}, true
	case 'T':
		return Rgb{236, 232, 220}, true
	case 'C':
		return Rgb{214, 196, 160}, true
	case 'R':
		return Rgb{186, 28, 24}, true
	case 'r':
		return Rgb{120, 16, 14}, true
	case 'G':
		return Rgb{96, 104, 112}, true
	case 'g':
		return Rgb{52, 58, 66}, true
	case 'W':
		return Rgb{224, 228, 232}, true
	case 'S':
		return Rgb{212, 160, 116}, true
	case 's':
		return Rgb{164, 116, 80}, true
	case 'F':
		return Rgb{255, 244, 160}, true
	case 'f':
		return Rgb{255, 168, 48}, true
	default:
		return Rgb{}, false
	}
}

type Sprite struct {
	W, H   int
	pixels []Rgb
	mask   []bool
}

func spriteFromArt(art []string) Sprite {
	h := len(art)
	w := 0
	if h > 0 {
		w = len(art[0])
	}
	s := Sprite{W: w, H: h, pixels: make([]Rgb, w*h), mask: make([]bool, w*h)}
	i := 0
	for _, row := range art {
		for x := 0; x < w; x++ {
			if c, ok := spriteColor(row[x]); ok {
				s.pixels[i] = c
				s.mask[i] = true
			}
			i++
		}
	}
	return s
}

func (s Sprite) Sample(u, v float32) (Rgb, bool) {
	x := int(u * float32(s.W))
	y := int(v * float32(s.H))
	if x >= s.W {
		x = s.W - 1
	}
	if y >= s.H {
		y = s.H - 1
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	i := y*s.W + x
	if !s.mask[i] {
		return Rgb{}, false
	}
	return s.pixels[i], true
}

type ImpSprites struct {
	WalkA, WalkB, Attack, Pain, DieA, DieB, Corpse Sprite
}

func buildImpSprites() ImpSprites {
	return ImpSprites{
		WalkA: spriteFromArt([]string{
			"..H..........H..",
			"..HH........HH..",
			"...bBBBBBBBBb...",
			"...BBBBBBBBBB...",
			"..BBEEBBBBEEBB..",
			"..BBEEBBBBEEBB..",
			"...BBBBBBBBBB...",
			"...BbMTMTMTbB...",
			"....bBBBBBBb....",
			"..bBBBBBBBBBBb..",
			".CBBb.BBBB.bBBC.",
			".CBB..BBBB..BBC.",
			".CC...BBBB...CC.",
			"......bBBb......",
			".....BB..BB.....",
			"....BB....BB....",
			"....BB.....BB...",
			"...bB.......Bb..",
			"...BB.......BB..",
			"..CC.........CC.",
		}),
		WalkB: spriteFromArt([]string{
			"..H..........H..",
			"..HH........HH..",
			"...bBBBBBBBBb...",
			"...BBBBBBBBBB...",
			"..BBEEBBBBEEBB..",
			"..BBEEBBBBEEBB..",
			"...BBBBBBBBBB...",
			"...BbMTMTMTbB...",
			"....bBBBBBBb....",
			"..bBBBBBBBBBBb..",
			".CBBb.BBBB.bBBC.",
			".CBB..BBBB..BBC.",
			".CC...BBBB...CC.",
			"......bBBb......",
			".....BB..BB.....",
			"....BB.....BB...",
			"...BB.......BB..",
			"...Bb.......bB..",
			"...BB........BB.",
			"..CC..........CC",
		}),
		Attack: spriteFromArt([]string{
			".CC.H......H.CC.",
			".CBBHH....HHBBC.",
			".CBBbBBBBBBbBBC.",
			"..BBBBBBBBBBBB..",
			"..BBEEBBBBEEBB..",
			"..bBEEBBBBEEBb..",
			"...BBBBBBBBBB...",
			"...BbMMMMMMbB...",
			"...BbMTMTMTbB...",
			"....bBBBBBBb....",
			"...BBBBBBBBBB...",
			"...BBBBBBBBBB...",
			"....BBBBBBBB....",
			"......bBBb......",
			".....BB..BB.....",
			"....BB....BB....",
			"....BB....BB....",
			"...bB......Bb...",
			"...BB......BB...",
			"..CC........CC..",
		}),
		Pain: spriteFromArt([]string{
			"....H......H....",
			"...HH.....HH....",
			"..bBBBBBBBBb....",
			".RBBBBBBBBBB....",
			".RBBMMBBBMMBB...",
			"..rBBBBBBBBBR...",
			"...BBBBBBBBBB...",
			"...BbMMMMMMbB...",
			"....bBBBBBBbR...",
			"..bBBBBBBBBBBb..",
			".CBBb.BBBB.bBBC.",
			".CBB..BBBB..BBC.",
			".CC...BBBB...CC.",
			"......bBBb......",
			".....BB..BB.....",
			"....BB....BB....",
			"....BB....BB....",
			"...bB......Bb...",
			"...BB......BB...",
			"..CC........CC..",
		}),
		DieA: spriteFromArt([]string{
			"................",
			"................",
			"................",
			"...H........H...",
			"...HHBBBBBBHH...",
			"..bBBBBBBBBBBb..",
			"..RBMMBBBBMMBR..",
			"..rBBBBBBBBBBr..",
			"...BbMMMMMMbB...",
			"..RbBBBBBBBBbR..",
			".CBBBBBBBBBBBBC.",
			".CBb.BBBBBB.bBC.",
			".CC..BBBBBB..CC.",
			"....bBBBBBBb....",
			"...RBB....BBR...",
			"...BB......BB...",
			"..rB........Br..",
			"..BB........BB..",
			".RCC........CCR.",
			"................",
		}),
		DieB: spriteFromArt([]string{
			"................",
			"................",
			"................",
			"................",
			"................",
			"................",
			"................",
			"....H......H....",
			"...HHBBBBBHH....",
			"..RbBBBBBBBbR...",
			"..rBMMBBBMMBr...",
			"..RBBBBBBBBBR...",
			".RbBBBBBBBBBbR..",
			".CBBBBBBBBBBBC..",
			".CBbRBBBBBBRbC..",
			"..RR.BBBBB.RR...",
			"...RbBBBBBbR....",
			"..RRBBBBBBRR....",
			".rRRRbBBbRRRr...",
			"................",
		}),
		Corpse: spriteFromArt([]string{
			"................",
			"................",
			"................",
			"................",
			"................",
			"................",
			"................",
			"................",
			"................",
			"................",
			"................",
			"................",
			"................",
			"....rr..r.......",
			"..rRRrrRRr.r....",
			".rRbBBBBbRRrr...",
			"rRRBbHbBBbRRRr..",
			".rrRRbBBbRRrr...",
			"..r.rRRRRr.r....",
			"................",
		}),
	}
}

type GunSprites struct{ Idle, Fire Sprite }

func buildGunSprites() GunSprites {
	return GunSprites{
		Idle: spriteFromArt([]string{
			"........................",
			"..........WGG...........",
			".........GGGGG..........",
			".........gGGGGg.........",
			".........gGGGGg.........",
			".........gGGGGg.........",
			"........gGGGGGGg........",
			"........gGGGGGGg........",
			".......gGGGGGGGGg.......",
			".......sSGGGGGGSs.......",
			"......sSSSGGGGSSSs......",
			".....sSSSSSGGSSSSSs.....",
			"....sSSSSSSSSSSSSSSs....",
			"....sSSSSSSSSSSSSSs.....",
			"...sSSSSSSSSSSSSSSs.....",
			"...sSSSSSSSSSSSSSs......",
			"..sSSSSSSSSSSSSSSs......",
			"..sSSSSSSSSSSSSSs.......",
		}),
		Fire: spriteFromArt([]string{
			".........fFFf...........",
			"........fFFFFf..........",
			".......fFFFFFFf.........",
			"........fFFFFf..........",
			".........FGGF...........",
			".........gGGGGg.........",
			"........gGGGGGGg........",
			"........gGGGGGGg........",
			".......gGGGGGGGGg.......",
			".......sSGGGGGGSs.......",
			"......sSSSGGGGSSSs......",
			".....sSSSSSGGSSSSSs.....",
			"....sSSSSSSSSSSSSSSs....",
			"....sSSSSSSSSSSSSSs.....",
			"...sSSSSSSSSSSSSSSs.....",
			"...sSSSSSSSSSSSSSs......",
			"..sSSSSSSSSSSSSSSs......",
			"..sSSSSSSSSSSSSSs.......",
		}),
	}
}

func glyph5x7(ch rune) [7]uint8 {
	switch ch {
	case 'a', 'A':
		return [7]uint8{0b01110, 0b10001, 0b10001, 0b11111, 0b10001, 0b10001, 0b10001}
	case 'b', 'B':
		return [7]uint8{0b11110, 0b10001, 0b10001, 0b11110, 0b10001, 0b10001, 0b11110}
	case 'c', 'C':
		return [7]uint8{0b01110, 0b10001, 0b10000, 0b10000, 0b10000, 0b10001, 0b01110}
	case 'd', 'D':
		return [7]uint8{0b11110, 0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b11110}
	case 'e', 'E':
		return [7]uint8{0b11111, 0b10000, 0b10000, 0b11110, 0b10000, 0b10000, 0b11111}
	case 'g', 'G':
		return [7]uint8{0b01110, 0b10001, 0b10000, 0b10111, 0b10001, 0b10001, 0b01111}
	case 'h', 'H':
		return [7]uint8{0b10001, 0b10001, 0b10001, 0b11111, 0b10001, 0b10001, 0b10001}
	case 'i', 'I':
		return [7]uint8{0b11111, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100, 0b11111}
	case 'k', 'K':
		return [7]uint8{0b10001, 0b10010, 0b10100, 0b11000, 0b10100, 0b10010, 0b10001}
	case 'l', 'L':
		return [7]uint8{0b10000, 0b10000, 0b10000, 0b10000, 0b10000, 0b10000, 0b11111}
	case 'm', 'M':
		return [7]uint8{0b10001, 0b11011, 0b10101, 0b10101, 0b10001, 0b10001, 0b10001}
	case 'n', 'N':
		return [7]uint8{0b10001, 0b11001, 0b10101, 0b10011, 0b10001, 0b10001, 0b10001}
	case 'o', 'O':
		return [7]uint8{0b01110, 0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b01110}
	case 'p', 'P':
		return [7]uint8{0b11110, 0b10001, 0b10001, 0b11110, 0b10000, 0b10000, 0b10000}
	case 'r', 'R':
		return [7]uint8{0b11110, 0b10001, 0b10001, 0b11110, 0b10100, 0b10010, 0b10001}
	case 's', 'S':
		return [7]uint8{0b01111, 0b10000, 0b10000, 0b01110, 0b00001, 0b00001, 0b11110}
	case 't', 'T':
		return [7]uint8{0b11111, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100}
	case 'u', 'U':
		return [7]uint8{0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b01110}
	case 'v', 'V':
		return [7]uint8{0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b01010, 0b00100}
	case 'y', 'Y':
		return [7]uint8{0b10001, 0b10001, 0b01010, 0b00100, 0b00100, 0b00100, 0b00100}
	case '!':
		return [7]uint8{0b00100, 0b00100, 0b00100, 0b00100, 0b00100, 0b00000, 0b00100}
	case '-':
		return [7]uint8{0b00000, 0b00000, 0b00000, 0b01110, 0b00000, 0b00000, 0b00000}
	default:
		return [7]uint8{}
	}
}
