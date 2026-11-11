// Package dataset turns the raw gapeka2025 snapshot into a read-only in-memory
// model (the equivalent of the old Laravel pipeline + MySQL tables).
package dataset

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"livetrack/internal/routes"
	"livetrack/internal/source"
)

type Station struct {
	Code string  `json:"code"`
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
}

type Stop struct {
	Seq         int
	StationCode string
	ArrivMs     int64
	DepartMs    int64
	RouteID     *int     // leg from stop Seq-1 to this stop
	InvRoute    bool     // leg runs against the route's canonical direction
	DistanceKm  *float64 // length of the leg ending at this stop
	PassThrough bool     // "Ls" (langsung): train passes without stopping
}

type Schedule struct {
	TrID       int
	TrCd       string
	TrName     string
	OriginCode string
	DestCode   string
	DepartMs   int64
	ArrivMs    int64
	DistanceKm float64
	Stops      []Stop
}

// Dataset is immutable after construction; swap whole datasets to reload.
type Dataset struct {
	Stations      []Station
	StationByCode map[string]*Station
	Schedules     []*Schedule // sorted by tr_cd (case-insensitive string order)
	ScheduleByID  map[int]*Schedule
	Routes        map[int]*routes.Route
	LoadedAt      time.Time

	// Pre-encoded static responses (plain + gzip).
	StationsJSON  Encoded
	RailLinesJSON Encoded
}

// Encoded is a JSON body with its gzip-compressed form.
type Encoded struct {
	Plain []byte
	Gzip  []byte
}

// Build runs the pipeline transformations (stations, schedules, routes) in memory.
func Build(raw *source.Raw) (*Dataset, error) {
	stations := make([]Station, 0, len(raw.Stations))
	for _, s := range raw.Stations {
		stations = append(stations, Station{
			Code: s.Cd,
			Name: s.Nm,
			// The old DB stored DECIMAL(10,7); keep the same precision.
			Lat: round(s.Pos[0], 7),
			Lon: round(s.Pos[1], 7),
		})
	}

	routeMap := make(map[int]*routes.Route, len(raw.Routes))
	railLines := make([][][2]float64, 0, len(raw.Routes))
	lenKm := make(map[int]float64, len(raw.Routes))
	for _, r := range raw.Routes {
		poly := routes.Flatten(r.Paths)
		total := r.LenCm / 1000
		routeMap[int(r.RouteID)] = routes.New(poly, total)
		lenKm[int(r.RouteID)] = total
		railLines = append(railLines, poly)
	}

	schedules := make([]*Schedule, 0, len(raw.Trains))
	for _, t := range raw.Trains {
		if len(t.Paths) == 0 {
			continue
		}
		s := &Schedule{
			TrID:       int(t.TrID),
			TrCd:       string(t.TrCd),
			TrName:     t.TrName,
			OriginCode: t.Paths[0].StCd,
			DestCode:   t.Paths[len(t.Paths)-1].StCd,
			DepartMs:   int64(t.Paths[0].DepartMs),
			ArrivMs:    int64(t.Paths[len(t.Paths)-1].ArrivMs),
			Stops:      make([]Stop, len(t.Paths)),
		}
		var total float64
		for i, p := range t.Paths {
			stop := Stop{
				Seq:         i,
				StationCode: p.StCd,
				ArrivMs:     int64(p.ArrivMs),
				DepartMs:    int64(p.DepartMs),
				InvRoute:    p.InvRoute != nil && *p.InvRoute,
				PassThrough: isLs(p.UsrArriv) || isLs(p.UsrDepart),
			}
			if p.RouteID != nil {
				id := int(*p.RouteID)
				stop.RouteID = &id
				if km, ok := lenKm[id]; ok {
					total += km
					d := round(km, 3)
					stop.DistanceKm = &d
				}
			}
			s.Stops[i] = stop
		}
		s.DistanceKm = round(total, 3)
		schedules = append(schedules, s)
	}

	return New(stations, schedules, routeMap, railLines)
}

// New assembles a Dataset from already-transformed parts (also used by tests).
func New(stations []Station, schedules []*Schedule, routeMap map[int]*routes.Route, railLines [][][2]float64) (*Dataset, error) {
	ds := &Dataset{
		Stations:      stations,
		StationByCode: make(map[string]*Station, len(stations)),
		Schedules:     schedules,
		ScheduleByID:  make(map[int]*Schedule, len(schedules)),
		Routes:        routeMap,
		LoadedAt:      time.Now(),
	}
	for i := range ds.Stations {
		ds.StationByCode[ds.Stations[i].Code] = &ds.Stations[i]
	}
	for _, s := range ds.Schedules {
		ds.ScheduleByID[s.TrID] = s
	}
	// Old API ordered by tr_cd with a case-insensitive MySQL collation (string order).
	sort.SliceStable(ds.Schedules, func(a, b int) bool {
		x, y := strings.ToLower(ds.Schedules[a].TrCd), strings.ToLower(ds.Schedules[b].TrCd)
		if x != y {
			return x < y
		}
		return ds.Schedules[a].TrID < ds.Schedules[b].TrID
	})

	var err error
	if ds.StationsJSON, err = encode(ds.Stations); err != nil {
		return nil, fmt.Errorf("encode stations: %w", err)
	}
	if railLines == nil {
		railLines = [][][2]float64{}
	}
	if ds.RailLinesJSON, err = encode(railLines); err != nil {
		return nil, fmt.Errorf("encode rail lines: %w", err)
	}
	return ds, nil
}

// Coords returns the coordinates of a station code.
func (d *Dataset) Coords(code string) (lat, lon float64, ok bool) {
	s, ok := d.StationByCode[code]
	if !ok {
		return 0, 0, false
	}
	return s.Lat, s.Lon, true
}

// StationName returns the station name, falling back to the code.
func (d *Dataset) StationName(code string) string {
	if s, ok := d.StationByCode[code]; ok {
		return s.Name
	}
	return code
}

// Store holds the current dataset and allows atomic swaps on reload.
type Store struct{ p atomic.Pointer[Dataset] }

func (s *Store) Load() *Dataset  { return s.p.Load() }
func (s *Store) Swap(d *Dataset) { s.p.Store(d) }
func NewStore(d *Dataset) *Store { st := &Store{}; st.Swap(d); return st }

func isLs(s *string) bool { return s != nil && *s == "Ls" }

// round rounds half away from zero, like PHP round().
func round(x float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(x*p) / p
}

func encode(v any) (Encoded, error) {
	plain, err := json.Marshal(v)
	if err != nil {
		return Encoded{}, err
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if _, err := zw.Write(plain); err != nil {
		return Encoded{}, err
	}
	if err := zw.Close(); err != nil {
		return Encoded{}, err
	}
	return Encoded{Plain: plain, Gzip: buf.Bytes()}, nil
}
