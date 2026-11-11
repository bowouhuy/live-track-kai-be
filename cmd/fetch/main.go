// Command fetch downloads a fresh snapshot from gapeka2025.com into DATA_DIR.
// Run it manually and rarely; the files are stored as-is.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"livetrack/internal/source"
)

const baseURL = "https://www.gapeka2025.com"

var targets = []struct{ path, file string }{
	{"/api/v1/public-station/stations", source.StationsFile},
	{"/api/v1/public-train/gapeka", source.TrainsFile},
	{"/api/v1/public-route/route-path", source.RoutesFile},
}

func main() {
	dir := os.Getenv("DATA_DIR")
	if dir == "" {
		dir = "data/raw"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(err)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	for _, t := range targets {
		if err := fetch(client, baseURL+t.path, filepath.Join(dir, t.file)); err != nil {
			fail(err)
		}
	}
	slog.Info("done; reload the server (docker compose kill -s HUP backend) or restart it")
}

func fetch(client *http.Client, url, dest string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "live-track-kai (local train simulation project)")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}

	// Sanity-check the envelope before replacing the existing file.
	var env struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil || len(env.Data) == 0 {
		return fmt.Errorf("GET %s: unexpected response body", url)
	}

	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return err
	}
	slog.Info("saved", "file", dest, "items", len(env.Data), "bytes", len(body))
	return nil
}

func fail(err error) {
	slog.Error("fetch failed", "err", err)
	os.Exit(1)
}
