package main

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openDeviceFinalRecordTestDB(t *testing.T) *sql.DB {
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

func TestAdoptLegacyDeviceRecordUsingCanonicalizesAction(t *testing.T) {
	db := openDeviceFinalRecordTestDB(t)
	ts := time.Date(2026, 4, 3, 10, 12, 24, 0, time.FixedZone("CEST", 2*60*60))

	_, err := db.Exec(`
		INSERT INTO records (employee_id, employee_name, timestamp, action, status_code, source)
		VALUES (?, ?, ?, ?, ?, ?)
	`, 156, "Utente Test 156", ts.Format(time.RFC3339), "pausa", 2, "device")
	if err != nil {
		t.Fatalf("seed legacy record: %v", err)
	}

	adopted, err := adoptLegacyDeviceRecordUsing(db, 2, 156, ts, 828430000, "I_pausa", 2)
	if err != nil {
		t.Fatalf("adopt legacy record: %v", err)
	}
	if !adopted {
		t.Fatal("expected legacy record to be adopted")
	}

	var action string
	var statusCode int
	var deviceID int
	var rawTS int64
	err = db.QueryRow(`
		SELECT action, status_code, device_id, raw_device_timestamp
		FROM records
		WHERE employee_id = ? AND timestamp = ?
	`, 156, ts.Format(time.RFC3339)).Scan(&action, &statusCode, &deviceID, &rawTS)
	if err != nil {
		t.Fatalf("read adopted record: %v", err)
	}

	if action != "I_pausa" || statusCode != 2 {
		t.Fatalf("expected adopted record to become I_pausa/2, got %s/%d", action, statusCode)
	}
	if deviceID != 2 || rawTS != 828430000 {
		t.Fatalf("expected adopted record to store device metadata 2/828430000, got %d/%d", deviceID, rawTS)
	}
}

func TestNormalizeDeviceFinalActionsUsingRepairsStoredLegacyActionNames(t *testing.T) {
	db := openDeviceFinalRecordTestDB(t)
	ts := time.Date(2026, 4, 3, 10, 13, 51, 0, time.FixedZone("CEST", 2*60*60))

	_, err := db.Exec(`
		INSERT INTO records (employee_id, employee_name, timestamp, action, status_code, source, device_id, raw_device_timestamp)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, 156, "Utente Test 156", ts.Format(time.RFC3339), "pausa", 3, "device", 2, 828430001)
	if err != nil {
		t.Fatalf("seed device record: %v", err)
	}

	if err := normalizeDeviceFinalActionsUsing(db); err != nil {
		t.Fatalf("normalize device final actions: %v", err)
	}

	var action string
	err = db.QueryRow(`
		SELECT action
		FROM records
		WHERE employee_id = ? AND timestamp = ?
	`, 156, ts.Format(time.RFC3339)).Scan(&action)
	if err != nil {
		t.Fatalf("read normalized record: %v", err)
	}

	if action != "F_pausa" {
		t.Fatalf("expected normalized action F_pausa, got %s", action)
	}
}
