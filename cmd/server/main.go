// Command server serves the REST API and the SSE position stream.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"livetrack/internal/api"
	"livetrack/internal/config"
	"livetrack/internal/dataset"
	"livetrack/internal/realtime"
	"livetrack/internal/source"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func load(dir string) (*dataset.Dataset, error) {
	start := time.Now()
	raw, err := source.Load(dir)
	if err != nil {
		return nil, err
	}
	ds, err := dataset.Build(raw)
	if err != nil {
		return nil, err
	}
	slog.Info("dataset loaded", "dir", dir, "stations", len(ds.Stations),
		"schedules", len(ds.Schedules), "routes", len(ds.Routes), "took", time.Since(start).Round(time.Millisecond))
	return ds, nil
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ds, err := load(cfg.DataDir)
	if err != nil {
		return err
	}

	store := dataset.NewStore(ds)
	hub := realtime.NewHub()
	srv := &api.Server{
		Store:             store,
		Hub:               hub,
		BroadcastInterval: cfg.BroadcastInterval,
		AllowedOrigins:    cfg.AllowedOrigins,
		Now:               time.Now,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Request contexts derive from baseCtx so open SSE streams end on shutdown.
	baseCtx, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return baseCtx },
	}

	go realtime.RunBroadcaster(ctx, hub, cfg.BroadcastInterval, srv.LivePayload)
	go watchReload(ctx, cfg.DataDir, store)

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.Addr, "interval", cfg.BroadcastInterval, "cors", cfg.AllowedOrigins)
		errCh <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		slog.Info("shutting down")
		cancelBase()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	}
	return nil
}
