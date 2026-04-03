package main

import "testing"

func TestShouldSyncStaffDefaultsDisabled(t *testing.T) {
	t.Setenv("ANVIZ_AUTO_SYNC_STAFF", "")
	t.Setenv("ANVIZ_MANUAL_SYNC_STAFF", "")

	if shouldSyncStaff(false) {
		t.Fatalf("expected automatic staff sync to default to disabled")
	}

	if shouldSyncStaff(true) {
		t.Fatalf("expected manual staff sync to default to disabled")
	}
}

func TestShouldSyncStaffHonorsExplicitTrue(t *testing.T) {
	t.Setenv("ANVIZ_AUTO_SYNC_STAFF", "true")
	t.Setenv("ANVIZ_MANUAL_SYNC_STAFF", "true")

	if !shouldSyncStaff(false) {
		t.Fatalf("expected automatic staff sync to honor explicit true")
	}

	if !shouldSyncStaff(true) {
		t.Fatalf("expected manual staff sync to honor explicit true")
	}
}
