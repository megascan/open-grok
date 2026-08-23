package gboom

import "math"

const (
	playerRadius = 0.20
	impRadius    = 0.30

	moveSpeed     = 3.3
	turnSpeed     = 2.2
	moveAccelTau  = 0.08
	turnAccelTau  = 0.07
	holdWindow    = 0.16
	playerMaxHP   = 100
	fireCooldown  = 0.32
	muzzleTime    = 0.09
	hitWidth      = 0.33
	pistolDamage  = 11
	impHP         = 30
	impSpeed      = 1.55
	impSightRange = 9.0
	impMeleeRange = 0.95
	impWindup     = 0.38
	impAttackCD   = 0.95
	impPainTime   = 0.28
	impDeathTime  = 0.55
	impBiteDamage = 7
	controlCount  = 6
)

var mapArt = []string{
	"1111111111111111111111",
	"1P...1....2......2...1",
	"1....1.22.2.3333.2.I.1",
	"1....1.2..2.3..3.2...1",
	"1.11.1.2.I2.3.I3.222.1",
	"1.1..1.2..2.33.3...2.1",
	"1.1..1.22.2....3.2.2.1",
	"1.1......2..33.3.2...1",
	"1.111111.2.I3..32222.1",
	"1......1.2..3333.....1",
	"144444.1.2........11.1",
	"1....4.1.22222222..1.1",
	"1.I..4.1........2.I1.1",
	"1....4.11111111.2..1.1",
	"1.4444.......41.2222.1",
	"1.4..444444..41......1",
	"1.4.I......I.41.111111",
	"1.4..444444..4....I..1",
	"1.444........4.11....1",
	"1...44444444.4.1..1111",
	"1............4.1.....1",
	"1111111111111111111111",
}

type Map struct {
	W, H  int
	cells []uint8
}

func (m *Map) Cell(x, y int32) uint8 {
	if x < 0 || y < 0 || int(x) >= m.W || int(y) >= m.H {
		return 1
	}
	return m.cells[int(y)*m.W+int(x)]
}

func (m *Map) Solid(x, y int32) bool { return m.Cell(x, y) != 0 }

func (m *Map) blocked(x, y, radius float32) bool {
	minX := int32(math.Floor(float64(x - radius)))
	maxX := int32(math.Floor(float64(x + radius)))
	minY := int32(math.Floor(float64(y - radius)))
	maxY := int32(math.Floor(float64(y + radius)))
	for cy := minY; cy <= maxY; cy++ {
		for cx := minX; cx <= maxX; cx++ {
			if m.Solid(cx, cy) {
				return true
			}
		}
	}
	return false
}

func (m *Map) LOS(x0, y0, x1, y1 float32) bool {
	dx := x1 - x0
	dy := y1 - y0
	dist := float32(math.Sqrt(float64(dx*dx + dy*dy)))
	if dist < 1e-4 {
		return true
	}
	rdx, rdy := dx/dist, dy/dist
	mapX := int32(math.Floor(float64(x0)))
	mapY := int32(math.Floor(float64(y0)))
	deltaX := float32(math.MaxFloat32)
	if rdx != 0 {
		deltaX = float32(math.Abs(1 / float64(rdx)))
	}
	deltaY := float32(math.MaxFloat32)
	if rdy != 0 {
		deltaY = float32(math.Abs(1 / float64(rdy)))
	}
	var stepX int32
	var sideX float32
	if rdx < 0 {
		stepX = -1
		sideX = (x0 - float32(mapX)) * deltaX
	} else {
		stepX = 1
		sideX = (float32(mapX) + 1 - x0) * deltaX
	}
	var stepY int32
	var sideY float32
	if rdy < 0 {
		stepY = -1
		sideY = (y0 - float32(mapY)) * deltaY
	} else {
		stepY = 1
		sideY = (float32(mapY) + 1 - y0) * deltaY
	}
	for {
		var travelled float32
		if sideX < sideY {
			mapX += stepX
			travelled = sideX
			sideX += deltaX
		} else {
			mapY += stepY
			travelled = sideY
			sideY += deltaY
		}
		if travelled >= dist {
			return true
		}
		if m.Solid(mapX, mapY) {
			return false
		}
	}
}

type Player struct {
	X, Y, Angle                       float32
	VelForward, VelStrafe, VelRot     float32
	HP                                int32
	FireCooldown, Muzzle, DamageFlash float32
	Bob                               float32
}

func (p *Player) Dir() (float32, float32) {
	return float32(math.Cos(float64(p.Angle))), float32(math.Sin(float64(p.Angle)))
}

type Control int

const (
	ControlForward Control = iota
	ControlBack
	ControlTurnLeft
	ControlTurnRight
	ControlStrafeLeft
	ControlStrafeRight
)

type ImpStateKind int

const (
	ImpIdle ImpStateKind = iota
	ImpChasing
	ImpAttacking
	ImpPain
	ImpDying
	ImpDead
)

type ImpState struct {
	Kind ImpStateKind
	T    float32
}

type ImpVisual int

