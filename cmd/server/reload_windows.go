//go:build windows

package main

import (
	"context"

	"livetrack/internal/dataset"
)

// SIGHUP does not exist on Windows; restart the process to reload.
func watchReload(context.Context, string, *dataset.Store) {}
