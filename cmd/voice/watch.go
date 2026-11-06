package main

import (
	"context"
	"os"
	"time"
)

// fileStamp is what tells a file changed: its time and size, or that it is
// missing.
type fileStamp struct {
	mod  time.Time
	size int64
	ok   bool
}

func stampOf(path string) fileStamp {
	st, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{mod: st.ModTime(), size: st.Size(), ok: true}
}

// watchFiles calls changed whenever one of the files is written, created or
// removed, checking every interval until ctx ends. A change made by this
// process (a window saving settings, which reloads anyway) is seen too; a
// reload is cheap and gives the same result twice.
func watchFiles(ctx context.Context, interval time.Duration, paths []string, changed func()) {
	last := make([]fileStamp, len(paths))
	for i, p := range paths {
		last[i] = stampOf(p)
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		moved := false
		for i, p := range paths {
			if s := stampOf(p); s != last[i] {
				last[i], moved = s, true
			}
		}
		if moved {
			changed()
		}
	}
}
