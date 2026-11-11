// Package position computes the simulated train status and location from the
// GAPEKA schedule and the current time. See docs/backend.md §5.
package position

import (
	"fmt"
	"math"

	"livetrack/internal/dataset"
	"livetrack/internal/geo"
)

const (
	DayMs = int64(86_400_000)
	// Display timezone: WIB (Asia/Jakarta), UTC+7, no DST.
	wibOffsetSec = int64(7 * 3600)
)

type Status string

const (
	BelumBerangkat  Status = "belum_berangkat"
	DalamPerjalanan Status = "dalam_perjalanan"
	SudahTiba       Status = "sudah_tiba"
)

// ResolveWindow finds the active daily instance of a trip. Schedule times are
// ms since UTC midnight of the trip's start day. nowMs is Unix ms.
func ResolveWindow(s *dataset.Schedule, nowMs int64) (Status, int64) {
	timeOfDay := floorMod(s.DepartMs, DayMs)
	duration := s.ArrivMs - s.DepartMs
	startToday := nowMs - floorMod(nowMs, DayMs) + timeOfDay

	for _, start := range []int64{startToday, startToday - DayMs} {
		end := start + duration
		if start <= nowMs && nowMs <= end {
			return DalamPerjalanan, s.DepartMs + (nowMs - start)
		}
	}
	if nowMs < startToday {
		return BelumBerangkat, s.DepartMs
	}
	return SudahTiba, s.ArrivMs
}

// Location is where a trip is at a given point on its own time axis.
type Location struct {
	Lat, Lon          *float64
	CurrentSeq        *int
	NextSeq           *int
	LegProgress       *float64
	SecondsToNextStop *int64
}

// LocateOnTrip walks the stops once and returns the dwell or in-leg position.
func LocateOnTrip(ds *dataset.Dataset, stops []dataset.Stop, targetMs int64) Location {
	n := len(stops)
	for i := 0; i < n; i++ {
		stop := stops[i]

		// (a) dwelling at stop i
		if targetMs <= stop.DepartMs {
			loc := Location{CurrentSeq: ptr(i)}
			loc.Lat, loc.Lon = stationPtr(ds, stop.StationCode)
			if i < n-1 {
				next := stops[i+1]
				loc.NextSeq = ptr(i + 1)
				// Counts down to *arrival* at the next stop (not departure from this one).
				loc.SecondsToNextStop = ptr((next.ArrivMs - targetMs) / 1000)
			}
			return loc
		}

		// (b) travelling on leg i -> i+1
		if i < n-1 {
			next := stops[i+1]
			if targetMs <= next.ArrivMs {
				legSpan := next.ArrivMs - stop.DepartMs
				legProgress := 1.0
				if legSpan > 0 {
					legProgress = float64(targetMs-stop.DepartMs) / float64(legSpan)
				}
				routeProgress := legProgress
				if next.InvRoute {
					routeProgress = 1 - legProgress
				}

				loc := Location{
					NextSeq:           ptr(i + 1),
					LegProgress:       ptr(Round(legProgress, 4)),
					SecondsToNextStop: ptr((next.ArrivMs - targetMs) / 1000),
				}
				if p, ok := interpolate(ds, next.RouteID, routeProgress); ok {
					loc.Lat, loc.Lon = ptr(p[0]), ptr(p[1])
				} else {
					// Defensive fallback: straight line between the two stations.
					aLat, aLon, okA := ds.Coords(stop.StationCode)
					bLat, bLon, okB := ds.Coords(next.StationCode)
					if okA && okB {
						p := geo.Lerp([2]float64{aLat, aLon}, [2]float64{bLat, bLon}, legProgress)
						loc.Lat, loc.Lon = ptr(p[0]), ptr(p[1])
					}
				}
				return loc
			}
		}
	}

	// Past every stop: at the last station.
	loc := Location{}
	if n > 0 {
		loc.CurrentSeq = ptr(n - 1)
		loc.Lat, loc.Lon = stationPtr(ds, stops[n-1].StationCode)
	}
	return loc
}

func interpolate(ds *dataset.Dataset, routeID *int, progress float64) ([2]float64, bool) {
	if routeID == nil {
		return [2]float64{}, false
	}
	r, ok := ds.Routes[*routeID]
	if !ok {
		return [2]float64{}, false
	}
	return r.Interpolate(progress)
}

// LiveTrain is one entry of GET /api/trains/live and of the SSE payload.
// current_stop_seq / next_stop_seq / seconds_to_next_stop are additions over
// the Laravel version so the client can update the detail panel per tick.
type LiveTrain struct {
	TrID              int      `json:"tr_id"`
	NomorKA           string   `json:"nomor_ka"`
	Nama              string   `json:"nama"`
	Relasi            string   `json:"relasi"`
	Berangkat         string   `json:"berangkat"`
	Datang            string   `json:"datang"`
	Status            Status   `json:"status"`
	Progress          float64  `json:"progress"`
	Lat               *float64 `json:"lat"`
	Lon               *float64 `json:"lon"`
	CurrentStopSeq    *int     `json:"current_stop_seq"`
	NextStopSeq       *int     `json:"next_stop_seq"`
	SecondsToNextStop *int64   `json:"seconds_to_next_stop"`
}

