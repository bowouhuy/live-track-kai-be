// Package source holds the raw JSON shapes returned by gapeka2025.com and
// loads the snapshot files stored in data/raw.
package source

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
)

// File names inside the raw data directory.
const (
	StationsFile = "gapeka2025_stations.json"
	TrainsFile   = "gapeka2025_trains.json"
	RoutesFile   = "gapeka2025_routes.json"
)

// Envelope is the response wrapper used by every gapeka2025.com endpoint.
type Envelope[T any] struct {
	StatusCode int    `json:"statusCode"`
	Message    string `json:"message"`
	Data       []T    `json:"data"`
}

type Station struct {
	Cd  string     `json:"cd"`
	Nm  string     `json:"nm"`
	Pos [2]float64 `json:"pos"` // [lat, lon]
}

type Train struct {
	TrID   FlexInt     `json:"tr_id"`
	TrCd   FlexString  `json:"tr_cd"`
	TrName string      `json:"tr_name"`
	Paths  []TrainPath `json:"paths"`
}

type TrainPath struct {
	StCd      string   `json:"st_cd"`
	ArrivMs   FlexInt  `json:"arriv_ms"`
	DepartMs  FlexInt  `json:"depart_ms"`
	RouteID   *FlexInt `json:"route_id"`
	InvRoute  *bool    `json:"inv_route"`
	UsrArriv  *string  `json:"usr_arriv"`
	UsrDepart *string  `json:"usr_depart"`
}

type Route struct {
	RouteID FlexInt     `json:"route_id"`
	LenCm   float64     `json:"len_cm"` // actually metres, despite the name
	Paths   []RouteEdge `json:"paths"`
}

type RouteEdge struct {
	Pos   [][2]float64 `json:"pos"`    // [[lat, lon], [lat, lon]]
	PosCm []float64    `json:"pos_cm"` // distance (metres) of each point from the canonical route start
}

// Raw is the full snapshot read from disk.
type Raw struct {
	Stations []Station
	Trains   []Train
	Routes   []Route
}

// Load reads the three snapshot files from dir.
func Load(dir string) (*Raw, error) {
	var raw Raw
	var err error
	if raw.Stations, err = readEnvelope[Station](filepath.Join(dir, StationsFile)); err != nil {
		return nil, err
	}
	if raw.Trains, err = readEnvelope[Train](filepath.Join(dir, TrainsFile)); err != nil {
		return nil, err
	}
	if raw.Routes, err = readEnvelope[Route](filepath.Join(dir, RoutesFile)); err != nil {
		return nil, err
	}
	return &raw, nil
}

func readEnvelope[T any](path string) ([]T, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var env Envelope[T]
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(env.Data) == 0 {
		return nil, fmt.Errorf("%s: envelope has no data", path)
	}
	return env.Data, nil
}

// FlexInt accepts a JSON number (integer or float) or a numeric string.
type FlexInt int64

func (f *FlexInt) UnmarshalJSON(b []byte) error {
	b = bytes.Trim(b, `"`)
	if string(b) == "null" || len(b) == 0 {
		*f = 0
		return nil
	}
	if i, err := strconv.ParseInt(string(b), 10, 64); err == nil {
		*f = FlexInt(i)
		return nil
	}
	fl, err := strconv.ParseFloat(string(b), 64)
	if err != nil {
		return fmt.Errorf("FlexInt: %q is not a number", b)
	}
	*f = FlexInt(math.Round(fl))
	return nil
}

// FlexString accepts a JSON string or number.
type FlexString string

func (f *FlexString) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = FlexString(s)
		return nil
	}
	if string(b) == "null" {
		*f = ""
		return nil
	}
	*f = FlexString(b)
	return nil
}
