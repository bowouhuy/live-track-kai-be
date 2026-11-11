// Package api exposes the REST + SSE endpoints.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"livetrack/internal/dataset"
	"livetrack/internal/position"
	"livetrack/internal/realtime"
)

type Server struct {
	Store             *dataset.Store
	Hub               *realtime.Hub
	BroadcastInterval time.Duration
	AllowedOrigins    []string
	Now               func() time.Time
}

// Handler returns the full HTTP handler with CORS applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/config", s.config)
	mux.HandleFunc("GET /api/stations", s.static(func(d *dataset.Dataset) dataset.Encoded { return d.StationsJSON }))
	mux.HandleFunc("GET /api/rail-lines", s.static(func(d *dataset.Dataset) dataset.Encoded { return d.RailLinesJSON }))
	mux.HandleFunc("GET /api/trains/live", s.live)
	mux.HandleFunc("GET /api/trains/{trId}", s.detail)
	mux.Handle("GET /api/stream", s.Hub)
	return s.cors(mux)
}

// LivePayload builds the SSE payload: only trains currently in transit.
func (s *Server) LivePayload() ([]byte, error) {
	trains := position.CurrentPositions(s.Store.Load(), s.Now().UnixMilli(), position.DalamPerjalanan)
	return json.Marshal(map[string]any{"trains": trains})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	ds := s.Store.Load()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"schedules":   len(ds.Schedules),
		"stations":    len(ds.Stations),
		"routes":      len(ds.Routes),
		"loaded_at":   ds.LoadedAt.UTC().Format(time.RFC3339),
		"sse_clients": s.Hub.Clients(),
	})
}

func (s *Server) config(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"broadcast_interval_seconds": int(s.BroadcastInterval / time.Second),
	})
}

func (s *Server) static(pick func(*dataset.Dataset) dataset.Encoded) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := pick(s.Store.Load())
		h := w.Header()
		h.Set("Content-Type", "application/json")
		h.Set("Cache-Control", "public, max-age=300")
		h.Add("Vary", "Accept-Encoding")
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			h.Set("Content-Encoding", "gzip")
			_, _ = w.Write(body.Gzip)
			return
		}
		_, _ = w.Write(body.Plain)
	}
}

// live returns every schedule (Laravel-compatible); ?status=dalam_perjalanan filters.
func (s *Server) live(w http.ResponseWriter, r *http.Request) {
	only := position.Status(r.URL.Query().Get("status"))
	switch only {
	case "", position.BelumBerangkat, position.DalamPerjalanan, position.SudahTiba:
	default:
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "status tidak valid"})
		return
	}
	trains := position.CurrentPositions(s.Store.Load(), s.Now().UnixMilli(), only)
	writeJSON(w, http.StatusOK, map[string]any{"trains": trains})
}

func (s *Server) detail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("trId"))
	ds := s.Store.Load()
	sched, ok := ds.ScheduleByID[id]
	if err != nil || !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	writeJSON(w, http.StatusOK, position.Detail(ds, sched, s.Now().UnixMilli()))
}

func (s *Server) cors(next http.Handler) http.Handler {
	any := slices.Contains(s.AllowedOrigins, "*")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			if any {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else if slices.Contains(s.AllowedOrigins, origin) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
			}
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Cache-Control, Last-Event-ID")
			w.Header().Set("Access-Control-Max-Age", "86400")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("write json", "err", err)
	}
}