// PositionFor computes one live entry.
func PositionFor(ds *dataset.Dataset, s *dataset.Schedule, nowMs int64) LiveTrain {
	status, target := ResolveWindow(s, nowMs)
	lt := LiveTrain{
		TrID:      s.TrID,
		NomorKA:   s.TrCd,
		Nama:      s.TrName,
		Relasi:    s.OriginCode + "-" + s.DestCode,
		Berangkat: FormatClock(s.DepartMs),
		Datang:    FormatClock(s.ArrivMs),
		Status:    status,
		Progress:  progress(s, target),
	}
	switch status {
	case DalamPerjalanan:
		loc := LocateOnTrip(ds, s.Stops, target)
		lt.Lat, lt.Lon = loc.Lat, loc.Lon
		lt.CurrentStopSeq, lt.NextStopSeq, lt.SecondsToNextStop = loc.CurrentSeq, loc.NextSeq, loc.SecondsToNextStop
	case BelumBerangkat:
		lt.Lat, lt.Lon = stationPtr(ds, s.OriginCode)
	case SudahTiba:
		lt.Lat, lt.Lon = stationPtr(ds, s.DestCode)
	}
	return lt
}

// CurrentPositions computes every schedule, optionally keeping only one status.
func CurrentPositions(ds *dataset.Dataset, nowMs int64, only Status) []LiveTrain {
	out := make([]LiveTrain, 0, len(ds.Schedules))
	for _, s := range ds.Schedules {
		lt := PositionFor(ds, s, nowMs)
		if only != "" && lt.Status != only {
			continue
		}
		out = append(out, lt)
	}
	return out
}

type DetailStop struct {
	Seq           int    `json:"seq"`
	StationCode   string `json:"station_code"`
	StationName   string `json:"station_name"`
	Arriv         string `json:"arriv"`
	Depart        string `json:"depart"`
	IsPassThrough bool   `json:"is_pass_through"`
}

// TrainDetail is the body of GET /api/trains/{trId}.
type TrainDetail struct {
	TrID              int          `json:"tr_id"`
	TrCd              string       `json:"tr_cd"`
	TrName            string       `json:"tr_name"`
	OriginCode        string       `json:"origin_code"`
	OriginName        string       `json:"origin_name"`
	DestCode          string       `json:"dest_code"`
	DestName          string       `json:"dest_name"`
	DistanceKm        float64      `json:"distance_km"`
	Berangkat         string       `json:"berangkat"`
	Datang            string       `json:"datang"`
	Status            Status       `json:"status"`
	CurrentStopSeq    *int         `json:"current_stop_seq"`
	NextStopSeq       *int         `json:"next_stop_seq"`
	SecondsToNextStop *int64       `json:"seconds_to_next_stop"`
	Stops             []DetailStop `json:"stops"`
	Polyline          [][2]float64 `json:"polyline"`
}

// Detail builds the train detail response.
func Detail(ds *dataset.Dataset, s *dataset.Schedule, nowMs int64) TrainDetail {
	status, target := ResolveWindow(s, nowMs)
	d := TrainDetail{
		TrID:       s.TrID,
		TrCd:       s.TrCd,
		TrName:     s.TrName,
		OriginCode: s.OriginCode,
		OriginName: ds.StationName(s.OriginCode),
		DestCode:   s.DestCode,
		DestName:   ds.StationName(s.DestCode),
		DistanceKm: Round(s.DistanceKm, 1),
		Berangkat:  FormatClock(s.DepartMs),
		Datang:     FormatClock(s.ArrivMs),
		Status:     status,
		Stops:      make([]DetailStop, len(s.Stops)),
		Polyline:   [][2]float64{},
	}
	if status == DalamPerjalanan {
		loc := LocateOnTrip(ds, s.Stops, target)
		d.CurrentStopSeq, d.NextStopSeq, d.SecondsToNextStop = loc.CurrentSeq, loc.NextSeq, loc.SecondsToNextStop
	}
	for i, st := range s.Stops {
		d.Stops[i] = DetailStop{
			Seq:           st.Seq,
			StationCode:   st.StationCode,
			StationName:   ds.StationName(st.StationCode),
			Arriv:         FormatClock(st.ArrivMs),
			Depart:        FormatClock(st.DepartMs),
			IsPassThrough: st.PassThrough,
		}
		if st.RouteID == nil {
			continue
		}
		r, ok := ds.Routes[*st.RouteID]
		if !ok {
			continue
		}
		if st.InvRoute {
			for k := len(r.Polyline) - 1; k >= 0; k-- {
				d.Polyline = append(d.Polyline, r.Polyline[k])
			}
		} else {
			d.Polyline = append(d.Polyline, r.Polyline...)
		}
	}
	return d
}

// FormatClock renders ms-since-UTC-midnight as "HH:MM:SS" in WIB, wrapping past 24h.
func FormatClock(ms int64) string {
	secs := floorMod(floorDiv(ms, 1000)+wibOffsetSec, 86_400)
	return fmt.Sprintf("%02d:%02d:%02d", secs/3600, secs%3600/60, secs%60)
}

func progress(s *dataset.Schedule, target int64) float64 {
	duration := s.ArrivMs - s.DepartMs
	if duration <= 0 {
		return 1.0
	}
	p := float64(target-s.DepartMs) / float64(duration)
	return Round(math.Max(0, math.Min(1, p)), 4)
}

// Round rounds half away from zero (PHP round semantics).
func Round(x float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(x*p) / p
}

func stationPtr(ds *dataset.Dataset, code string) (*float64, *float64) {
	lat, lon, ok := ds.Coords(code)
	if !ok {
		return nil, nil
	}
	return &lat, &lon
}

func ptr[T any](v T) *T { return &v }

func floorMod(a, b int64) int64 {
	m := a % b
	if m < 0 {
		m += b
	}
	return m
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}
