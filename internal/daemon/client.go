package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"voice/internal/service"
)

// Client reaches the background process of this installation, starting or
// replacing it when needed. It implements service.API.
type Client struct {
	Exe        string // voice.exe of this installation
	ConfigPath string
	Version    string
	InfoPath   string

	info Info
}

var _ service.API = (*Client)(nil)

func (c *Client) do(ctx context.Context, info Info, method string, args any, timeout time.Duration) (any, error) {
	body, _ := json.Marshal(args)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+info.Addr+"/v1/"+method, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set(tokenHeader, info.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Result any    `json:"result"`
		Error  string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("voice background process: %s", resp.Status)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return out.Result, nil
	case http.StatusUnprocessableEntity:
		return nil, service.UserError{Msg: out.Error}
	}
	return nil, fmt.Errorf("voice background process: %s", out.Error)
}

func (c *Client) alive(ctx context.Context) (Info, bool) {
	info, err := ReadInfo(c.InfoPath)
	if err != nil {
		return info, false
	}
	res, err := c.do(ctx, info, "ping", nil, 2*time.Second)
	if err != nil {
		return info, false
	}
	m, _ := res.(map[string]any)
	if v, _ := m["version"].(string); v != c.Version {
		// Updated binary: retire the old process so the new code runs.
		c.do(ctx, info, "shutdown", nil, 2*time.Second)
		for i := 0; i < 50; i++ {
			if _, err := os.Stat(c.InfoPath); err != nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		return info, false
	}
	return info, true
}

// Ensure returns a running background process, starting one if needed.
func (c *Client) Ensure(ctx context.Context) (Info, error) {
	if c.info.Addr != "" {
		if _, err := c.do(ctx, c.info, "ping", nil, 2*time.Second); err == nil {
			return c.info, nil
		}
	}
	if info, ok := c.alive(ctx); ok {
		c.info = info
		return info, nil
	}
	if err := StartDetached(c.Exe, "serve", "-config", c.ConfigPath); err != nil {
		return Info{}, fmt.Errorf("start voice background process: %w", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return Info{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		if info, ok := c.alive(ctx); ok {
			c.info = info
			return info, nil
		}
	}
	return Info{}, errors.New("voice background process did not start; run `voice serve` to see why")
}

func (c *Client) call(ctx context.Context, method string, args any, timeout time.Duration) (any, error) {
	info, err := c.Ensure(ctx)
	if err != nil {
		return nil, err
	}
	return c.do(ctx, info, method, args, timeout)
}

func (c *Client) Speak(ctx context.Context, a service.SpeakArgs) (any, error) {
	timeout := 10 * time.Second
	if a.Wait {
		timeout = 3 * time.Minute
	}
	return c.call(ctx, "speak", a, timeout)
}

func (c *Client) Control(ctx context.Context, a service.ControlArgs) (any, error) {
	return c.call(ctx, "control", a, 10*time.Second)
}

func (c *Client) Listen(ctx context.Context, a service.ListenArgs) (any, error) {
	return c.call(ctx, "listen", a, 3*time.Minute)
}

func (c *Client) Clone(ctx context.Context, a service.CloneArgs) (any, error) {
	return c.call(ctx, "clone", a, 5*time.Minute)
}

// Agent reports a hook event. Unlike the other calls it never starts the
// background process: if none runs, nothing was dictated, and hooks that run
// on every tool call must stay fast.
func (c *Client) Agent(ctx context.Context, a service.AgentArgs) (any, error) {
	info, err := ReadInfo(c.InfoPath)
	if err != nil {
		return []any{}, nil
	}
	return c.do(ctx, info, "agent", a, 2*time.Second)
}

// Mod asks for a session's dictated messages, as its Claude Code mod does.
func (c *Client) Mod(ctx context.Context, a service.ModArgs) (any, error) {
	return c.call(ctx, "mod", a, 5*time.Second)
}

func (c *Client) Dictate(ctx context.Context, a service.DictateArgs) (any, error) {
	return c.call(ctx, "dictate", a, 30*time.Second)
}

// Stop asks a running background process to exit.
func (c *Client) Stop(ctx context.Context) error {
	info, err := ReadInfo(c.InfoPath)
	if err != nil {
		return errors.New("not running")
	}
	_, err = c.do(ctx, info, "shutdown", nil, 3*time.Second)
	return err
}

// Status reports the running process without starting one.
func (c *Client) Status(ctx context.Context) (Info, bool) {
	info, err := ReadInfo(c.InfoPath)
	if err != nil {
		return info, false
	}
	_, err = c.do(ctx, info, "ping", nil, 2*time.Second)
	return info, err == nil
}
