package main

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openBadgeHistoryShadowTestDB(t *testing.T) *sql.DB {
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
			person_key TEXT NOT NULL,
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
		CREATE TABLE records (
			id INTEGER PRIMARY KEY,
			employee_id INTEGER NOT NULL,
			timestamp TEXT NOT NULL
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
			(1, 1, 33, 0, 100, NULL),
			(2, 1, 56, 100, 200, NULL),
			(3, 1, 124, 200, NULL, NULL),
			(4, 2, 33, 400, NULL, NULL);
	`)
	if err != nil {
		t.Fatalf("seed history: %v", err)
	}

	return db
}

func TestBadgeHistoryResolverUsesHalfOpenIntervalsAndCurrentPresentation(t *testing.T) {
	db := openBadgeHistoryShadowTestDB(t)
	resolver, err := loadBadgeHistoryResolverUsing(db)
	if err != nil {
		t.Fatalf("load resolver: %v", err)
	}

	assignment, presentationID, status := resolver.resolveAt(33, time.Unix(50, 0).UTC())
	if status != "resolved" || assignment.PersonID != 1 || presentationID != 124 {
		t.Fatalf("old badge: status=%s person=%d presentation=%d", status, assignment.PersonID, presentationID)
	}

	_, _, status = resolver.resolveAt(33, time.Unix(100, 0).UTC())
	if status != "interval_missing" {
		t.Fatalf("valid_to boundary must be excluded, got %s", status)
	}

	assignment, presentationID, status = resolver.resolveAt(56, time.Unix(100, 0).UTC())
	if status != "resolved" || assignment.PersonID != 1 || presentationID != 124 {
		t.Fatalf("valid_from boundary: status=%s person=%d presentation=%d", status, assignment.PersonID, presentationID)
	}

	assignment, presentationID, status = resolver.resolveAt(33, time.Unix(450, 0).UTC())
	if status != "resolved" || assignment.PersonID != 2 || presentationID != 33 {
		t.Fatalf("reused badge: status=%s person=%d presentation=%d", status, assignment.PersonID, presentationID)
	}
}

func TestBadgeHistoryShadowAuditDetectsFutureReuseWithoutChangingLegacyResult(t *testing.T) {
	db := openBadgeHistoryShadowTestDB(t)
	resolver, err := loadBadgeHistoryResolverUsing(db)
	if err != nil {
		t.Fatalf("load resolver: %v", err)
	}

	format := func(value int64) string {
		return time.Unix(value, 0).UTC().Format(time.RFC3339)
	}
	_, err = db.Exec(`
		INSERT INTO records (id, employee_id, timestamp)
		VALUES (1, 33, ?), (2, 56, ?), (3, 33, ?), (4, 999, ?)
	`, format(50), format(150), format(450), format(50))
	if err != nil {
		t.Fatalf("seed records: %v", err)
	}

	report, err := auditBadgeHistoryRowsUsing(
		db,
		`SELECT id, employee_id, timestamp FROM records ORDER BY id`,
		map[int]int{33: 56, 56: 124},
		resolver,
	)
	if err != nil {
		t.Fatalf("shadow audit: %v", err)
	}

	if report.Total != 4 || report.Matched != 2 || report.Mismatched != 1 || report.Unmanaged != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if len(report.MismatchSamples) != 1 {
		t.Fatalf("expected one mismatch sample, got %d", len(report.MismatchSamples))
	}
}
