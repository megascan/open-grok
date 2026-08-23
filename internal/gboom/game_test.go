package gboom

import (
	"math"
	"testing"
)

func TestAllImpSpawnsReachableFromPlayerStart(t *testing.T) {
	game := NewGame()
	if game.TotalImps() < 5 {
		t.Fatalf("want a meaningful demon count, got %d", game.TotalImps())
	}
	w, h := game.Map.W, game.Map.H
	start := [2]int32{int32(math.Floor(float64(game.Player.X))), int32(math.Floor(float64(game.Player.Y)))}
	reachable := make([]bool, w*h)
	stack := [][2]int32{start}
	for len(stack) > 0 {
		x, y := stack[len(stack)-1][0], stack[len(stack)-1][1]
		stack = stack[:len(stack)-1]
		if game.Map.Solid(x, y) || reachable[int(y)*w+int(x)] {
			continue
		}
		reachable[int(y)*w+int(x)] = true
		stack = append(stack,
			[2]int32{x + 1, y}, [2]int32{x - 1, y},
			[2]int32{x, y + 1}, [2]int32{x, y - 1},
		)
	}
	for _, imp := range game.Imps {
		ix, iy := int(math.Floor(float64(imp.X))), int(math.Floor(float64(imp.Y)))
		if !reachable[iy*w+ix] {
			t.Fatalf("imp spawn at (%d, %d) unreachable from player start", ix, iy)
		}
	}
}

func TestMapBorderIsSolid(t *testing.T) {
	game := NewGame()
	for x := int32(0); x < int32(game.Map.W); x++ {
		if !game.Map.Solid(x, 0) || !game.Map.Solid(x, int32(game.Map.H)-1) {
			t.Fatal("top/bottom border must be solid")
		}
	}
	for y := int32(0); y < int32(game.Map.H); y++ {
		if !game.Map.Solid(0, y) || !game.Map.Solid(int32(game.Map.W)-1, y) {
			t.Fatal("left/right border must be solid")
		}
	}
}

func TestPlayerCannotWalkThroughWalls(t *testing.T) {
	game := NewGame()
	game.Player.Angle = -float32(math.Pi / 2)
	for range 600 {
		game.Press(ControlForward)
		game.Step(1.0 / 30.0)
	}
	if game.Player.Y < 1.0+playerRadius-1e-3 {
		t.Fatalf("clipped into border wall: y=%v", game.Player.Y)
	}
}

func TestLOSBlockedByWallsAndOpenInRoom(t *testing.T) {
	game := NewGame()
	px, py := game.Player.X, game.Player.Y
	if game.Map.LOS(px, py, 20.5, 20.5) {
		t.Fatal("opposite corner should be occluded")
	}
	if !game.Map.LOS(px, py, px+1, py+1) {
		t.Fatal("starting room should be visible")
	}
}

func TestShootingAnImpInFrontDamagesAndEventuallyKills(t *testing.T) {
	game := NewGame()
	dx, dy := game.Player.Dir()
	game.Imps[0].X = game.Player.X + dx*2
	game.Imps[0].Y = game.Player.Y + dy*2
	game.Imps[0].State = ImpState{Kind: ImpIdle}
	hpBefore := game.Imps[0].HP
	game.QueueFire()
	game.Step(0.016)
	if game.Imps[0].HP >= hpBefore {
		t.Fatal("first shot must connect")
	}
	for range 20 {
		game.Step(fireCooldown + 0.01)
		game.QueueFire()
		game.Step(0.016)
		if !game.Imps[0].Alive() {
			break
		}
	}
	if game.Imps[0].Alive() {
		t.Fatal("imp should die after repeated hits")
	}
	if game.Kills != 1 {
		t.Fatalf("kills=%d", game.Kills)
	}
}

func TestFireRespectsCooldown(t *testing.T) {
	game := NewGame()
	dx, dy := game.Player.Dir()
	game.Imps[0].X = game.Player.X + dx*2
	game.Imps[0].Y = game.Player.Y + dy*2
	game.QueueFire()
	game.Step(0.016)
	hpAfterFirst := game.Imps[0].HP
	game.QueueFire()
	game.Step(0.016)
	if game.Imps[0].HP != hpAfterFirst {
		t.Fatal("immediate second shot must be swallowed by cooldown")
	}
}

