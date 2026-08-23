package gboom

import (
	"bytes"
	"image/png"
	"math"
	"testing"
)

func TestStartsOnTitleAndAnyKeyStartsGame(t *testing.T) {
	state := NewState()
	if state.Phase() != PhaseTitle {
		t.Fatal(state.Phase())
	}
	if state.HandleKey("w") != KeyChanged {
		t.Fatal("any key should start")
	}
	if state.Phase() != PhasePlaying {
		t.Fatal(state.Phase())
	}
}

func TestEscAndQCloseInEveryPhase(t *testing.T) {
	for _, phase := range []Phase{PhaseTitle, PhasePlaying, PhaseWon, PhaseDead} {
		for _, key := range []string{"esc", "q"} {
			state := NewState()
			state.phase = phase
			if state.HandleKey(key) != KeyClose {
				t.Fatalf("phase=%v key=%q", phase, key)
			}
		}
	}
}

func TestEndScreensHaveDismissalGracePeriod(t *testing.T) {
	state := NewState()
	state.phase = PhaseDead
	state.phaseTime = 0.1
	if state.HandleKey(" ") != KeyChanged {
		t.Fatal("within grace, non-quit keys are swallowed")
	}
	state.phaseTime = EndScreenGrace + 0.1
	if state.HandleKey(" ") != KeyClose {
		t.Fatal("after grace, any key closes")
	}
}

