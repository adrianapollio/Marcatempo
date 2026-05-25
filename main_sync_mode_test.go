package main

import "testing"

func TestParseAttendanceModeOverride(t *testing.T) {
	mode, err := parseAttendanceModeOverride("new")
	if err != nil {
		t.Fatalf("expected no error for new, got %v", err)
	}
	if mode == nil || *mode != 0x02 {
		t.Fatalf("expected new-records override, got %#v", mode)
	}

	mode, err = parseAttendanceModeOverride("all")
	if err != nil {
		t.Fatalf("expected no error for all, got %v", err)
	}
	if mode == nil || *mode != 0x01 {
		t.Fatalf("expected all-records override, got %#v", mode)
	}

	mode, err = parseAttendanceModeOverride("")
	if err != nil {
		t.Fatalf("expected no error for empty override, got %v", err)
	}
	if mode != nil {
		t.Fatalf("expected nil override for empty input, got %#v", mode)
	}
}

func TestParseAttendanceModeOverrideRejectsInvalidValue(t *testing.T) {
	mode, err := parseAttendanceModeOverride("unexpected")
	if err == nil {
		t.Fatalf("expected error for invalid override, got mode %#v", mode)
	}
	if mode != nil {
		t.Fatalf("expected nil mode for invalid override, got %#v", mode)
	}
}