func TestImpChasesAndBitesPlayer(t *testing.T) {
	game := NewGame()
	game.Imps = game.Imps[:1]
	game.Imps[0].X = game.Player.X + 1.5
	game.Imps[0].Y = game.Player.Y
	game.Imps[0].State = ImpState{Kind: ImpIdle}
	bitten := false
	for range 400 {
		game.Step(1.0 / 30.0)
		if game.Player.HP < playerMaxHP {
			bitten = true
			break
		}
	}
	if !bitten {
		t.Fatal("imp should reach and bite the player")
	}
}

func TestWinConditionWhenAllImpsDead(t *testing.T) {
	game := NewGame()
	for i := range game.Imps {
		game.Imps[i].State = ImpState{Kind: ImpDead}
	}
	game.Kills = game.TotalImps()
	if !game.Won() || game.Dead() {
		t.Fatal("expected won, not dead")
	}
}

func TestMovementDecaysToRest(t *testing.T) {
	game := NewGame()
	game.Press(ControlForward)
	game.Press(ControlTurnRight)
	for range 120 {
		game.Step(1.0 / 30.0)
	}
	if abs32(game.Player.VelForward) >= 0.01 || abs32(game.Player.VelRot) >= 0.01 {
		t.Fatalf("should glide to rest: vf=%v vr=%v", game.Player.VelForward, game.Player.VelRot)
	}
}

func TestHeldForwardReachesAndSustainsTargetSpeed(t *testing.T) {
	game := NewGame()
	game.Player.Angle = float32(math.Pi / 2)
	dt := float32(1.0 / 30.0)
	for range 40 {
		game.Press(ControlForward)
		game.Step(dt)
	}
	speed := game.Player.VelForward
	if abs32(speed-moveSpeed) >= 0.05 {
		t.Fatalf("expected ~%v, got %v", moveSpeed, speed)
	}
	lo, hi := float32(math.MaxFloat32), float32(-math.MaxFloat32)
	for range 30 {
		game.Press(ControlForward)
		game.Step(dt)
		if game.Player.VelForward < lo {
			lo = game.Player.VelForward
		}
		if game.Player.VelForward > hi {
			hi = game.Player.VelForward
		}
	}
	if hi-lo >= 0.02 {
		t.Fatalf("velocity sawtoothed: [%v, %v]", lo, hi)
	}
}

func TestReleaseAwareSupportsSimultaneousMoveAndTurn(t *testing.T) {
	game := NewGame()
	game.SetReleaseAware(true)
	game.Player.Angle = 0
	game.Press(ControlForward)
	game.Press(ControlTurnLeft)
	angle0 := game.Player.Angle
	for range 30 {
		game.Step(1.0 / 30.0)
	}
	if game.Player.VelForward <= 1 {
		t.Fatalf("forward must persist without repeats, got %v", game.Player.VelForward)
	}
	if game.Player.Angle >= angle0 {
		t.Fatal("should have turned left while moving")
	}
	game.Release(ControlForward)
	for range 30 {
		game.Step(1.0 / 30.0)
	}
	if abs32(game.Player.VelForward) >= 0.2 {
		t.Fatalf("released forward should glide to rest, got %v", game.Player.VelForward)
	}
	if abs32(game.Player.VelRot) <= 0.1 {
		t.Fatal("turn should still be held")
	}
}

func TestReleaseAllUnLatchesMovement(t *testing.T) {
	game := NewGame()
	game.SetReleaseAware(true)
	game.Press(ControlForward)
	game.Press(ControlTurnLeft)
	game.ReleaseAll()
	for range 30 {
		game.Step(1.0 / 30.0)
	}
	if abs32(game.Player.VelForward) >= 0.1 || abs32(game.Player.VelRot) >= 0.1 {
		t.Fatal("release_all must clear latched movement")
	}
}

func sustainedSpeed(repeatInterval float32) float32 {
	game := NewGame()
	game.Player.Angle = float32(math.Pi / 2)
	dt := float32(1.0 / 60.0)
	since := repeatInterval
	for range 120 {
		since += dt
		if since >= repeatInterval {
			game.Press(ControlForward)
			since = 0
		}
		game.Step(dt)
	}
	return game.Player.VelForward
}

func TestSustainedSpeedIsIndependentOfRepeatCadence(t *testing.T) {
	fast := sustainedSpeed(0.03)
	slow := sustainedSpeed(0.12)
	if abs32(fast-moveSpeed) >= 0.1 {
		t.Fatalf("fast cadence: %v", fast)
	}
	if abs32(fast-slow) >= 0.25 {
		t.Fatalf("cadence changed sustained speed: fast=%v slow=%v", fast, slow)
	}
}
