package service

import (
	"context"
	"strings"
	"testing"
)

func TestSessionStyle(t *testing.T) {
	ag := &Agents{}
	ag.Seen(7, "claude", "game")
	s := &Service{Agents: ag}
	if got, err := s.Control(context.Background(), ControlArgs{Action: "set_style", Style: "narrative", PID: 7}); err != nil || !strings.Contains(got.(string), "narrative") {
		t.Fatalf("set narrative: %v %v", got, err)
	}
	if ag.Style(7) != StyleNarrative || ag.Style(8) != "" {
		t.Fatalf("style: %q / %q", ag.Style(7), ag.Style(8))
	}
	s.Control(context.Background(), ControlArgs{Action: "set_style", Style: "short", PID: 7})
	if ag.Style(7) != "" {
		t.Fatal("short did not end narrative")
	}
	if _, err := s.Control(context.Background(), ControlArgs{Action: "set_style", Style: "loud", PID: 7}); err == nil {
		t.Fatal("an unknown style was taken")
	}
}
