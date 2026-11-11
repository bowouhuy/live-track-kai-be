//go:build !windows

package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"livetrack/internal/dataset"
)

// watchReload rebuilds the dataset on SIGHUP and swaps it atomically. On
// failure the old dataset stays in place.
func watchReload(ctx context.Context, dir string, store *dataset.Store) {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	for {
		select {
		case <-ctx.Done():
			return
		case <-hup:
			slog.Info("SIGHUP received, reloading dataset")
			ds, err := load(dir)
			if err != nil {
				slog.Error("reload failed, keeping previous dataset", "err", err)
				continue
			}
			store.Swap(ds)
		}
	}
}
