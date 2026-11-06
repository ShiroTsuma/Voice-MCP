// Package daemon runs voice's background process and lets every other voice
// process (MCP servers, hooks, CLI) use it. One long-lived process owns the
// audio device, so Windows shows a single, persistent "voice" entry in the
// volume mixer, and models load once for all clients.
//
// The process listens on a random 127.0.0.1 port. Its address and a random
// token are in serve-<id>.json in the state directory; requests without the
// token are refused.
package daemon

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"voice/internal/service"
)

type Info struct {
	Addr    string `json:"addr"`
	Token   string `json:"token"`
	PID     int    `json:"pid"`
	Version string `json:"version"`
	Exe     string `json:"exe"`
}

// ID distinguishes installations (dev build and installed copy may share a
// state directory) by the executable path.
func ID(exe string) string {
	h := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(exe))))
	return hex.EncodeToString(h[:6])
}

func InfoPath(stateDir, exe string) string {
	return filepath.Join(stateDir, "serve-"+ID(exe)+".json")
}

const tokenHeader = "X-Voice-Token"

// TokenHeader carries the token on requests from voice processes and the
// settings window.
const TokenHeader = tokenHeader

// ErrRunning means another background process of this installation is up.
var ErrRunning = errors.New("voice background process already running")

// Serve answers requests until ctx ends or a client asks it to stop. The
// caller holds the single-instance lock. extra, when set, adds handlers
// (the settings window) and learns the address and token.
func Serve(ctx context.Context, api service.API, infoPath, version, exe string, extra func(mux *http.ServeMux, info Info)) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	tok := make([]byte, 24)
	rand.Read(tok)
	info := Info{Addr: ln.Addr().String(), Token: hex.EncodeToString(tok), PID: os.Getpid(), Version: version, Exe: exe}

	mux := http.NewServeMux()
	handle := func(name string, fn func(context.Context, []byte) (any, error)) {
		mux.HandleFunc("/v1/"+name, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(tokenHeader) != info.Token {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			res, err := fn(r.Context(), body)
			w.Header().Set("Content-Type", "application/json")
			var ue service.UserError
			switch {
			case errors.As(err, &ue):
				w.WriteHeader(http.StatusUnprocessableEntity)
				json.NewEncoder(w).Encode(map[string]string{"error": ue.Msg})
			case err != nil:
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			default:
				json.NewEncoder(w).Encode(map[string]any{"result": res})
			}
		})
	}
	decodeInto := func(body []byte, v any) error {
		if len(body) == 0 {
			return nil
		}
		if err := json.Unmarshal(body, v); err != nil {
			return service.UserError{Msg: "invalid arguments: " + err.Error()}
		}
		return nil
	}
	handle("ping", func(context.Context, []byte) (any, error) {
		return map[string]any{"version": version, "pid": info.PID}, nil
	})
	handle("shutdown", func(context.Context, []byte) (any, error) {
		go func() { time.Sleep(100 * time.Millisecond); stop() }()
		return "stopping", nil
	})
	handle("speak", func(ctx context.Context, b []byte) (any, error) {
		var a service.SpeakArgs
		if err := decodeInto(b, &a); err != nil {
			return nil, err
		}
		return api.Speak(ctx, a)
	})
	handle("control", func(ctx context.Context, b []byte) (any, error) {
		var a service.ControlArgs
		if err := decodeInto(b, &a); err != nil {
			return nil, err
		}
		return api.Control(ctx, a)
	})
	handle("listen", func(ctx context.Context, b []byte) (any, error) {
		var a service.ListenArgs
		if err := decodeInto(b, &a); err != nil {
			return nil, err
		}
		return api.Listen(ctx, a)
	})
	handle("clone", func(ctx context.Context, b []byte) (any, error) {
		var a service.CloneArgs
		if err := decodeInto(b, &a); err != nil {
			return nil, err
		}
		return api.Clone(ctx, a)
	})

	handle("agent", func(ctx context.Context, b []byte) (any, error) {
		var a service.AgentArgs
		if err := decodeInto(b, &a); err != nil {
			return nil, err
		}
		return api.Agent(ctx, a)
	})

	handle("dictate", func(ctx context.Context, b []byte) (any, error) {
		var a service.DictateArgs
		if err := decodeInto(b, &a); err != nil {
			return nil, err
		}
		return api.Dictate(ctx, a)
	})

	handle("mod", func(ctx context.Context, b []byte) (any, error) {
		var a service.ModArgs
		if err := decodeInto(b, &a); err != nil {
			return nil, err
		}
		return api.Mod(ctx, a)
	})

	// The session API, for any agent (docs/http-api.md).
	for _, action := range service.SessionActions {
		handle("session/"+action, func(ctx context.Context, b []byte) (any, error) {
			var a service.SessionArgs
			if err := decodeInto(b, &a); err != nil {
				return nil, err
			}
			a.Action = action
			return api.Session(ctx, a)
		})
	}

	if extra != nil {
		extra(mux, info)
	}
	// Calls end with the server: a poll waiting for news answers at shutdown
	// instead of holding it up.
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx }}
	if err := writeInfo(infoPath, info); err != nil {
		ln.Close()
		return err
	}
	// The same at a fixed place, for clients that cannot read voice's config
	// to find its state directory (the Claude Code mod).
	discovery := DiscoveryPath()
	if discovery != "" {
		writeInfo(discovery, info)
	}
	defer func() {
		// Only remove the files if they still describe this process.
		for _, p := range []string{infoPath, discovery} {
			if cur, err := ReadInfo(p); p != "" && err == nil && cur.PID == info.PID {
				os.Remove(p)
			}
		}
	}()
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(shut)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// DiscoveryPath is where the running background process is described for
// other programs: voice\daemon.json in the user's config folder (%APPDATA%
// on Windows, ~/.config elsewhere).
func DiscoveryPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "voice", "daemon.json")
}

func writeInfo(path string, info Info) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, _ := json.Marshal(info)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func ReadInfo(path string) (Info, error) {
	var info Info
	b, err := os.ReadFile(path)
	if err != nil {
		return info, err
	}
	return info, json.Unmarshal(b, &info)
}
