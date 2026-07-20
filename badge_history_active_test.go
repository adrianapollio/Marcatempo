package main

import (
	"database/sql"
	"os"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openBadgeHistoryActiveTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE people (
			id INTEGER PRIMARY KEY,
			person_key TEXT NOT NULL UNIQUE,
			display_name TEXT NOT NULL,
			is_active INTEGER NOT NULL
		);
		CREATE TABLE person_badge_history (
			id INTEGER PRIMARY KEY,
			person_id INTEGER NOT NULL,
			anviz_employee_id INTEGER NOT NULL,
			valid_from_utc INTEGER NOT NULL,
			valid_to_utc INTEGER,
			voided_at_utc INTEGER
		);
		CREATE TABLE employees (
			id INTEGER PRIMARY KEY,
			name TEXT,
			is_admin INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE records (
			id INTEGER PRIMARY KEY,
			employee_id INTEGER NOT NULL,
			employee_name TEXT NOT NULL,
			timestamp TEXT NOT NULL,
			action TEXT NOT NULL,
			status_code INTEGER NOT NULL,
			source TEXT NOT NULL,
			latitude REAL,
			longitude REAL
		);
	`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO people (id, person_key, display_name, is_active) VALUES
			(1, 'PERSON-A', 'Persona A', 1),
			(2, 'PERSON-B', 'Persona B', 1);
		INSERT INTO person_badge_history
			(id, person_id, anviz_employee_id, valid_from_utc, valid_to_utc, voided_at_utc)
		VALUES
			(1, 1, 10, 0, 100, NULL),
			(2, 1, 20, 100, NULL, NULL),
			(3, 2, 10, 200, NULL, NULL);
		INSERT INTO employees (id, name, is_admin) VALUES
			(10, 'Persona B', 0),
			(20, 'Persona A', 1),
			(156, 'Utente test', 0);
	`)
	if err != nil {
		t.Fatalf("seed resolver: %v", err)
	}

	format := func(value int64) string { return time.Unix(value, 0).UTC().Format(time.RFC3339) }
	_, err = db.Exec(`
		INSERT INTO records (id, employee_id, employee_name, timestamp, action, status_code, source)
		VALUES
			(1, 10, 'Vecchio A', ?, 'In', 0, 'device'),
			(2, 20, 'A', ?, 'Out', 1, 'device'),
			(3, 10, 'Nuovo B', ?, 'In', 0, 'device'),
			(4, 156, 'Utente test', ?, 'In', 0, 'device')
	`, format(50), format(150), format(250), format(250))
	if err != nil {
		t.Fatalf("seed records: %v", err)
	}
	return db
}

func withActiveBadgeHistoryDB(t *testing.T, db *sql.DB) {
	t.Helper()
	previousDB := DB
	previousMode, hadMode := os.LookupEnv("BADGE_HISTORY_MODE")
	DB = db
	if err := os.Setenv("BADGE_HISTORY_MODE", "active"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		DB = previousDB
		if hadMode {
			_ = os.Setenv("BADGE_HISTORY_MODE", previousMode)
		} else {
			_ = os.Unsetenv("BADGE_HISTORY_MODE")
		}
	})
}

