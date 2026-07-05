package main

import (
	"path/filepath"
	"testing"

	"github.com/canavan-a/fleetman/internal/api"
)

func TestTUIStateSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tui_state.yaml")

	want := &TUIState{
		SelectedDeviceIDs: []string{"dev-1", "dev-2"},
		ActiveTag:         "prod",
		Compact:           true,
	}
	if err := SaveTUIState(path, want); err != nil {
		t.Fatalf("SaveTUIState: %v", err)
	}

	got, err := LoadTUIState(path)
	if err != nil {
		t.Fatalf("LoadTUIState: %v", err)
	}
	if got.ActiveTag != want.ActiveTag || got.Compact != want.Compact || len(got.SelectedDeviceIDs) != len(want.SelectedDeviceIDs) {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestLoadTUIStateMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.yaml")

	got, err := LoadTUIState(path)
	if err != nil {
		t.Fatalf("LoadTUIState: %v", err)
	}
	if got == nil || len(got.SelectedDeviceIDs) != 0 || got.ActiveTag != "" || got.Compact {
		t.Fatalf("expected empty state for missing file, got %+v", got)
	}
}

// TestPendingSelectionReconciledAndStaleDropped guards the restore behavior:
// device IDs loaded from disk that no longer exist should be silently
// dropped once the first live device list arrives, and reconciliation
// should only run once.
func TestPendingSelectionReconciledAndStaleDropped(t *testing.T) {
	m := mainModel{
		selected:           make(map[string]api.Device),
		pendingSelectedIDs: []string{"dev-1", "dev-stale"},
	}

	updated, _ := m.Update(dataMsg{
		tags: nil,
		devices: []api.Device{
			{DeviceID: "dev-1"},
			{DeviceID: "dev-2"},
		},
	})
	m = updated.(mainModel)

	if m.pendingSelectedIDs != nil {
		t.Fatalf("expected pendingSelectedIDs cleared after reconciliation, got %v", m.pendingSelectedIDs)
	}
	if _, ok := m.selected["dev-1"]; !ok {
		t.Fatalf("expected dev-1 to be reconciled into selection")
	}
	if _, ok := m.selected["dev-stale"]; ok {
		t.Fatalf("expected stale device id to be silently dropped")
	}
	if len(m.selected) != 1 {
		t.Fatalf("expected exactly 1 selected device, got %d", len(m.selected))
	}
}
