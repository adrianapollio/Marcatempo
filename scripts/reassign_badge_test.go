package main

import (
	"bytes"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestListCurrentAssignmentsContainsNoPINData(t *testing.T) {
	path := createBadgeAdminTestDB(t)
	db, err := openDatabase(path, true)
	if err != nil {
		t.Fatalf("open list: %v", err)
	}
	defer db.Close()

	var output bytes.Buffer
	if err := writeCurrentAssignmentsCSV(db, &output); err != nil {
		t.Fatalf("write inventory: %v", err)
	}
	text := output.String()
	if !bytes.Contains(output.Bytes(), []byte("PERSON-A,Alice,true,10,100")) {
		t.Fatalf("missing current assignment: %s", text)
	}
	if bytes.Contains(bytes.ToLower(output.Bytes()), []byte("pin")) {
		t.Fatalf("inventory must not contain PIN fields: %s", text)
	}
}

func createBadgeAdminTestDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "attendance.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		PRAGMA foreign_keys = ON;
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at_utc INTEGER NOT NULL
		);
		INSERT INTO schema_migrations VALUES (1, 'people_and_person_badge_history', 1);

		CREATE TABLE employees (
			id INTEGER PRIMARY KEY,
			name TEXT
		);
		CREATE TABLE people (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			person_key TEXT NOT NULL UNIQUE,
			display_name TEXT NOT NULL,
			is_active INTEGER NOT NULL,
			inactive_at_utc INTEGER,
			created_at_utc INTEGER NOT NULL,
			updated_at_utc INTEGER NOT NULL
		);
		CREATE TABLE person_badge_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			person_id INTEGER NOT NULL,
			anviz_employee_id INTEGER NOT NULL,
			card_serial INTEGER NOT NULL,
			valid_from_utc INTEGER NOT NULL,
			valid_to_utc INTEGER,
			boundary_quality TEXT NOT NULL,
			reason TEXT NOT NULL,
			created_by TEXT NOT NULL,
			created_at_utc INTEGER NOT NULL,
			voided_at_utc INTEGER,
			void_reason TEXT,
			FOREIGN KEY (person_id) REFERENCES people(id)
		);
		CREATE TABLE records (
			id INTEGER PRIMARY KEY,
			employee_id INTEGER NOT NULL,
			timestamp TEXT NOT NULL
		);
		CREATE TABLE device_raw_records (
			id INTEGER PRIMARY KEY,
			employee_id INTEGER NOT NULL,
			parsed_timestamp TEXT NOT NULL
		);

		INSERT INTO employees (id, name) VALUES (10, 'Alice'), (20, 'Bob'), (30, 'Carol');
		INSERT INTO people (id, person_key, display_name, is_active, created_at_utc, updated_at_utc)
		VALUES (1, 'PERSON-A', 'Alice', 1, 1, 1), (2, 'PERSON-B', 'Bob', 1, 1, 1);
		INSERT INTO person_badge_history (
			id, person_id, anviz_employee_id, card_serial, valid_from_utc, valid_to_utc,
			boundary_quality, reason, created_by, created_at_utc
		) VALUES
			(1, 1, 10, 100, 0, NULL, 'exact', 'initial', 'seed', 1),
			(2, 2, 20, 200, 0, NULL, 'exact', 'initial', 'seed', 1);
		INSERT INTO records (id, employee_id, timestamp)
		VALUES (1, 20, '1970-01-01T00:20:00Z');
		INSERT INTO device_raw_records (id, employee_id, parsed_timestamp)
		VALUES (1, 20, '1970-01-01T00:20:00Z');
	`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return path
}

func badgeAdminTestConfig(path string) commandConfig {
	return commandConfig{
		DBPath:          path,
		PersonKey:       "PERSON-B",
		AnvizEmployeeID: 20,
		CardSerial:      100,
		EffectiveAt:     time.Unix(1000, 0).UTC(),
		Reason:          "reassigned",
		Actor:           "test",
		Apply:           true,
	}
}

func TestApplyReassignmentClosesOldCardAndTargetBadgeAtomically(t *testing.T) {
	path := createBadgeAdminTestDB(t)
	cfg := badgeAdminTestConfig(path)

	db, err := openDatabase(path, true)
	if err != nil {
		t.Fatalf("open preflight: %v", err)
	}
	plan, err := buildPlan(db, cfg)
	db.Close()
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if len(plan.CloseAssignments) != 2 || plan.KeepAssignment != nil || len(plan.ConflictingFuture) != 0 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if plan.AffectedRecords != 1 || plan.AffectedRawRecords != 1 {
		t.Fatalf("unexpected affected counts: records=%d raw=%d", plan.AffectedRecords, plan.AffectedRawRecords)
	}

	result, err := applyReassignment(cfg)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if result.ClosedCount != 2 || !result.AssignmentCreated || result.PersonCreated {
		t.Fatalf("unexpected result: %+v", result)
	}

	db, err = openDatabase(path, true)
	if err != nil {
		t.Fatalf("open result: %v", err)
	}
	defer db.Close()
	var closed int
	if err := db.QueryRow(`SELECT COUNT(*) FROM person_badge_history WHERE id IN (1, 2) AND valid_to_utc = 1000`).Scan(&closed); err != nil {
		t.Fatalf("count closed: %v", err)
	}
	if closed != 2 {
		t.Fatalf("expected 2 closed assignments, got %d", closed)
	}
	var created int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM person_badge_history
		WHERE person_id = 2 AND anviz_employee_id = 20 AND card_serial = 100
		  AND valid_from_utc = 1000 AND valid_to_utc IS NULL
	`).Scan(&created); err != nil {
		t.Fatalf("count created: %v", err)
	}
	if created != 1 {
		t.Fatalf("expected new assignment, got %d", created)
	}
	var auditRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM badge_assignment_audit`).Scan(&auditRows); err != nil {
		t.Fatalf("audit count: %v", err)
	}
	if auditRows != 3 {
		t.Fatalf("expected 3 audit rows, got %d", auditRows)
	}
}

func TestReassignmentIsIdempotentAfterApply(t *testing.T) {
	path := createBadgeAdminTestDB(t)
	cfg := badgeAdminTestConfig(path)
	if _, err := applyReassignment(cfg); err != nil {
		t.Fatalf("first apply: %v", err)
	}

	db, err := openDatabase(path, true)
	if err != nil {
		t.Fatalf("open rerun: %v", err)
	}
	plan, err := buildPlan(db, cfg)
	db.Close()
	if err != nil {
		t.Fatalf("rerun plan: %v", err)
	}
	if plan.KeepAssignment == nil || len(plan.CloseAssignments) != 0 || len(plan.ConflictingFuture) != 0 {
		t.Fatalf("unexpected rerun plan: %+v", plan)
	}

	result, err := applyReassignment(cfg)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if result.ClosedCount != 0 || result.AssignmentCreated || result.PersonCreated {
		t.Fatalf("second apply changed data: %+v", result)
	}
}

func TestPlanBlocksFutureConflictingAssignment(t *testing.T) {
	path := createBadgeAdminTestDB(t)
	db, err := openDatabase(path, false)
	if err != nil {
		t.Fatalf("open seed: %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO person_badge_history (
			person_id, anviz_employee_id, card_serial, valid_from_utc, valid_to_utc,
			boundary_quality, reason, created_by, created_at_utc
		) VALUES (1, 10, 300, 2000, NULL, 'exact', 'replacement', 'seed', 1)
	`)
	db.Close()
	if err != nil {
		t.Fatalf("seed future: %v", err)
	}

	cfg := badgeAdminTestConfig(path)
	cfg.CardSerial = 300
	db, err = openDatabase(path, true)
	if err != nil {
		t.Fatalf("open plan: %v", err)
	}
	plan, err := buildPlan(db, cfg)
	db.Close()
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.ConflictingFuture) != 1 {
		t.Fatalf("expected one future conflict, got %+v", plan.ConflictingFuture)
	}
}

