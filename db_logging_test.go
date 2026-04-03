package main

import (
	"bytes"
	"database/sql"
	"log"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openDeviceRawInsertTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite memory db: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})

	_, err = db.Exec(`
		CREATE TABLE device_raw_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			device_id INTEGER NOT NULL,
			employee_id INTEGER NOT NULL,
			employee_name TEXT,
			raw_device_timestamp INTEGER NOT NULL,
			parsed_timestamp TEXT NOT NULL,
			action TEXT NOT NULL,
			status_code INTEGER NOT NULL,
			imported_at TEXT NOT NULL
		)
	`)
	if err != nil {
		t.Fatalf("create device_raw_records: %v", err)
	}

	_, err = db.Exec(`
		CREATE UNIQUE INDEX idx_device_raw_unique
		ON device_raw_records(device_id, employee_id, raw_device_timestamp, status_code)
	`)
	if err != nil {
		t.Fatalf("create device_raw unique index: %v", err)
	}

	return db
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
	})

	return &buf
}

func TestInsertDeviceRawRecordUsingLogsDuplicateByDefault(t *testing.T) {
	db := openDeviceRawInsertTestDB(t)
	logs := captureLogs(t)
	ts := time.Date(2026, 4, 3, 9, 15, 0, 0, time.FixedZone("CEST", 2*60*60))

	if _, err := insertDeviceRawRecordUsing(db, 2, 156, "Mario Rossi", ts, 828400000, "In", 0); err != nil {
		t.Fatalf("first insert failed: %v", err)
	}

	if _, err := insertDeviceRawRecordUsing(db, 2, 156, "Mario Rossi", ts, 828400000, "In", 0); err != ErrDuplicateRecord {
		t.Fatalf("expected ErrDuplicateRecord, got %v", err)
	}

	if !strings.Contains(logs.String(), "DEDUPE device_raw duplicate") {
		t.Fatalf("expected duplicate log, got %q", logs.String())
	}
}

func TestInsertDeviceRawRecordUsingWithOptionsSuppressesDuplicateLog(t *testing.T) {
	db := openDeviceRawInsertTestDB(t)
	logs := captureLogs(t)
	ts := time.Date(2026, 4, 3, 9, 20, 0, 0, time.FixedZone("CEST", 2*60*60))

	if _, err := insertDeviceRawRecordUsingWithOptions(db, 2, 156, "Mario Rossi", ts, 828400001, "Out", 1, deviceRawInsertOptions{logIndividual: false}); err != nil {
		t.Fatalf("first insert failed: %v", err)
	}

	if _, err := insertDeviceRawRecordUsingWithOptions(db, 2, 156, "Mario Rossi", ts, 828400001, "Out", 1, deviceRawInsertOptions{logIndividual: false}); err != ErrDuplicateRecord {
		t.Fatalf("expected ErrDuplicateRecord, got %v", err)
	}

	if strings.Contains(logs.String(), "DEDUPE device_raw duplicate") {
		t.Fatalf("expected duplicate log to be suppressed, got %q", logs.String())
	}
}
