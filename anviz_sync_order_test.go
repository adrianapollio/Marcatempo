package main

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openAttendanceOrderTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite memory db: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})

	_, err = db.Exec(`
		CREATE TABLE records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			employee_id INTEGER NOT NULL,
			employee_name TEXT NOT NULL DEFAULT '',
			timestamp DATETIME NOT NULL,
			action TEXT NOT NULL,
			status_code INTEGER NOT NULL DEFAULT 0,
			source TEXT NOT NULL,
			device_id INTEGER,
			raw_device_timestamp INTEGER,
			latitude REAL,
			longitude REAL
		)
	`)
	if err != nil {
		t.Fatalf("create records table: %v", err)
	}

	return db
}

func TestSortAttendanceChunkRecordsChronologically(t *testing.T) {
	early := time.Date(2026, 4, 3, 13, 23, 30, 0, time.FixedZone("CEST", 2*60*60))
	late := time.Date(2026, 4, 3, 14, 5, 21, 0, time.FixedZone("CEST", 2*60*60))

	records := []attendanceChunkRecord{
		{UserID: 28, TimestampSecs: 828453921, RecordTime: late, StatusCode: 1, Action: "pausa"},
		{UserID: 28, TimestampSecs: 828451410, RecordTime: early, StatusCode: 1, Action: "pausa"},
	}

	sortAttendanceChunkRecordsChronologically(records)

	if !records[0].RecordTime.Equal(early) || !records[1].RecordTime.Equal(late) {
		t.Fatalf("expected ascending chronological order, got %s then %s", records[0].RecordTime.Format(time.RFC3339), records[1].RecordTime.Format(time.RFC3339))
	}
}

func TestChronologicalPauseResolutionWithinChunk(t *testing.T) {
	db := openAttendanceOrderTestDB(t)
	employeeID := 28
	employeeName := "SEPE"
	loc := time.FixedZone("CEST", 2*60*60)
	workStart := time.Date(2026, 4, 3, 8, 0, 0, 0, loc)
	firstPause := time.Date(2026, 4, 3, 13, 23, 30, 0, loc)
	secondPause := time.Date(2026, 4, 3, 14, 5, 21, 0, loc)

	inserted, err := insertRecordWithDeviceMetaUsing(db, employeeID, employeeName, workStart, "In", 0, "device", nil, nil, nil, nil)
	if err != nil || !inserted {
		t.Fatalf("seed In record failed inserted=%v err=%v", inserted, err)
	}

	chunkRecords := []attendanceChunkRecord{
		{UserID: uint32(employeeID), TimestampSecs: 828453921, RecordTime: secondPause, StatusCode: 1, Action: "pausa"},
		{UserID: uint32(employeeID), TimestampSecs: 828451410, RecordTime: firstPause, StatusCode: 1, Action: "pausa"},
	}

	sortAttendanceChunkRecordsChronologically(chunkRecords)

	gotActions := make([]string, 0, len(chunkRecords))
	for _, chunkRecord := range chunkRecords {
		resolution, err := resolveDeviceActionUsing(db, employeeID, chunkRecord.RecordTime, chunkRecord.Action, chunkRecord.StatusCode)
		if err != nil {
			t.Fatalf("resolve device action: %v", err)
		}

		gotActions = append(gotActions, resolution.FinalAction)
		inserted, err := insertRecordWithDeviceMetaUsing(db, employeeID, employeeName, chunkRecord.RecordTime, resolution.FinalAction, resolution.FinalStatusCode, "device", nil, nil, nil, nil)
		if err != nil || !inserted {
			t.Fatalf("insert resolved record failed inserted=%v err=%v", inserted, err)
		}
	}

	if gotActions[0] != "I_pausa" || gotActions[1] != "F_pausa" {
		t.Fatalf("expected pausa records to resolve as I_pausa then F_pausa, got %v", gotActions)
	}
}
