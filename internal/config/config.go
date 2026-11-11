// Package config reads settings from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr              string        // HTTP listen address
	DataDir           string        // directory holding gapeka2025_*.json
	BroadcastInterval time.Duration // SSE tick interval
	AllowedOrigins    []string      // CORS; ["*"] allows any origin
}

func Load() (Config, error) {
	c := Config{
		Addr:    ":" + env("PORT", "8080"),
		DataDir: env("DATA_DIR", "data/raw"),
	}

	secs, err := strconv.Atoi(env("BROADCAST_INTERVAL_SECONDS", "5"))
	if err != nil || secs < 1 {
		return c, fmt.Errorf("BROADCAST_INTERVAL_SECONDS must be a positive integer")
	}
	c.BroadcastInterval = time.Duration(secs) * time.Second

	for _, o := range strings.Split(env("CORS_ALLOWED_ORIGINS", "*"), ",") {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			c.AllowedOrigins = append(c.AllowedOrigins, o)
		}
	}
	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
