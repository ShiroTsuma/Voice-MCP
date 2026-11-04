package core

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"voice/internal/audio"
	"voice/internal/preset"
)

// Shared state lets separate voice processes (the MCP backend, hook runs,
// a second backend for another protocol version) agree on mute and know
// when something was last said.
type sharedState struct {
	Muted     bool      `json:"muted"`
	LastSpoke time.Time `json:"last_spoke"`
	// Asked is when each agent (by process ID) last asked to say something
	// itself: its speech may still be queued or playing.
	Asked map[string]time.Time `json:"asked,omitempty"`
}

var (
	stateMu   sync.Mutex
	statePath string
	cacheDir  string
)

// SetDataDir enables shared state and the phrase cache under dir. Without it
// (tests) state is per process and nothing is cached.
func SetDataDir(dir string) {
	stateMu.Lock()
	defer stateMu.Unlock()
	statePath = filepath.Join(dir, "state.json")
	cacheDir = filepath.Join(dir, "cache")
}

// DataDir is the directory given to SetDataDir.
func DataDir() string {
	stateMu.Lock()
	defer stateMu.Unlock()
	return filepath.Dir(statePath)
}

func loadState() sharedState {
	stateMu.Lock()
	defer stateMu.Unlock()
	var st sharedState
	if statePath == "" {
		return st
	}
	if b, err := os.ReadFile(statePath); err == nil {
		json.Unmarshal(b, &st)
	}
	return st
}

func updateState(fn func(*sharedState)) {
	stateMu.Lock()
	defer stateMu.Unlock()
	if statePath == "" {
		return
	}
	var st sharedState
	if b, err := os.ReadFile(statePath); err == nil {
		json.Unmarshal(b, &st)
	}
	fn(&st)
	b, _ := json.Marshal(st)
	os.MkdirAll(filepath.Dir(statePath), 0o755)
	tmp := statePath + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, statePath)
	}
}

// SinceLastSpoke reports how long ago any voice process finished speaking.
func SinceLastSpoke() time.Duration {
	st := loadState()
	if st.LastSpoke.IsZero() {
		return time.Duration(1<<63 - 1)
	}
	return time.Since(st.LastSpoke)
}

// SharedMuted reports the mute flag set by any voice process.
func SharedMuted() bool { return loadState().Muted }

// MarkAsked records that the agent with this process ID asked to speak.
func MarkAsked(pid uint32) {
	if pid == 0 {
		return
	}
	updateState(func(st *sharedState) {
		if st.Asked == nil {
			st.Asked = map[string]time.Time{}
		}
		for k, at := range st.Asked {
			if time.Since(at) > 10*time.Minute {
				delete(st.Asked, k)
			}
		}
		st.Asked[strconv.FormatUint(uint64(pid), 10)] = time.Now()
	})
}

// SinceAsked reports how long ago the agent asked to speak, counted from
// the request: unlike SinceLastSpoke, it covers speech still playing.
func SinceAsked(pid uint32) time.Duration {
	if at, ok := loadState().Asked[strconv.FormatUint(uint64(pid), 10)]; ok && pid != 0 {
		return time.Since(at)
	}
	return time.Duration(1<<63 - 1)
}

func markSpoke() { updateState(func(st *sharedState) { st.LastSpoke = time.Now() }) }

// cacheKey covers everything that changes the rendered audio.
func cacheKey(p preset.Resolved, ch preset.Choice, lang, text string) string {
	h := fnv.New64a()
	fmt.Fprintf(h, "%s|%v|%v|%g|%v|%s|%s", p.Name, ch, p.DSP, p.Volume, p.Pitch, lang, text)
	return fmt.Sprintf("%016x", h.Sum64())
}

func readCache(key string) []float32 {
	stateMu.Lock()
	dir := cacheDir
	stateMu.Unlock()
	if dir == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(dir, key+".wav"))
	if err != nil {
		return nil
	}
	pcm, rate, err := audio.DecodeWAV(b)
	if err != nil || rate != audio.DeviceRate {
		return nil
	}
	return pcm
}

func writeCache(key string, pcm []float32) {
	stateMu.Lock()
	dir := cacheDir
	stateMu.Unlock()
	if dir == "" || len(pcm) == 0 {
		return
	}
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, key+".wav")
	if os.WriteFile(path+".tmp", audio.EncodeWAV(pcm, audio.DeviceRate), 0o644) == nil {
		os.Rename(path+".tmp", path)
	}
}