func TestFramePNGProducesValidPNGAtRequestedSize(t *testing.T) {
	state := NewState()
	state.Tick()
	raw := state.FramePNG(320, 200)
	if raw == nil {
		t.Fatal("png frame")
	}
	if !bytes.Equal(raw[:8], []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("png signature")
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 320 || cfg.Height != 200 {
		t.Fatalf("dims %dx%d", cfg.Width, cfg.Height)
	}
}

func TestFramePNGIsCachedUntilStateChanges(t *testing.T) {
	state := NewState()
	state.Tick()
	a := append([]byte(nil), state.FramePNG(160, 100)...)
	b := append([]byte(nil), state.FramePNG(160, 100)...)
	if !bytes.Equal(a, b) {
		t.Fatal("same gen + dims must reuse the cached frame")
	}
	state.Tick()
	c := state.FramePNG(160, 100)
	if bytes.Equal(a, c) {
		t.Fatal("tick must invalidate the cached frame")
	}
}

func TestFrameSizeMappingRespectsCapsAndAspect(t *testing.T) {
	w, h := FrameSizeForCells(300, 80)
	if w > maxFrameW || h > maxFrameH {
		t.Fatalf("capped %dx%d", w, h)
	}
	w, h = FrameSizeForCells(60, 15)
	got := math.Round(float64(w) / float64(h) * 10)
	want := math.Round((60 * float64(cellPxW)) / (15 * float64(cellPxH)) * 10)
	if got != want {
		t.Fatalf("aspect got %v want %v", got, want)
	}
	w, h = FrameSizeForCells(1, 1)
	if w < 64 || h < 64 {
		t.Fatalf("floor %dx%d", w, h)
	}
}

func TestPlayingToDeadTransition(t *testing.T) {
	state := NewState()
	state.HandleKey("w")
	state.game.Player.HP = 0
	state.Tick()
	if state.Phase() != PhaseDead {
		t.Fatal(state.Phase())
	}
}

func TestPlayingToWonTransitionAfterCorpsesSettle(t *testing.T) {
	state := NewState()
	state.HandleKey("w")
	total := state.game.TotalImps()
	for i := range state.game.Imps {
		state.game.Imps[i].State = ImpState{Kind: ImpDead}
	}
	state.game.Kills = total
	state.Tick()
	if state.Phase() != PhaseWon {
		t.Fatal(state.Phase())
	}
}

func playingWithRegion() *State {
	state := NewState()
	state.HandleKey("w")
	state.SetMouseRegion(0, 0, 200, 50)
	return state
}

func TestMouseAimDeltaMatchesColumnDelta(t *testing.T) {
	state := playingWithRegion()
	state.game.Player.Angle = 0
	state.HandleMouse(MouseMoved, 40, 10)
	if state.game.Player.Angle != 0 {
		t.Fatal("first event seeds only")
	}
	state.HandleMouse(MouseMoved, 45, 10)
	if state.game.Player.Angle != 5*mouseAimSensitivity {
		t.Fatalf("angle %v", state.game.Player.Angle)
	}
	state.HandleMouse(MouseMoved, 42, 10)
	if abs32(state.game.Player.Angle-2*mouseAimSensitivity) >= 1e-5 {
		t.Fatalf("angle %v", state.game.Player.Angle)
	}
}

func TestMouseAimOnlyWhilePlaying(t *testing.T) {
	for _, phase := range []Phase{PhaseTitle, PhaseWon, PhaseDead} {
		state := NewState()
		state.SetMouseRegion(0, 0, 200, 50)
		state.phase = phase
		state.game.Player.Angle = 0.5
		state.HandleMouse(MouseMoved, 10, 10)
		state.HandleMouse(MouseMoved, 50, 10)
		if state.game.Player.Angle != 0.5 {
			t.Fatalf("phase %v aimed", phase)
		}
	}
}

func TestMouseAimBaselineReseedsOnDiscontinuity(t *testing.T) {
	state := playingWithRegion()
	state.game.Player.Angle = 0
	state.HandleMouse(MouseMoved, 10, 10)
	state.HandleMouse(MouseMoved, 12, 10)
	after := state.game.Player.Angle
	if after != 2*mouseAimSensitivity {
		t.Fatalf("after %v", after)
	}
	state.ReleaseAll()
	state.HandleMouse(MouseMoved, 50, 10)
	if state.game.Player.Angle != after {
		t.Fatal("reseed must not apply the gap")
	}
	state.HandleMouse(MouseMoved, 52, 10)
	if state.game.Player.Angle != after+2*mouseAimSensitivity {
		t.Fatalf("angle %v", state.game.Player.Angle)
	}

	state.game.Player.Angle = 0
	state.lastMouseCol = nil
	state.HandleMouse(MouseMoved, 10, 10)
	state.HandleMouse(MouseMoved, 10+maxMouseAimDX+1, 10)
	if state.game.Player.Angle != 0 {
		t.Fatal("teleport dx must reseed")
	}
	state.HandleMouse(MouseMoved, 10+maxMouseAimDX+1+3, 10)
	if state.game.Player.Angle != 3*mouseAimSensitivity {
		t.Fatalf("angle %v", state.game.Player.Angle)
	}
}

func TestOutOfRegionMotionDoesNotAim(t *testing.T) {
	state := playingWithRegion()
	state.SetMouseRegion(20, 5, 20, 10)
	state.game.Player.Angle = 0
	state.HandleMouse(MouseMoved, 25, 10)
	state.HandleMouse(MouseMoved, 30, 10)
	angle := state.game.Player.Angle
	if angle != 5*mouseAimSensitivity {
		t.Fatalf("angle %v", angle)
	}
	state.HandleMouse(MouseMoved, 100, 10)
	if state.game.Player.Angle != angle {
		t.Fatal("oob yaw")
	}
	state.HandleMouse(MouseMoved, 22, 10)
	if state.game.Player.Angle != angle {
		t.Fatal("re-entry must not apply OOB span")
	}
	state.HandleMouse(MouseMoved, 24, 10)
	if state.game.Player.Angle != angle+2*mouseAimSensitivity {
		t.Fatalf("angle %v", state.game.Player.Angle)
	}
}

func TestClickFiresOnlyPlayingInRegion(t *testing.T) {
	state := playingWithRegion()
	state.HandleMouse(MouseClick, 40, 10)
	state.Tick()
	if state.game.Player.Muzzle <= 0 {
		t.Fatal("in-region click must fire")
	}

	state = NewState()
	state.SetMouseRegion(0, 0, 200, 50)
	state.HandleMouse(MouseClick, 40, 10)
	state.Tick()
	if state.game.Player.Muzzle != 0 {
		t.Fatal("title click must not fire")
	}
	state.HandleKey("w")
	state.HandleMouse(MouseClick, 250, 10)
	state.Tick()
	if state.game.Player.Muzzle != 0 {
		t.Fatal("out-of-region click must not fire")
	}
	for _, phase := range []Phase{PhaseWon, PhaseDead} {
		state = NewState()
		state.SetMouseRegion(0, 0, 200, 50)
		state.phase = phase
		state.HandleMouse(MouseClick, 40, 10)
		state.Tick()
		if state.game.Player.Muzzle != 0 {
			t.Fatalf("phase %v click fired", phase)
		}
	}
}

func TestClearMouseRegionDisablesAimAndFire(t *testing.T) {
	state := playingWithRegion()
	state.game.Player.Angle = 0
	state.HandleMouse(MouseMoved, 40, 10)
	state.HandleMouse(MouseMoved, 45, 10)
	angle := state.game.Player.Angle
	state.ClearMouseRegion()
	state.HandleMouse(MouseMoved, 50, 10)
	state.HandleMouse(MouseMoved, 55, 10)
	if state.game.Player.Angle != angle {
		t.Fatal("cleared region still aimed")
	}
	state.HandleMouse(MouseClick, 40, 10)
	state.Tick()
	if state.game.Player.Muzzle != 0 {
		t.Fatal("cleared region still fired")
	}
}
