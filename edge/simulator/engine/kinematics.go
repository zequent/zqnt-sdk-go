package engine

import "math"

// Pure kinematics: linear interpolation toward a target position over elapsed time, using a flat
// local-tangent-plane approximation (fine at the scale a single simulated flight covers -- no real
// flight-dynamics model, per the simulator's own scope).
const metersPerDegLat = 111320.0

func metersPerDegLon(atLat float64) float64 {
	return metersPerDegLat * math.Cos(atLat*math.Pi/180)
}

// Position is a simulated device's geographic position.
type Position struct {
	Lat, Lon, Alt float64 // degrees, degrees, meters
}

// moveToward advances pos toward target by at most maxHorizM horizontally and maxVertM
// vertically, and reports whether target was reached this step.
func moveToward(pos, target Position, maxHorizM, maxVertM float64) (Position, bool) {
	dLat := target.Lat - pos.Lat
	dLon := target.Lon - pos.Lon
	dyM := dLat * metersPerDegLat
	dxM := dLon * metersPerDegLon(pos.Lat)
	horizDist := math.Hypot(dxM, dyM)
	vertDist := target.Alt - pos.Alt

	next := pos
	arrivedHoriz, arrivedVert := true, true

	const epsilonM = 0.25
	if horizDist > epsilonM {
		if horizDist <= maxHorizM || maxHorizM <= 0 {
			next.Lat, next.Lon = target.Lat, target.Lon
		} else {
			frac := maxHorizM / horizDist
			next.Lat = pos.Lat + dLat*frac
			next.Lon = pos.Lon + dLon*frac
			arrivedHoriz = false
		}
	}
	if math.Abs(vertDist) > epsilonM {
		if math.Abs(vertDist) <= maxVertM || maxVertM <= 0 {
			next.Alt = target.Alt
		} else {
			step := maxVertM
			if vertDist < 0 {
				step = -step
			}
			next.Alt = pos.Alt + step
			arrivedVert = false
		}
	}
	return next, arrivedHoriz && arrivedVert
}

// headingTo returns the compass bearing (degrees, 0-360, 0=north) from one position to another.
func headingTo(from, to Position) float64 {
	dyM := (to.Lat - from.Lat) * metersPerDegLat
	dxM := (to.Lon - from.Lon) * metersPerDegLon(from.Lat)
	if dxM == 0 && dyM == 0 {
		return 0
	}
	deg := math.Atan2(dxM, dyM) * 180 / math.Pi
	if deg < 0 {
		deg += 360
	}
	return deg
}