const (
	VisualWalkA ImpVisual = iota
	VisualWalkB
	VisualAttack
	VisualPain
	VisualDieA
	VisualDieB
	VisualCorpse
)

type Imp struct {
	X, Y           float32
	HP             int32
	State          ImpState
	anim           float32
	attackCooldown float32
}

func (imp Imp) Alive() bool {
	return imp.State.Kind != ImpDying && imp.State.Kind != ImpDead
}

func (imp Imp) WalkBob() (float32, bool) {
	if imp.State.Kind != ImpChasing {
		return 0, false
	}
	return float32(math.Sin(float64(imp.anim * 9))), true
}

func (imp Imp) Visual() ImpVisual {
	switch imp.State.Kind {
	case ImpIdle:
		return VisualWalkA
	case ImpChasing:
		if uint32(imp.anim*3)%2 == 0 {
			return VisualWalkA
		}
		return VisualWalkB
	case ImpAttacking:
		return VisualAttack
	case ImpPain:
		return VisualPain
	case ImpDying:
		if imp.State.T > impDeathTime*0.5 {
			return VisualDieA
		}
		return VisualDieB
	default:
		return VisualCorpse
	}
}

type Game struct {
	Map          Map
	Player       Player
	Imps         []Imp
	Kills        uint32
	Time         float32
	hold         [controlCount]float32
	releaseAware bool
	fireQueued   bool
	rng          XorShift64
}

func NewGame() *Game {
	h := len(mapArt)
	w := len(mapArt[0])
	cells := make([]uint8, w*h)
	playerStart := [2]float32{1.5, 1.5}
	var imps []Imp
	for y, row := range mapArt {
		for x := 0; x < len(row); x++ {
			ch := row[x]
			cx, cy := float32(x)+0.5, float32(y)+0.5
			switch {
			case ch >= '1' && ch <= '4':
				cells[y*w+x] = ch - '0'
			case ch == 'P':
				playerStart = [2]float32{cx, cy}
			case ch == 'I':
				imps = append(imps, Imp{
					X: cx, Y: cy, HP: impHP,
					State: ImpState{Kind: ImpIdle},
					anim:  float32(x*7+y*13) * 0.1,
				})
			}
		}
	}
	return &Game{
		Map: Map{W: w, H: h, cells: cells},
		Player: Player{
			X: playerStart[0], Y: playerStart[1],
			HP: playerMaxHP,
		},
		Imps: imps,
		rng:  NewXorShift64(0x9E3779B97F4A7C15),
	}
}

func (g *Game) SetReleaseAware(v bool) { g.releaseAware = v }
func (g *Game) TotalImps() uint32      { return uint32(len(g.Imps)) }
func (g *Game) Won() bool              { return g.Kills == g.TotalImps() }
func (g *Game) Dead() bool             { return g.Player.HP <= 0 }

func (g *Game) Press(c Control) {
	if g.releaseAware {
		g.hold[c] = float32(math.Inf(1))
	} else {
		g.hold[c] = holdWindow
	}
}

func (g *Game) Release(c Control) { g.hold[c] = 0 }
func (g *Game) ReleaseAll() {
	for i := range g.hold {
		g.hold[i] = 0
	}
}

func (g *Game) AnyHeld() bool {
	for _, h := range g.hold {
		if h > 0 {
			return true
		}
	}
	return false
}

func (g *Game) QueueFire() { g.fireQueued = true }

func (g *Game) Step(dt float32) {
	g.Time += dt
	g.stepPlayer(dt)
	if g.fireQueued {
		g.fireQueued = false
		g.tryFire()
	}
	g.stepImps(dt)
}

func (g *Game) stepPlayer(dt float32) {
	for i := range g.hold {
		g.hold[i] = max32(g.hold[i]-dt, 0)
	}
	held := func(c Control) bool { return g.hold[c] > 0 }
	axis := func(pos, neg Control) float32 {
		v := 0
		if held(pos) {
			v++
		}
		if held(neg) {
			v--
		}
		return float32(v)
	}
	fwd := axis(ControlForward, ControlBack)
	strafe := axis(ControlStrafeRight, ControlStrafeLeft)
	mag := float32(math.Sqrt(float64(fwd*fwd + strafe*strafe)))
	if mag > 1 {
		fwd /= mag
		strafe /= mag
	}
	targetForward := fwd * moveSpeed
	targetStrafe := strafe * moveSpeed
	targetRot := axis(ControlTurnRight, ControlTurnLeft) * turnSpeed
	moveBlend := 1 - float32(math.Exp(float64(-dt/moveAccelTau)))
	turnBlend := 1 - float32(math.Exp(float64(-dt/turnAccelTau)))
	p := &g.Player
	p.VelForward += (targetForward - p.VelForward) * moveBlend
	p.VelStrafe += (targetStrafe - p.VelStrafe) * moveBlend
	p.VelRot += (targetRot - p.VelRot) * turnBlend
	p.Angle += p.VelRot * dt
	dx, dy := p.Dir()
	sx, sy := -dy, dx
	stepX := (dx*p.VelForward + sx*p.VelStrafe) * dt
	stepY := (dy*p.VelForward + sy*p.VelStrafe) * dt
	if !g.Map.blocked(p.X+stepX, p.Y, playerRadius) {
		p.X += stepX
	}
	if !g.Map.blocked(p.X, p.Y+stepY, playerRadius) {
		p.Y += stepY
	}
	p.Bob += (abs32(p.VelForward) + abs32(p.VelStrafe)) * dt
	p.FireCooldown = max32(p.FireCooldown-dt, 0)
	p.Muzzle = max32(p.Muzzle-dt, 0)
	p.DamageFlash = max32(p.DamageFlash-dt*1.8, 0)
}

