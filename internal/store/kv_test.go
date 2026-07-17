package store

import (
	"errors"
	"testing"
)

func TestMetaRoundtripAndOverwrite(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)

	if _, err := s.GetMeta("boot_id"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing meta: err = %v, want ErrNotFound", err)
	}
	if err := s.SetMeta("boot_id", "b-1"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	if err := s.SetMeta("boot_id", "b-2"); err != nil {
		t.Fatalf("overwrite SetMeta: %v", err)
	}
	if v, err := s.GetMeta("boot_id"); err != nil || v != "b-2" {
		t.Fatalf("GetMeta = %q, %v; want b-2", v, err)
	}

	// Boot-id continuity survives restart.
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	s2 := openStore(t, dir)
	if v, err := s2.GetMeta("boot_id"); err != nil || v != "b-2" {
		t.Fatalf("GetMeta after reopen = %q, %v; want b-2", v, err)
	}
}

func TestCursorRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)

	if _, err := s.GetCursor("cli", "s1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing cursor: err = %v, want ErrNotFound", err)
	}
	if err := s.SetCursor("cli", "s1", 10); err != nil {
		t.Fatalf("SetCursor: %v", err)
	}
	if err := s.SetCursor("cli", "s1", 25); err != nil {
		t.Fatalf("advance SetCursor: %v", err)
	}
	if err := s.SetCursor("cli", "s2", 7); err != nil {
		t.Fatalf("second session SetCursor: %v", err)
	}
	if err := s.SetCursor("tui", "s1", 3); err != nil {
		t.Fatalf("second client SetCursor: %v", err)
	}

	if seq, err := s.GetCursor("cli", "s1"); err != nil || seq != 25 {
		t.Fatalf("GetCursor(cli,s1) = %d, %v; want 25", seq, err)
	}
	if seq, err := s.GetCursor("cli", "s2"); err != nil || seq != 7 {
		t.Fatalf("GetCursor(cli,s2) = %d, %v; want 7", seq, err)
	}
	if seq, err := s.GetCursor("tui", "s1"); err != nil || seq != 3 {
		t.Fatalf("GetCursor(tui,s1) = %d, %v; want 3", seq, err)
	}

	// Cursor re-establishment works across restart (ADR-0005 durability row).
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	s2 := openStore(t, dir)
	if seq, err := s2.GetCursor("cli", "s1"); err != nil || seq != 25 {
		t.Fatalf("GetCursor after reopen = %d, %v; want 25", seq, err)
	}
}
