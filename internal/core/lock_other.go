//go:build !windows

package core

import "context"

func acquirePlayback(context.Context) (func(), error) { return func() {}, nil }

func HoldPlayback(ctx context.Context) (func(), error) { return acquirePlayback(ctx) }
