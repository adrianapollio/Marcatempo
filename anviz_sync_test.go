package main

import (
	"encoding/binary"
	"testing"
)

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

func TestAnvizLoginAttemptsTryZeroPasswordThenEmptyPayload(t *testing.T) {
	attempts := anvizLoginAttempts(3)
	if len(attempts) != 3 {
		t.Fatalf("expected 3 login attempts, got %d", len(attempts))
	}
	if attempts[0].Label != "configured-id-zero-password-4bytes" || attempts[0].PacketDeviceID != 3 || len(attempts[0].Data) != 4 {
		t.Fatalf("expected first login attempt to use configured ID with zero-password-4bytes, got %+v", attempts[0])
	}
	if attempts[1].Label != "configured-id-empty-payload" || attempts[1].PacketDeviceID != 3 || len(attempts[1].Data) != 0 {
		t.Fatalf("expected second login attempt to use configured ID with empty-payload, got %+v", attempts[1])
	}
	if attempts[2].Label != "broadcast-id-empty-payload" || attempts[2].PacketDeviceID != 0 || len(attempts[2].Data) != 0 {
		t.Fatalf("expected third login attempt to use broadcast ID with empty-payload, got %+v", attempts[2])
	}
}

func TestBuildAnvizLoginPacketPayloadLengths(t *testing.T) {
	withZeroPassword := BuildAnvizPacket(3, 0x38, make([]byte, 4))
	withEmptyPayload := BuildAnvizPacket(3, 0x38, nil)

	if got := binary.BigEndian.Uint16(withZeroPassword[6:8]); got != 4 {
		t.Fatalf("expected zero-password login payload length 4, got %d", got)
	}
	if got := binary.BigEndian.Uint16(withEmptyPayload[6:8]); got != 0 {
		t.Fatalf("expected empty login payload length 0, got %d", got)
	}
	if len(withZeroPassword) != len(withEmptyPayload)+4 {
		t.Fatalf("expected zero-password login packet to be 4 bytes longer, got %d and %d", len(withZeroPassword), len(withEmptyPayload))
	}
}
