//go:build !windows

package main

import "time"

// sinceBoot is unknown here: voice never waits.
func sinceBoot() time.Duration { return time.Hour }
