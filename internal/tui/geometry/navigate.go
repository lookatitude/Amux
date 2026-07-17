package geometry

// Direction is a spatial focus move.
type Direction int

const (
	Left Direction = iota
	Right
	Up
	Down
)

func (d Direction) String() string {
	switch d {
	case Left:
		return "left"
	case Right:
		return "right"
	case Up:
		return "up"
	default:
		return "down"
	}
}

// Neighbour returns the pane id spatially adjacent to `from` in direction d, or
// ("", false) when there is none. Selection is deterministic: among panes that
// lie in the requested direction it prefers those overlapping `from` on the
// perpendicular axis, then the nearest by primary-axis edge distance, then the
// nearest perpendicular centre, then the lexicographically smaller id. When no
// pane overlaps, it falls back to the directional pane nearest by centre so a
// move never silently no-ops when a target exists.
func (l Layout) Neighbour(from string, d Direction) (string, bool) {
	fl, ok := l.Pane(from)
	if !ok {
		return "", false
	}
	f := fl.Outer

	type cand struct {
		id          string
		overlap     bool
		primaryDist int
		perpDist    int
	}
	var best *cand
	consider := func(c cand) {
		if best == nil || better(c.overlap, c.primaryDist, c.perpDist, c.id,
			best.overlap, best.primaryDist, best.perpDist, best.id) {
			cc := c
			best = &cc
		}
	}

	for _, pl := range l.Panes {
		if pl.PaneID == from {
			continue
		}
		r := pl.Outer
		var inDir, overlap bool
		var primaryDist, perpDist int
		switch d {
		case Left:
			inDir = r.CenterX() < f.CenterX()
			primaryDist = f.X - r.Right()
			overlap = rangesOverlap(r.Y, r.Bottom(), f.Y, f.Bottom())
			perpDist = absInt(r.CenterY() - f.CenterY())
		case Right:
			inDir = r.CenterX() > f.CenterX()
			primaryDist = r.X - f.Right()
			overlap = rangesOverlap(r.Y, r.Bottom(), f.Y, f.Bottom())
			perpDist = absInt(r.CenterY() - f.CenterY())
		case Up:
			inDir = r.CenterY() < f.CenterY()
			primaryDist = f.Y - r.Bottom()
			overlap = rangesOverlap(r.X, r.Right(), f.X, f.Right())
			perpDist = absInt(r.CenterX() - f.CenterX())
		case Down:
			inDir = r.CenterY() > f.CenterY()
			primaryDist = r.Y - f.Bottom()
			overlap = rangesOverlap(r.X, r.Right(), f.X, f.Right())
			perpDist = absInt(r.CenterX() - f.CenterX())
		}
		if !inDir {
			continue
		}
		if primaryDist < 0 {
			primaryDist = 0
		}
		consider(cand{id: pl.PaneID, overlap: overlap, primaryDist: primaryDist, perpDist: perpDist})
	}
	if best == nil {
		return "", false
	}
	return best.id, true
}

// better reports whether candidate A is preferred over candidate B under the
// neighbour ordering (overlap first, then nearest primary edge, then nearest
// perpendicular centre, then smaller id).
func better(aOverlap bool, aPrim, aPerp int, aID string, bOverlap bool, bPrim, bPerp int, bID string) bool {
	if aOverlap != bOverlap {
		return aOverlap
	}
	if aPrim != bPrim {
		return aPrim < bPrim
	}
	if aPerp != bPerp {
		return aPerp < bPerp
	}
	return aID < bID
}

// rangesOverlap reports whether half-open [a0,a1) and [b0,b1) share any cell.
func rangesOverlap(a0, a1, b0, b1 int) bool {
	lo := a0
	if b0 > lo {
		lo = b0
	}
	hi := a1
	if b1 < hi {
		hi = b1
	}
	return lo < hi
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