func (g *Game) TargetInCrosshair() (int, bool) {
	px, py := g.Player.X, g.Player.Y
	dx, dy := g.Player.Dir()
	bestI, bestD := -1, float32(0)
	for i, imp := range g.Imps {
		if !imp.Alive() {
			continue
		}
		rx, ry := imp.X-px, imp.Y-py
		along := rx*dx + ry*dy
		if along <= 0 {
			continue
		}
		perp := abs32(rx*dy - ry*dx)
		if perp > hitWidth {
			continue
		}
		if !g.Map.LOS(px, py, imp.X, imp.Y) {
			continue
		}
		if bestI < 0 || along < bestD {
			bestI, bestD = i, along
		}
	}
	return bestI, bestI >= 0
}

func (g *Game) tryFire() {
	if g.Player.FireCooldown > 0 {
		return
	}
	g.Player.FireCooldown = fireCooldown
	g.Player.Muzzle = muzzleTime
	i, ok := g.TargetInCrosshair()
	if !ok {
		return
	}
	damage := pistolDamage + int32(g.rng.NextF32()*7)
	imp := &g.Imps[i]
	imp.HP -= damage
	if imp.HP <= 0 {
		imp.State = ImpState{Kind: ImpDying, T: impDeathTime}
		g.Kills++
	} else {
		imp.State = ImpState{Kind: ImpPain, T: impPainTime}
	}
}

func (g *Game) stepImps(dt float32) {
	px, py := g.Player.X, g.Player.Y
	var playerDamage int32
	for i := range g.Imps {
		imp := &g.Imps[i]
		if imp.State.Kind == ImpDead {
			continue
		}
		ix, iy := imp.X, imp.Y
		dist := float32(math.Sqrt(float64((px-ix)*(px-ix) + (py-iy)*(py-iy))))
		switch imp.State.Kind {
		case ImpIdle:
			if dist < impSightRange && g.Map.LOS(ix, iy, px, py) {
				imp.State = ImpState{Kind: ImpChasing}
			}
		case ImpChasing:
			imp.attackCooldown = max32(imp.attackCooldown-dt, 0)
			if dist < impMeleeRange {
				if imp.attackCooldown <= 0 {
					imp.State = ImpState{Kind: ImpAttacking, T: impWindup}
				}
			} else {
				imp.anim += dt
				g.chaseStep(i, px, py, dist, dt)
			}
		case ImpAttacking:
			t := imp.State.T - dt
			if t <= 0 {
				imp.State = ImpState{Kind: ImpChasing}
				imp.attackCooldown = impAttackCD
				if dist < impMeleeRange*1.25 {
					playerDamage += impBiteDamage + int32(g.rng.NextF32()*5)
				}
			} else {
				imp.State.T = t
			}
		case ImpPain:
			t := imp.State.T - dt
			if t <= 0 {
				imp.State = ImpState{Kind: ImpChasing}
			} else {
				imp.State.T = t
			}
		case ImpDying:
			t := imp.State.T - dt
			if t <= 0 {
				imp.State = ImpState{Kind: ImpDead}
			} else {
				imp.State.T = t
			}
		}
	}
	if playerDamage > 0 {
		g.Player.HP = max32i(g.Player.HP-playerDamage, 0)
		g.Player.DamageFlash = 1
	}
}

func (g *Game) chaseStep(i int, px, py, dist, dt float32) {
	ix, iy := g.Imps[i].X, g.Imps[i].Y
	mx, my := (px-ix)/dist, (py-iy)/dist
	for j, other := range g.Imps {
		if i == j || !other.Alive() {
			continue
		}
		ox, oy := ix-other.X, iy-other.Y
		d2 := ox*ox + oy*oy
		if d2 < 0.49 && d2 > 1e-6 {
			d := float32(math.Sqrt(float64(d2)))
			mx += (ox / d) * 0.6
			my += (oy / d) * 0.6
		}
	}
	mag := float32(math.Sqrt(float64(mx*mx + my*my)))
	if mag < 1e-4 {
		mag = 1e-4
	}
	step := impSpeed * dt
	sx, sy := mx/mag*step, my/mag*step
	imp := &g.Imps[i]
	if !g.Map.blocked(imp.X+sx, imp.Y, impRadius) {
		imp.X += sx
	}
	if !g.Map.blocked(imp.X, imp.Y+sy, impRadius) {
		imp.Y += sy
	}
}

func max32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func max32i(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func abs32(a float32) float32 {
	if a < 0 {
		return -a
	}
	return a
}
