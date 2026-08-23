package gboom

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"time"
)

const (
	maxFrameW = 480
	maxFrameH = 320
	cellPxW   = 8
	cellPxH   = 16
	maxDT     = 0.1
	// Delay before end screens accept a dismissal key.
	EndScreenGrace = 0.8
	// Radians of yaw per terminal cell of horizontal mouse motion.
	mouseAimSensitivity float32 = 0.06
	// Column jumps larger than this re-seed the baseline without yawing.
	maxMouseAimDX = 12
)

var textOutline = Rgb{16, 6, 6}

// Phase is where the player is in the easter-egg flow.
type Phase int

const (
	PhaseTitle Phase = iota
	PhasePlaying
	PhaseWon
	PhaseDead
)

// KeyOutcome is the result of a key event while the game modal is open.
type KeyOutcome int

const (
	KeyChanged KeyOutcome = iota
	KeyClose
)

// HUD values for the overlay chrome.
type HUD struct {
	HP           int32
	Kills, Total uint32
	Playing      bool
}

// MouseKind is the subset of mouse events the game consumes.
type MouseKind int

const (
	MouseMoved MouseKind = iota
	MouseDrag
	MouseClick
)

// State is the modal /gboom easter egg. Ticked at ~30 fps; PNG frames match
// the Rust kitty pipeline.
type State struct {
	game     *Game
	renderer *Renderer
	fire     *FireSim
	phase    Phase
	lastTick time.Time
	simGen   uint64
	fb       FrameBuffer
	png      []byte
	cached   cachedFrame
	hasCache bool
	// Wall-clock time inside the current phase.
	phaseTime    float32
	lastMouseCol *uint16
	mouseRegion  *[4]uint16 // x, y, w, h
}

type cachedFrame struct {
	gen  uint64
	w, h int
}

func NewState() *State {
	return &State{
		game:     NewGame(),
		renderer: NewRenderer(),
		fire:     NewFireSim(),
		phase:    PhaseTitle,
		lastTick: time.Now(),
	}
}

func (s *State) SetReleaseAware(v bool) { s.game.SetReleaseAware(v) }
func (s *State) Phase() Phase           { return s.phase }
func (s *State) Game() *Game            { return s.game }
func (s *State) AnyMovementHeld() bool  { return s.game.AnyHeld() }

func (s *State) SetMouseRegion(x, y, width, height int) {
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	r := [4]uint16{uint16(x), uint16(y), uint16(width), uint16(height)}
	s.mouseRegion = &r
}

func (s *State) ClearMouseRegion() {
	s.mouseRegion = nil
	s.lastMouseCol = nil
}

func (s *State) inMouseRegion(col, row uint16) bool {
	if s.mouseRegion == nil {
		return false
	}
	x, y, w, h := s.mouseRegion[0], s.mouseRegion[1], s.mouseRegion[2], s.mouseRegion[3]
	return col >= x && row >= y && col < x+w && row < y+h
}

func controlFor(key string) (Control, bool) {
	switch key {
	case "w", "W", "up":
		return ControlForward, true
	case "s", "S", "down":
		return ControlBack, true
	case "a", "A":
		return ControlStrafeLeft, true
	case "d", "D":
		return ControlStrafeRight, true
	case "left":
		return ControlTurnLeft, true
	case "right":
		return ControlTurnRight, true
	}
	return 0, false
}

// Tick advances the game by wall-clock time.
func (s *State) Tick() {
	dt := float32(time.Since(s.lastTick).Seconds())
	if dt > maxDT {
		dt = maxDT
	}
	s.lastTick = time.Now()
	s.phaseTime += dt
	switch s.phase {
	case PhaseTitle, PhaseWon, PhaseDead:
		s.fire.Step()
	case PhasePlaying:
		s.game.Step(dt)
		if s.game.Dead() {
			s.setPhase(PhaseDead)
		} else if s.game.Won() && s.allCorpsesSettled() {
			s.setPhase(PhaseWon)
		}
	}
	s.simGen++
}

func (s *State) setPhase(phase Phase) {
	s.phase = phase
	s.phaseTime = 0
	s.lastMouseCol = nil
}

func (s *State) allCorpsesSettled() bool {
	for _, imp := range s.game.Imps {
		if imp.State.Kind != ImpDead {
			return false
		}
	}
	return true
}

// HandleKey consumes a bubbletea key string.
func (s *State) HandleKey(key string) KeyOutcome {
	if key == "esc" || key == "q" || key == "Q" || key == "ctrl+c" {
		return KeyClose
	}
	switch s.phase {
	case PhaseTitle:
		s.setPhase(PhasePlaying)
		s.lastTick = time.Now()
		s.simGen++
	case PhasePlaying:
		if c, ok := controlFor(key); ok {
			s.game.Press(c)
		} else if key == " " || key == "space" || key == "enter" {
			s.game.QueueFire()
		}
	case PhaseWon, PhaseDead:
		if s.phaseTime > EndScreenGrace {
			return KeyClose
		}
	}
	return KeyChanged
}

// HandleRelease un-latches a movement control (release-aware terminals).
func (s *State) HandleRelease(key string) {
	if s.phase != PhasePlaying {
		return
	}
	if c, ok := controlFor(key); ok {
		s.game.Release(c)
	}
}

