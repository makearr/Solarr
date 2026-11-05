package httpx

// lane is one of the three queues a request waits in; each is guaranteed its share when all are waiting, and
// a share nobody waits for goes to the others (user, 2026-11-05).
type lane int

const (
	laneUser lane = iota
	laneFresh
	laneBackground
	laneCount
)

// LaneShares are the lanes' relative weights: of a contended resource each gets its weight over the sum of the
// weights of the lanes waiting, so a user hammering the interface still leaves the others their part.
type LaneShares struct{ User, Fresh, Background float64 }

// DefaultLaneShares is 70/20/10; a person sets their own (Settings -> Priority).
var DefaultLaneShares = LaneShares{User: 70, Fresh: 20, Background: 10}

func (s LaneShares) of(l lane) float64 {
	switch l {
	case laneUser:
		return s.User
	case laneFresh:
		return s.Fresh
	default:
		return s.Background
	}
}

// sharesFunc reads the shares at every decision so a change applies at once; nil is the default.
type sharesFunc func() LaneShares

func (f sharesFunc) read() LaneShares {
	if f == nil {
		return DefaultLaneShares
	}
	return f()
}

// laneOf maps a priority onto its lane; the finer values of a caller's own ladder rank within a lane.
func laneOf(priority Priority) lane {
	switch {
	case priority <= PriorityUser:
		return laneUser
	case priority <= PriorityFresh:
		return laneFresh
	default:
		return laneBackground
	}
}

// laneTurns is smooth weighted round robin over the lanes that are waiting, so an idle lane costs the others nothing.
type laneTurns struct {
	credit [laneCount]float64
}

// next answers the lane whose turn it is among those waiting, without taking the turn.
func (t *laneTurns) next(shares LaneShares, waiting [laneCount]bool) (lane, bool) {
	best, found := laneUser, false
	var bestCredit float64
	for candidate := range laneCount {
		if !waiting[candidate] {
			continue
		}
		if credit := t.credit[candidate] + shares.of(candidate); !found || credit > bestCredit {
			best, bestCredit, found = candidate, credit, true
		}
	}
	return best, found
}

// took records that served used the turn.
func (t *laneTurns) took(shares LaneShares, waiting [laneCount]bool, served lane) {
	var total float64
	for candidate := range laneCount {
		if waiting[candidate] {
			t.credit[candidate] += shares.of(candidate)
			total += shares.of(candidate)
		}
	}
	t.credit[served] -= total
}