func TestActiveResolutionSeparatesReusedAnvizID(t *testing.T) {
	db := openBadgeHistoryActiveTestDB(t)
	withActiveBadgeHistoryDB(t, db)

	records, err := getBadgeHistoryActiveRecords("", "", "", "PERSON-A")
	if err != nil {
		t.Fatalf("records person A: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("person A records=%d, want 2", len(records))
	}
	for _, record := range records {
		if record.PersonKey != "PERSON-A" || record.EmployeeID != 20 || record.EmployeeName != "Persona A" {
			t.Fatalf("unexpected resolved record: %+v", record)
		}
	}
	if records[0].AnvizEmployeeID != 10 || records[1].AnvizEmployeeID != 20 {
		t.Fatalf("original Anviz IDs not preserved: %+v", records)
	}

	reused, err := getBadgeHistoryActiveRecords("", "", "", "PERSON-B")
	if err != nil {
		t.Fatalf("records person B: %v", err)
	}
	if len(reused) != 1 || reused[0].ID != 3 || reused[0].PersonKey != "PERSON-B" {
		t.Fatalf("reused ID mixed owners: %+v", reused)
	}
}

func TestActiveResolutionKeepsUnmanagedEmployeeFallback(t *testing.T) {
	db := openBadgeHistoryActiveTestDB(t)
	withActiveBadgeHistoryDB(t, db)

	records, err := getBadgeHistoryActiveRecords("", "", "156", "")
	if err != nil {
		t.Fatalf("legacy fallback: %v", err)
	}
	if len(records) != 1 || records[0].EmployeeID != 156 || records[0].PersonID != nil {
		t.Fatalf("unexpected legacy fallback: %+v", records)
	}
}

func TestResolveBadgeForPersonUsesRequestedTimestamp(t *testing.T) {
	db := openBadgeHistoryActiveTestDB(t)
	withActiveBadgeHistoryDB(t, db)

	id, name, err := resolveBadgeForPersonAt("PERSON-A", time.Unix(50, 0).UTC())
	if err != nil || id != 10 || name != "Persona A" {
		t.Fatalf("historical badge: id=%d name=%q err=%v", id, name, err)
	}
	id, _, err = resolveBadgeForPersonAt("PERSON-A", time.Unix(100, 0).UTC())
	if err != nil || id != 20 {
		t.Fatalf("half-open boundary: id=%d err=%v", id, err)
	}
}

func TestActiveEmployeesExposeStableIdentityAndLegacyFallback(t *testing.T) {
	db := openBadgeHistoryActiveTestDB(t)
	withActiveBadgeHistoryDB(t, db)

	employees, err := getBadgeHistoryActiveEmployees()
	if err != nil {
		t.Fatalf("employees: %v", err)
	}
	found := make(map[string]Employee)
	for _, employee := range employees {
		key := employee.PersonKey
		if key == "" {
			key = "legacy"
		}
		found[key] = employee
	}
	if found["PERSON-A"].ID != 20 || !found["PERSON-A"].IsAdmin {
		t.Fatalf("person A presentation: %+v", found["PERSON-A"])
	}
	if found["PERSON-B"].ID != 10 {
		t.Fatalf("person B presentation: %+v", found["PERSON-B"])
	}
	if found["legacy"].ID != 156 {
		t.Fatalf("legacy employee missing: %+v", found)
	}
}

func TestEmployeeHistoryUsesCurrentOwnerOfReusedID(t *testing.T) {
	db := openBadgeHistoryActiveTestDB(t)
	withActiveBadgeHistoryDB(t, db)

	records, err := GetEmployeeRecords(10, 10)
	if err != nil {
		t.Fatalf("employee history: %v", err)
	}
	if len(records) != 1 || records[0].ID != 3 || records[0].PersonKey != "PERSON-B" {
		t.Fatalf("current owner received another person's history: %+v", records)
	}
}

func TestActiveTimelineIgnoresPreviousOwnerOfSameID(t *testing.T) {
	db := openBadgeHistoryActiveTestDB(t)
	withActiveBadgeHistoryDB(t, db)

	timeline, managed, err := loadBadgeHistoryActiveTimelineBeforeUsing(db, 10, time.Unix(300, 0).UTC())
	if err != nil {
		t.Fatalf("active timeline: %v", err)
	}
	if !managed || len(timeline) != 1 || timeline[0].Timestamp.Unix() != 250 {
		t.Fatalf("timeline mixed previous owner: managed=%t timeline=%+v", managed, timeline)
	}
}

func TestResolverRejectsOverlappingPersonAssignments(t *testing.T) {
	db := openBadgeHistoryActiveTestDB(t)
	_, err := db.Exec(`
		UPDATE person_badge_history SET valid_to_utc = 150 WHERE id = 1
	`)
	if err != nil {
		t.Fatalf("create overlap: %v", err)
	}
	if _, err := loadBadgeHistoryResolverUsing(db); err == nil {
		t.Fatal("expected overlapping person assignments to be rejected")
	}
}