func TestApplyCanCreateNewPerson(t *testing.T) {
	path := createBadgeAdminTestDB(t)
	cfg := badgeAdminTestConfig(path)
	cfg.PersonKey = "PERSON-C"
	cfg.DisplayName = "Carol"
	cfg.CreatePerson = true
	cfg.AnvizEmployeeID = 30
	cfg.CardSerial = 100

	result, err := applyReassignment(cfg)
	if err != nil {
		t.Fatalf("apply create person: %v", err)
	}
	if !result.PersonCreated || !result.AssignmentCreated || result.ClosedCount != 1 {
		t.Fatalf("unexpected create result: %+v", result)
	}

	result, err = applyReassignment(cfg)
	if err != nil {
		t.Fatalf("idempotent create-person rerun: %v", err)
	}
	if result.PersonCreated || result.AssignmentCreated || result.ClosedCount != 0 {
		t.Fatalf("create-person rerun changed data: %+v", result)
	}
}

func TestInactivePersonRequiresExplicitReactivation(t *testing.T) {
	path := createBadgeAdminTestDB(t)
	db, err := openDatabase(path, false)
	if err != nil {
		t.Fatalf("open seed: %v", err)
	}
	_, err = db.Exec(`UPDATE people SET is_active = 0, inactive_at_utc = 900 WHERE person_key = 'PERSON-B'`)
	db.Close()
	if err != nil {
		t.Fatalf("mark inactive: %v", err)
	}

	cfg := badgeAdminTestConfig(path)
	db, err = openDatabase(path, true)
	if err != nil {
		t.Fatalf("open plan: %v", err)
	}
	_, err = buildPlan(db, cfg)
	db.Close()
	if err == nil {
		t.Fatal("expected inactive person to be blocked")
	}

	cfg.ReactivatePerson = true
	result, err := applyReassignment(cfg)
	if err != nil {
		t.Fatalf("reactivate apply: %v", err)
	}
	if !result.PersonReactivated {
		t.Fatalf("expected reactivation: %+v", result)
	}
}
