package position

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"livetrack/internal/dataset"
	"livetrack/internal/routes"
)

// Fixture from docs/backend.md §10.
func fixture(t *testing.T, schedules ...*dataset.Schedule) *dataset.Dataset {
	t.Helper()
	r1 := [][2]float64{{-6.0, 106.0}, {-6.5, 106.5}, {-7.0, 107.0}}
	r2 := [][2]float64{{-7.0, 107.0}, {-7.5, 107.5}}
	rm := map[int]*routes.Route{}
	for id, poly := range map[int][][2]float64{1: r1, 2: r2} {
		tmp := routes.New(poly, 0)
		rm[id] = routes.New(poly, tmp.Cumulative[len(tmp.Cumulative)-1])
	}
	stations := []dataset.Station{
		{Code: "AAA", Name: "Alpha", Lat: -6.0, Lon: 106.0},
		{Code: "BBB", Name: "Bravo", Lat: -6.55, Lon: 106.60},
		{Code: "CCC", Name: "Charlie", Lat: -7.0, Lon: 107.0},
	}
	ds, err := dataset.New(stations, schedules, rm, [][][2]float64{r1, r2})
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

func intp(v int) *int { return &v }

func multiLeg() *dataset.Schedule {
	return &dataset.Schedule{
		TrID: 1, TrCd: "1", TrName: "Multi", OriginCode: "AAA", DestCode: "CCC",
		DepartMs: 7_200_000, ArrivMs: 14_400_000, DistanceKm: 123.456,
		Stops: []dataset.Stop{
			{Seq: 0, StationCode: "AAA", ArrivMs: 7_200_000, DepartMs: 7_200_000},
			{Seq: 1, StationCode: "BBB", ArrivMs: 10_800_000, DepartMs: 11_100_000, RouteID: intp(1)},
			{Seq: 2, StationCode: "CCC", ArrivMs: 14_400_000, DepartMs: 14_400_000, RouteID: intp(2)},
		},
	}
}

func at(s string) int64 {
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return tm.UnixMilli()
}

func near(t *testing.T, name string, got *float64, want, tol float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: got nil, want %v", name, want)
	}
	if math.Abs(*got-want) > tol {
		t.Fatalf("%s: got %v, want %v ±%v", name, *got, want, tol)
	}
}

func TestMultiLegMovingOnFirstLeg(t *testing.T) {
	s := multiLeg()
	ds := fixture(t, s)
	p := PositionFor(ds, s, at("2026-07-23T02:30:00Z"))
	if p.Status != DalamPerjalanan {
		t.Fatalf("status %s", p.Status)
	}
	if math.Abs(p.Progress-0.25) > 0.001 {
		t.Fatalf("progress %v", p.Progress)
	}
	near(t, "lat", p.Lat, -6.5, 0.05)
	near(t, "lon", p.Lon, 106.5, 0.05)
	if p.CurrentStopSeq != nil || p.NextStopSeq == nil || *p.NextStopSeq != 1 {
		t.Fatalf("seqs %v %v", p.CurrentStopSeq, p.NextStopSeq)
	}
}

func TestMultiLegDwellingAtIntermediate(t *testing.T) {
	s := multiLeg()
	ds := fixture(t, s)
	p := PositionFor(ds, s, at("2026-07-23T03:02:00Z"))
	if p.Status != DalamPerjalanan {
		t.Fatalf("status %s", p.Status)
	}
	if *p.Lat != -6.55 || *p.Lon != 106.60 {
		t.Fatalf("dwell pos %v,%v", *p.Lat, *p.Lon)
	}
	if *p.CurrentStopSeq != 1 || *p.NextStopSeq != 2 {
		t.Fatalf("seqs %v %v", *p.CurrentStopSeq, *p.NextStopSeq)
	}
	// Counts down to arrival at CCC: 14_400_000 - 10_920_000 = 3480 s.
	if *p.SecondsToNextStop != 3480 {
		t.Fatalf("seconds %v", *p.SecondsToNextStop)
	}
}

func TestMultiLegSecondLeg(t *testing.T) {
	s := multiLeg()
	ds := fixture(t, s)
	p := PositionFor(ds, s, at("2026-07-23T03:20:00Z"))
	if p.Status != DalamPerjalanan || *p.Lat > -7.0 || *p.Lat < -7.5 {
		t.Fatalf("got %s lat %v", p.Status, *p.Lat)
	}
}

func TestTripCrossingMidnightWIB(t *testing.T) {
	s := &dataset.Schedule{
		TrID: 2, TrCd: "2", OriginCode: "AAA", DestCode: "BBB",
		DepartMs: 57_600_000, ArrivMs: 68_400_000,
		Stops: []dataset.Stop{
			{Seq: 0, StationCode: "AAA", ArrivMs: 57_600_000, DepartMs: 57_600_000},
			{Seq: 1, StationCode: "BBB", ArrivMs: 68_400_000, DepartMs: 68_400_000, RouteID: intp(1)},
		},
	}
	ds := fixture(t, s)
	p := PositionFor(ds, s, at("2026-07-22T17:30:00Z"))
	if p.Status != DalamPerjalanan || math.Abs(p.Progress-0.5) > 0.001 {
		t.Fatalf("got %s %v", p.Status, p.Progress)
	}
}

