// Package routes holds rail segment geometry and distance-based interpolation.
package routes

import (
	"math"
	"sort"

	"livetrack/internal/geo"
	"livetrack/internal/source"
)

// Route is one rail segment polyline with precomputed cumulative distances.
type Route struct {
	Polyline   [][2]float64 // [lat, lon] in canonical order
	Cumulative []float64    // haversine km from point 0 to point i
	TotalKm    float64      // len_cm / 1000 (the source's length, used as interpolation target)
}

// New builds a Route and its cumulative distance table.
func New(polyline [][2]float64, totalKm float64) *Route {
	cum := make([]float64, len(polyline))
	for i := 1; i < len(polyline); i++ {
		p, q := polyline[i-1], polyline[i]
		cum[i] = cum[i-1] + geo.HaversineKm(p[0], p[1], q[0], q[1])
	}
	return &Route{Polyline: polyline, Cumulative: cum, TotalKm: totalKm}
}

// Interpolate returns the point at progress (0..1) along the route, measured by
// distance. ok is false when the route has no geometry.
func (r *Route) Interpolate(progress float64) (point [2]float64, ok bool) {
	n := len(r.Polyline)
	if n == 0 {
		return point, false
	}
	if n == 1 {
		return r.Polyline[0], true
	}
	progress = math.Max(0, math.Min(1, progress))
	target := progress * r.TotalKm

	// Smallest i in [1, n) with target <= cumulative[i]; equivalent to the
	// linear scan in the original implementation because cumulative is monotonic.
	i := 1 + sort.Search(n-1, func(k int) bool { return target <= r.Cumulative[k+1] })
	if i >= n {
		return r.Polyline[n-1], true
	}
	segLen := r.Cumulative[i] - r.Cumulative[i-1]
	t := 0.0
	if segLen > 0 {
		t = (target - r.Cumulative[i-1]) / segLen
	}
	return geo.Lerp(r.Polyline[i-1], r.Polyline[i], t), true
}

// Flatten turns a route's unordered edge list into an ordered polyline.
// Points are keyed by pos_cm rounded to 3 decimals; on duplicate keys the last
// one wins. Sorting by that key yields the spatial order along the track.
func Flatten(edges []source.RouteEdge) [][2]float64 {
	points := make(map[int64][2]float64)
	for _, e := range edges {
		n := min(len(e.Pos), len(e.PosCm))
		for k := 0; k < n; k++ {
			key := int64(math.Round(e.PosCm[k] * 1000))
			points[key] = e.Pos[k]
		}
	}
	keys := make([]int64, 0, len(points))
	for k := range points {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool { return keys[a] < keys[b] })
	out := make([][2]float64, len(keys))
	for i, k := range keys {
		out[i] = points[k]
	}
	return out
}
