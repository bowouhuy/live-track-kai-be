package routes

import (
	"math"
	"testing"

	"livetrack/internal/source"
)

func TestFlattenOrdersByPosCmAndLastWins(t *testing.T) {
	edges := []source.RouteEdge{
		{Pos: [][2]float64{{3, 3}, {2, 2}}, PosCm: []float64{300, 200}},
		{Pos: [][2]float64{{0, 0}, {1, 1}}, PosCm: []float64{0, 100.0001}},
		{Pos: [][2]float64{{1.5, 1.5}, {2.5, 2.5}}, PosCm: []float64{100.0004, 200}}, // same key as 100.0001 -> last wins
	}
	got := Flatten(edges)
	want := [][2]float64{{0, 0}, {1.5, 1.5}, {2.5, 2.5}, {3, 3}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestInterpolateByDistance(t *testing.T) {
	// Uneven spacing: point 1 is very close to point 0.
	poly := [][2]float64{{0, 0}, {0, 0.1}, {0, 1}}
	tmp := New(poly, 0)
	r := New(poly, tmp.Cumulative[2])

	p, ok := r.Interpolate(0.5)
	if !ok || math.Abs(p[1]-0.5) > 1e-6 {
		t.Fatalf("got %v", p)
	}
	if p, _ := r.Interpolate(-1); p != poly[0] {
		t.Fatalf("clamp low %v", p)
	}
	if p, _ := r.Interpolate(2); p != poly[2] {
		t.Fatalf("clamp high %v", p)
	}
	if _, ok := New(nil, 1).Interpolate(0.5); ok {
		t.Fatal("empty route must not interpolate")
	}
	if p, _ := New(poly[:1], 0).Interpolate(0.5); p != poly[0] {
		t.Fatal("single-point route")
	}
}
