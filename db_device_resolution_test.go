package main

import (
	"testing"
	"time"
)

func TestResolveGroupedDeviceActionInOut(t *testing.T) {
	action, status, note := resolveGroupedDeviceAction("in_out", nil)
	if action != "In" || status != 0 || note != "group_in_out_open" {
		t.Fatalf("expected first in_out to open work session, got action=%s status=%d note=%s", action, status, note)
	}

	timeline := []deviceTimelineRecord{
		{Timestamp: time.Now(), Action: "In", StatusCode: 0, Source: "device"},
	}
	action, status, note = resolveGroupedDeviceAction("in_out", timeline)
	if action != "Out" || status != 1 || note != "group_in_out_close" {
		t.Fatalf("expected second in_out to close work session, got action=%s status=%d note=%s", action, status, note)
	}
}

func TestResolveGroupedDeviceActionPausa(t *testing.T) {
	workdayTimeline := []deviceTimelineRecord{
		{Timestamp: time.Now(), Action: "In", StatusCode: 0, Source: "device"},
	}

	action, status, note := resolveGroupedDeviceAction("pausa", workdayTimeline)
	if action != "I_pausa" || status != 2 || note != "group_pausa_open" {
		t.Fatalf("expected pausa to open break, got action=%s status=%d note=%s", action, status, note)
	}

	workdayTimeline = append(workdayTimeline, deviceTimelineRecord{Timestamp: time.Now(), Action: "I_pausa", StatusCode: 2, Source: "device"})
	action, status, note = resolveGroupedDeviceAction("pausa", workdayTimeline)
	if action != "F_pausa" || status != 3 || note != "group_pausa_close" {
		t.Fatalf("expected second pausa to close break, got action=%s status=%d note=%s", action, status, note)
	}
}

func TestResolveGroupedDeviceActionInOutClosesWhenPauseAlreadyOpen(t *testing.T) {
	timeline := []deviceTimelineRecord{
		{Timestamp: time.Now(), Action: "In", StatusCode: 0, Source: "device"},
		{Timestamp: time.Now().Add(time.Minute), Action: "I_pausa", StatusCode: 2, Source: "device"},
	}

	action, status, note := resolveGroupedDeviceAction("in_out", timeline)
	if action != "Out" || status != 1 || note != "group_in_out_close_with_open_pause" {
		t.Fatalf("expected in_out with open pause to resolve Out/1, got action=%s status=%d note=%s", action, status, note)
	}
}

func TestResolveGroupedDeviceActionInOutClosesAfterPauseWithoutOpenSession(t *testing.T) {
	timeline := []deviceTimelineRecord{
		{Timestamp: time.Now(), Action: "I_pausa", StatusCode: 2, Source: "device"},
	}

	action, status, note := resolveGroupedDeviceAction("in_out", timeline)
	if action != "Out" || status != 1 || note != "group_in_out_close_after_pause_without_open_session" {
		t.Fatalf("expected in_out after standalone pause marker to resolve Out/1, got action=%s status=%d note=%s", action, status, note)
	}
}

func TestNormalizeDeviceRawActionLegacyCompatibility(t *testing.T) {
	if got := normalizeDeviceRawAction("inizio_pausa", -1); got != "I_pausa" {
		t.Fatalf("expected inizio_pausa to normalize to I_pausa, got %s", got)
	}
	if got := normalizeDeviceRawAction("pausa", -1); got != "pausa" {
		t.Fatalf("expected pausa grouped action to remain pausa, got %s", got)
	}
	if got := normalizeDeviceRawAction("", 5); got != "R_trasf" {
		t.Fatalf("expected legacy status fallback to preserve R_trasf, got %s", got)
	}
}

func TestNormalizeAnvizDeviceRawActionGroupedMode(t *testing.T) {
	t.Setenv("ANVIZ_SIMPLIFIED_DEVICE_RAW_ACTIONS", "true")

	cases := []struct {
		statusCode int
		legacy     string
		expected   string
	}{
		{0, "In", "in_out"},
		{1, "Out", "pausa"},
		{4, "U_trasf", "in_out"},
		{5, "R_trasf", "in_out"},
		{2, "I_pausa", "pausa"},
		{3, "F_pausa", "pausa"},
		{6, "I_break", "pausa"},
		{7, "F_break", "pausa"},
	}

	for _, tc := range cases {
		got := normalizeAnvizDeviceRawAction(tc.statusCode, tc.legacy)
		if got != tc.expected {
			t.Fatalf("status=%d legacy=%s expected %s got %s", tc.statusCode, tc.legacy, tc.expected, got)
		}
	}
}

func TestResolveGroupedDeviceActionPausaOnStatusOneSimplifiedFlow(t *testing.T) {
	t.Setenv("ANVIZ_SIMPLIFIED_DEVICE_RAW_ACTIONS", "true")

	workdayTimeline := []deviceTimelineRecord{
		{Timestamp: time.Now(), Action: "In", StatusCode: 0, Source: "device"},
	}

	raw := normalizeAnvizDeviceRawAction(1, "Out")
	if raw != "pausa" {
		t.Fatalf("expected simplified status 1 to normalize to pausa, got %s", raw)
	}

	action, status, note := resolveGroupedDeviceAction(raw, workdayTimeline)
	if action != "I_pausa" || status != 2 || note != "group_pausa_open" {
		t.Fatalf("expected simplified status 1 to open pausa, got action=%s status=%d note=%s", action, status, note)
	}
}

func TestNormalizeAnvizDeviceRawActionLegacyMode(t *testing.T) {
	t.Setenv("ANVIZ_SIMPLIFIED_DEVICE_RAW_ACTIONS", "false")

	if got := normalizeAnvizDeviceRawAction(4, "U_trasf"); got != "U_trasf" {
		t.Fatalf("expected legacy passthrough, got %s", got)
	}
}

func TestDecodeAttendanceActionPrefersPrimaryStatusByteAtOffset9(t *testing.T) {
	recordBytes := make([]byte, 10)
	recordBytes[8] = 1
	recordBytes[9] = 3

	status, action := decodeAttendanceAction(recordBytes)
	if status != 3 || action != "F_pausa" {
		t.Fatalf("expected conflicting status bytes to prefer offset 9 as F_pausa/3, got %s/%d", action, status)
	}
}

func TestDecodeAttendanceActionFallsBackToOffset8(t *testing.T) {
	recordBytes := make([]byte, 10)
	recordBytes[8] = 1
	recordBytes[9] = 12

	status, action := decodeAttendanceAction(recordBytes)
	if status != 1 || action != "Out" {
		t.Fatalf("expected fallback to offset 8 as Out/1, got %s/%d", action, status)
	}
}
