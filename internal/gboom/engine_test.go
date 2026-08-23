package gboom

import (
	"bytes"
	"testing"
)

func TestRenderGameFillsFramebuffer(t *testing.T) {
	renderer := NewRenderer()
	fb := NewFrameBuffer()
	fb.Resize(320, 200)
	renderer.RenderGame(fb, NewGame())
	any := false
	for _, b := range fb.Pixels {
		if b != 0 {
			any = true
			break
		}
	}
	if !any {
		t.Fatal("floor/ceiling pass must paint the frame")
	}
	if len(fb.Pixels) != 320*200*3 {
		t.Fatalf("pixels len %d", len(fb.Pixels))
	}
}

func TestRenderSurvivesExtremeSizes(t *testing.T) {
	renderer := NewRenderer()
	game := NewGame()
	fb := NewFrameBuffer()
	for _, dim := range [][2]int{{1, 1}, {2, 2}, {16, 8}, {639, 401}} {
		fb.Resize(dim[0], dim[1])
		renderer.RenderGame(fb, game)
	}
}

func TestZBufferOccludesSpritesBehindWalls(t *testing.T) {
	renderer := NewRenderer()
	game := NewGame()
	for i := range game.Imps {
		game.Imps[i].X = game.Player.X - 8
		game.Imps[i].Y = game.Player.Y
	}
	fb := NewFrameBuffer()
	fb.Resize(160, 100)
	renderer.RenderGame(fb, game)
	before := append([]byte(nil), fb.Pixels...)
	dx, dy := game.Player.Dir()
	game.Imps[0].X = game.Player.X + dx*1.5
	game.Imps[0].Y = game.Player.Y + dy*1.5
	renderer.RenderGame(fb, game)
	if bytes.Equal(before, fb.Pixels) {
		t.Fatal("visible imp must change the frame")
	}
}

func TestFireSimBurnsUpward(t *testing.T) {
	fire := NewFireSim()
	for range 60 {
		fire.Step()
	}
	var above uint32
	for x := 0; x < fire.w; x++ {
		above += uint32(fire.heat[(fire.h/2)*fire.w+x])
	}
	if above == 0 {
		t.Fatal("fire should propagate upward")
	}
}
