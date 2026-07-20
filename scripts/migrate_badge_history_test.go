package main

import (
	"database/sql"
	"testing"
)

func int64Pointer(value int64) *int64 {
	return &value
}

func TestIntervalsOverlapUsesHalfOpenBoundaries(t *testing.T) {
	if intervalsOverlap(0, int64Pointer(100), 100, int64Pointer(200)) {
		t.Fatal("intervalli adiacenti [0,100) e [100,200) non devono sovrapporsi")
	}
	if !intervalsOverlap(0, int64Pointer(101), 100, int64Pointer(200)) {
		t.Fatal("intervalli [0,101) e [100,200) devono sovrapporsi")
	}
	if !intervalsOverlap(0, nil, 100, nil) {
		t.Fatal("due intervalli aperti devono sovrapporsi")
	}
}

func TestSchemaRejectsEmployeeAndCardOverlaps(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE records (employee_id INTEGER, timestamp TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(badgeSchemaSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO people (
			person_key, display_name, is_active, inactive_at_utc,
			created_at_utc, updated_at_utc
		) VALUES
			('P-1', 'Persona 1', 1, NULL, 1, 1),
			('P-2', 'Persona 2', 1, NULL, 1, 1)
	`); err != nil {
		t.Fatal(err)
	}
	insert := func(personID, employeeID int, cardSerial int64, from int64, to interface{}) error {
		_, err := db.Exec(`
			INSERT INTO person_badge_history (
				person_id, anviz_employee_id, card_serial,
				valid_from_utc, valid_to_utc, boundary_quality, reason,
				created_by, created_at_utc
			) VALUES (?, ?, ?, ?, ?, 'exact', 'initial', 'test', 1)
		`, personID, employeeID, cardSerial, from, to)
		return err
	}

	if err := insert(1, 10, 1000, 0, 100); err != nil {
		t.Fatal(err)
	}
	if err := insert(2, 10, 2000, 50, 150); err == nil {
		t.Fatal("attesa sovrapposizione bloccata per employee_id")
	}
	if err := insert(2, 20, 1000, 50, 150); err == nil {
		t.Fatal("attesa sovrapposizione bloccata per card_serial")
	}
	if err := insert(2, 10, 1000, 100, nil); err != nil {
		t.Fatalf("il riutilizzo al confine esatto deve essere consentito: %v", err)
	}
}
