//go:build !windows

package main

import (
	"context"
	"log/slog"
)

func warnPublicNetworks(context.Context, *slog.Logger) {}