func TestTripCrossingMidnightUTCYesterdayInstance(t *testing.T) {
	// Starts 22:00 UTC, ends 02:00 UTC next day (ms axis not wrapped).
	s := &dataset.Schedule{
		TrID: 3, TrCd: "3", OriginCode: "AAA", DestCode: "BBB",
		DepartMs: 79_200_000, ArrivMs: 93_600_000,
		Stops: []dataset.Stop{
			{Seq: 0, StationCode: "AAA", ArrivMs: 79_200_000, DepartMs: 79_200_000},
			{Seq: 1, StationCode: "BBB", ArrivMs: 93_600_000, DepartMs: 93_600_000, RouteID: intp(1)},
		},
	}
	ds := fixture(t, s)
	p := PositionFor(ds, s, at("2026-07-23T01:00:00Z"))
	if p.Status != DalamPerjalanan || math.Abs(p.Progress-0.75) > 0.001 {
		t.Fatalf("got %s %v", p.Status, p.Progress)
	}
}

func TestNotYetDeparted(t *testing.T) {
	s := &dataset.Schedule{
		TrID: 4, TrCd: "4", OriginCode: "AAA", DestCode: "BBB",
		DepartMs: 39_600_000, ArrivMs: 46_800_000,
		Stops: []dataset.Stop{
			{Seq: 0, StationCode: "AAA", ArrivMs: 39_600_000, DepartMs: 39_600_000},
			{Seq: 1, StationCode: "BBB", ArrivMs: 46_800_000, DepartMs: 46_800_000, RouteID: intp(1)},
		},
	}
	ds := fixture(t, s)
	p := PositionFor(ds, s, at("2026-07-23T03:00:00Z"))
	if p.Status != BelumBerangkat || p.Progress != 0 || *p.Lat != -6.0 {
		t.Fatalf("got %+v", p)
	}
	if p.NextStopSeq != nil || p.SecondsToNextStop != nil {
		t.Fatal("seq fields must be null when not in transit")
	}
}

func TestArrived(t *testing.T) {
	s := &dataset.Schedule{
		TrID: 5, TrCd: "5", OriginCode: "AAA", DestCode: "BBB",
		DepartMs: 0, ArrivMs: 3_600_000,
		Stops: []dataset.Stop{
			{Seq: 0, StationCode: "AAA", ArrivMs: 0, DepartMs: 0},
			{Seq: 1, StationCode: "BBB", ArrivMs: 3_600_000, DepartMs: 3_600_000, RouteID: intp(1)},
		},
	}
	ds := fixture(t, s)
	p := PositionFor(ds, s, at("2026-07-23T02:00:00Z"))
	if p.Status != SudahTiba || p.Progress != 1 || *p.Lat != -6.55 || *p.Lon != 106.60 {
		t.Fatalf("got %+v", p)
	}
}

func TestInvRoute(t *testing.T) {
	// CCC -> AAA over route 1 in reverse direction.
	s := &dataset.Schedule{
		TrID: 6, TrCd: "6", OriginCode: "CCC", DestCode: "AAA",
		DepartMs: 0, ArrivMs: 3_600_000,
		Stops: []dataset.Stop{
			{Seq: 0, StationCode: "CCC", ArrivMs: 0, DepartMs: 0},
			{Seq: 1, StationCode: "AAA", ArrivMs: 3_600_000, DepartMs: 3_600_000, RouteID: intp(1), InvRoute: true},
		},
	}
	ds := fixture(t, s)
	p := PositionFor(ds, s, at("2026-07-23T00:15:00Z")) // 25% of the leg
	near(t, "lat", p.Lat, -6.75, 0.05)
	near(t, "lon", p.Lon, 106.75, 0.05)

	d := Detail(ds, s, at("2026-07-23T00:15:00Z"))
	if d.Polyline[0] != [2]float64{-7.0, 107.0} {
		t.Fatalf("inv polyline not reversed: %v", d.Polyline)
	}
}

func TestFormatClock(t *testing.T) {
	cases := map[int64]string{
		26_100_000: "14:15:00",
		86_100_000: "06:55:00",
		0:          "07:00:00",
		61_200_000: "00:00:00",
		93_599_999: "08:59:59",
	}
	for ms, want := range cases {
		if got := FormatClock(ms); got != want {
			t.Errorf("FormatClock(%d) = %s, want %s", ms, got, want)
		}
	}
}

func TestDetailJSONShape(t *testing.T) {
	s := multiLeg()
	ds := fixture(t, s)
	d := Detail(ds, s, at("2026-07-23T03:02:00Z"))
	b, _ := json.Marshal(d)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"tr_id", "tr_cd", "tr_name", "origin_code", "origin_name", "dest_code", "dest_name",
		"distance_km", "berangkat", "datang", "status", "current_stop_seq", "next_stop_seq", "seconds_to_next_stop", "stops", "polyline"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %q", k)
		}
	}
	if m["distance_km"].(float64) != 123.5 {
		t.Errorf("distance_km %v", m["distance_km"])
	}
	if m["origin_name"] != "Alpha" || m["berangkat"] != "09:00:00" {
		t.Errorf("got %v %v", m["origin_name"], m["berangkat"])
	}
	stops := m["stops"].([]any)
	first := stops[0].(map[string]any)
	for _, k := range []string{"seq", "station_code", "station_name", "arriv", "depart", "is_pass_through"} {
		if _, ok := first[k]; !ok {
			t.Errorf("stop missing key %q", k)
		}
	}
	// 3 points route 1 + 2 points route 2
	if len(d.Polyline) != 5 {
		t.Errorf("polyline len %d", len(d.Polyline))
	}

	// Not in transit -> seq fields serialised as null.
	d2 := Detail(ds, s, at("2026-07-23T05:00:00Z"))
	b2, _ := json.Marshal(d2)
	var m2 map[string]any
	_ = json.Unmarshal(b2, &m2)
	if m2["status"] != string(SudahTiba) || m2["current_stop_seq"] != nil {
		t.Errorf("got %v %v", m2["status"], m2["current_stop_seq"])
	}
}