// HandleMouse aims on move/drag and fires on left-click (Playing only).
func (s *State) HandleMouse(kind MouseKind, col, row int) {
	if s.phase != PhasePlaying {
		return
	}
	c, r := uint16(col), uint16(row)
	inRegion := s.inMouseRegion(c, r)
	switch kind {
	case MouseClick:
		if inRegion {
			s.game.QueueFire()
		}
	case MouseMoved, MouseDrag:
		if !inRegion {
			s.lastMouseCol = nil
			return
		}
		if s.lastMouseCol != nil {
			dx := int32(c) - int32(*s.lastMouseCol)
			if dx > maxMouseAimDX || dx < -maxMouseAimDX {
				s.lastMouseCol = &c
				return
			}
			if dx != 0 {
				s.game.Player.Angle += float32(dx) * mouseAimSensitivity
				s.simGen++
			}
		}
		s.lastMouseCol = &c
	}
}

func (s *State) ReleaseAll() {
	s.game.ReleaseAll()
	s.lastMouseCol = nil
}

func (s *State) HUD() HUD {
	return HUD{
		HP:      s.game.Player.HP,
		Kills:   s.game.Kills,
		Total:   s.game.TotalImps(),
		Playing: s.phase == PhasePlaying,
	}
}

// FrameSizeForCells maps a cell box to the internal render resolution.
func FrameSizeForCells(cols, rows int) (int, int) {
	w := cols * cellPxW
	h := rows * cellPxH
	if w < 64 {
		w = 64
	}
	if h < 64 {
		h = 64
	}
	scale := math.Min(float64(maxFrameW)/float64(w), float64(maxFrameH)/float64(h))
	if scale > 1 {
		scale = 1
	}
	w = int(float64(w) * scale)
	h = int(float64(h) * scale)
	if w < 64 {
		w = 64
	}
	if h < 64 {
		h = 64
	}
	return w, h
}

func (s *State) ensureFrame(w, h int) {
	if w < 8 || h < 8 {
		return
	}
	if s.hasCache && s.cached == (cachedFrame{s.simGen, w, h}) {
		return
	}
	s.fb.Resize(w, h)
	switch s.phase {
	case PhaseTitle:
		s.renderTitleScreen()
	case PhasePlaying:
		s.renderer.RenderGame(&s.fb, s.game)
	case PhaseWon:
		s.renderEndScreen("VICTORY!", Rgb{255, 214, 80})
	case PhaseDead:
		s.renderEndScreen("YOU DIED", GboomRed)
	}
	s.hasCache = true
	s.cached = cachedFrame{s.simGen, w, h}
	s.png = nil
}

// FramePixels returns the current RGB8 framebuffer at (w, h).
func (s *State) FramePixels(w, h int) []byte {
	s.ensureFrame(w, h)
	return s.fb.Pixels
}

// FramePNG returns the current frame PNG-encoded, cached per (simGen, w, h).
func (s *State) FramePNG(w, h int) []byte {
	if w < 8 || h < 8 {
		return nil
	}
	s.ensureFrame(w, h)
	if s.png != nil {
		return s.png
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	img := rgbImage{pix: s.fb.Pixels, w: w, h: h}
	if err := enc.Encode(&buf, img); err != nil {
		s.hasCache = false
		return nil
	}
	s.png = buf.Bytes()
	return s.png
}

func (s *State) renderTitleScreen() {
	clearFB(&s.fb, Rgb{7, 7, 9})
	s.fire.Draw(&s.fb, 0.62)
	h := int32(s.fb.H)
	titleScale := clampInt(s.fb.W/36, 2, 10)
	smallScale := titleScale / 3
	if smallScale < 1 {
		smallScale = 1
	}
	drawTextCenteredOutlined(&s.fb, "GBOOM", h/6, titleScale, GboomRed, textOutline)
	drawTextCenteredOutlined(&s.fb, "KNEE-DEEP IN THE TOKENS", h/6+int32(8*titleScale), smallScale, Rgb{212, 168, 92}, textOutline)
	if uint32(s.phaseTime*1.6)%2 == 0 {
		drawTextCenteredOutlined(&s.fb, "PRESS ANY KEY", h/6+int32(8*titleScale)+int32(10*smallScale), smallScale, Rgb{220, 210, 190}, textOutline)
	}
}

func (s *State) renderEndScreen(text string, color Rgb) {
	clearFB(&s.fb, Rgb{7, 7, 9})
	s.fire.Draw(&s.fb, 0.5)
	h := int32(s.fb.H)
	scale := clampInt(s.fb.W/40, 2, 8)
	drawTextCenteredOutlined(&s.fb, text, h/5, scale, color, textOutline)
	if s.phaseTime > EndScreenGrace && uint32(s.phaseTime*1.6)%2 == 0 {
		small := scale / 3
		if small < 1 {
			small = 1
		}
		drawTextCenteredOutlined(&s.fb, "PRESS ANY KEY", h/5+int32(10*scale), small, Rgb{220, 210, 190}, textOutline)
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

type rgbImage struct {
	pix  []byte
	w, h int
}

func (img rgbImage) ColorModel() color.Model { return color.RGBAModel }
func (img rgbImage) Bounds() image.Rectangle { return image.Rect(0, 0, img.w, img.h) }
func (img rgbImage) At(x, y int) color.Color {
	i := (y*img.w + x) * 3
	if i+2 >= len(img.pix) {
		return color.RGBA{}
	}
	return color.RGBA{img.pix[i], img.pix[i+1], img.pix[i+2], 255}
}
