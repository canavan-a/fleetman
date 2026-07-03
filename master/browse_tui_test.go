package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/canavan-a/fleetman/internal/api"
)

// TestTagCursorClampedAfterTagShrink guards against a panic where deleting
// a tag while the cursor sat on the last (or a since-removed) tag row left
// tagCursor pointing past the end of the refreshed, shorter tags slice —
// the next keypress touching tagName() indexed out of range.
func TestTagCursorClampedAfterTagShrink(t *testing.T) {
	m := mainModel{
		focus:     paneTags,
		tags:      []string{"a", "b", "c", "d", "e", "f", "g"},
		tagCursor: 8, // was on the last real tag before it got deleted
		selected:  make(map[string]api.Device),
	}

	// Simulate the tag list shrinking after a delete + refresh.
	updated, _ := m.Update(dataMsg{tags: []string{"a", "b", "c", "d", "e", "f"}, devices: nil})
	m = updated.(mainModel)

	// This used to panic: index out of range.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked: %v", r)
		}
	}()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
}
