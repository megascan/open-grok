package gboom

import "testing"

func TestTexturesHaveExpectedSize(t *testing.T) {
	for _, tex := range buildTextures() {
		if len(tex.Pixels) != TexSize*TexSize {
			t.Fatalf("wall tex len %d", len(tex.Pixels))
		}
	}
	if len(buildFloorTexture().Pixels) != TexSize*TexSize {
		t.Fatal("floor")
	}
	if len(buildCeilingTexture().Pixels) != TexSize*TexSize {
		t.Fatal("ceiling")
	}
}

func TestXorShiftIsNondegenerate(t *testing.T) {
	rng := NewXorShift64(0)
	any := false
	for range 4 {
		if rng.NextU32() != 0 {
			any = true
		}
	}
	if !any {
		t.Fatal("zero seed must not collapse")
	}
	rng = NewXorShift64(42)
	for range 1000 {
		f := rng.NextF32()
		if f < 0 || f >= 1 {
			t.Fatalf("float out of range: %v", f)
		}
	}
}

func TestEverySpritePaletteCharIsUsedInArt(t *testing.T) {
	used := map[Rgb]bool{}
	imps := buildImpSprites()
	guns := buildGunSprites()
	for _, s := range []Sprite{
		imps.WalkA, imps.WalkB, imps.Attack, imps.Pain, imps.DieA, imps.DieB, imps.Corpse,
		guns.Idle, guns.Fire,
	} {
		for i, on := range s.mask {
			if on {
				used[s.pixels[i]] = true
			}
		}
	}
	for _, ch := range []byte("BbHEMTCRrGgWSsFf") {
		c, ok := spriteColor(ch)
		if !ok {
			t.Fatalf("palette char %q must map", ch)
		}
		if !used[c] {
			t.Fatalf("palette char %q is mapped but unused in art", ch)
		}
	}
}

func TestSpritesHaveConsistentRows(t *testing.T) {
	imps := buildImpSprites()
	for _, s := range []Sprite{imps.WalkA, imps.WalkB, imps.Attack, imps.Pain, imps.DieA, imps.DieB, imps.Corpse} {
		if s.W != 16 || s.H != 20 {
			t.Fatalf("imp sprite %dx%d", s.W, s.H)
		}
		s.Sample(0, 0)
		s.Sample(0.999, 0.999)
	}
	guns := buildGunSprites()
	if guns.Idle.W != 24 || guns.Fire.W != 24 {
		t.Fatal("gun width")
	}
}

func TestFontCoversRequiredStrings(t *testing.T) {
	for _, text := range []string{
		"GBOOM", "KNEE-DEEP IN THE TOKENS", "PRESS ANY KEY", "YOU DIED", "VICTORY!",
	} {
		for _, ch := range text {
			if ch == ' ' {
				continue
			}
			if glyph5x7(ch) == ([7]uint8{}) {
				t.Fatalf("missing glyph for %q used in %q", ch, text)
			}
		}
	}
}
