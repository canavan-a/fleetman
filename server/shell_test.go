package main

import (
	"testing"
	"time"
)

func TestReapIdle_ClosesLiveIdleSession(t *testing.T) {
	s := NewShellStore()
	s.Open("sess-1", "device-1")
	s.sessions["sess-1"].lastUsed = time.Now().Add(-time.Hour)

	expired := s.ReapIdle(15*time.Minute, 2*time.Minute)
	if len(expired) != 1 || expired[0].sessionID != "sess-1" || expired[0].deviceID != "device-1" {
		t.Fatalf("expected sess-1 to be reported idle, got %+v", expired)
	}
	// A live idle session isn't deleted yet — the caller still needs to
	// tell the agent to close it; ReapIdle only reports it.
	if s.Get("sess-1") == nil {
		t.Error("live idle session should not be deleted by ReapIdle itself")
	}
}

func TestReapIdle_LeavesActiveSessionAlone(t *testing.T) {
	s := NewShellStore()
	s.Open("sess-1", "device-1")

	expired := s.ReapIdle(15*time.Minute, 2*time.Minute)
	if len(expired) != 0 {
		t.Fatalf("expected no expired sessions, got %+v", expired)
	}
}

func TestReapIdle_TouchResetsIdleClock(t *testing.T) {
	s := NewShellStore()
	s.Open("sess-1", "device-1")
	s.sessions["sess-1"].lastUsed = time.Now().Add(-time.Hour)

	s.Touch("sess-1")

	expired := s.ReapIdle(15*time.Minute, 2*time.Minute)
	if len(expired) != 0 {
		t.Fatalf("expected Touch to reset idle clock, got expired=%+v", expired)
	}
}

func TestReapIdle_PrunesStaleClosedSession(t *testing.T) {
	s := NewShellStore()
	s.Open("sess-1", "device-1")
	s.AppendOutput("sess-1", "stdout", "", true) // marks closed, sets lastUsed=now
	s.sessions["sess-1"].lastUsed = time.Now().Add(-time.Hour)

	expired := s.ReapIdle(15*time.Minute, 2*time.Minute)
	if len(expired) != 0 {
		t.Fatalf("a closed session should not be reported as needing a close, got %+v", expired)
	}
	if s.Get("sess-1") != nil {
		t.Error("stale closed session should have been pruned")
	}
}

func TestReapIdle_KeepsRecentlyClosedSession(t *testing.T) {
	s := NewShellStore()
	s.Open("sess-1", "device-1")
	s.AppendOutput("sess-1", "stdout", "", true)

	s.ReapIdle(15*time.Minute, 2*time.Minute)
	if s.Get("sess-1") == nil {
		t.Error("recently closed session should still be around for the master to poll")
	}
}
