package main

import (
	"os"
	"testing"
	"time"
)

// TestTagDeleteCascade guards against a regression where device_tags rows
// were left orphaned after a tag delete (ON DELETE CASCADE requires
// PRAGMA foreign_keys=ON, which the DSN wasn't actually enabling — see
// OpenDB). An orphaned row silently reattaches itself if a tag with the
// same name is ever created again.
func TestTagDeleteCascade(t *testing.T) {
	f, err := os.CreateTemp("", "fleetman-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)
	defer os.Remove(path + "-shm")
	defer os.Remove(path + "-wal")

	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.InsertDevice("dev-1", "tok-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTag("prod"); err != nil {
		t.Fatal(err)
	}
	if err := db.AddTagToDevices("prod", []string{"dev-1"}); err != nil {
		t.Fatal(err)
	}

	if tags := db.GetDeviceTags("dev-1"); len(tags) != 1 {
		t.Fatalf("expected 1 tag after tagging, got %v", tags)
	}

	if !db.DeleteTag("prod") {
		t.Fatal("DeleteTag returned false")
	}
	if tags := db.GetDeviceTags("dev-1"); len(tags) != 0 {
		t.Fatalf("device still has tags after tag delete: %v", tags)
	}

	// Recreating the tag must not silently reattach the device via an
	// orphaned device_tags row.
	if err := db.CreateTag("prod"); err != nil {
		t.Fatal(err)
	}
	if tags := db.GetDeviceTags("dev-1"); len(tags) != 0 {
		t.Fatalf("device regained tag %v after tag was recreated", tags)
	}
}
