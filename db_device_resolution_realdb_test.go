package main

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openResolverValidationCopy(t *testing.T) *sql.DB {
	t.Helper()

	dbPath := filepath.Join("runtime", "validation", "resolver-db-copy", "attendance.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open validation copy: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})

	return db
}

func mustParseRFC3339ForTest(t *testing.T, value string) time.Time {
	t.Helper()

	ts, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse timestamp %q: %v", value, err)
	}
	return ts
}

func TestResolverValidationCopyPreservesLegacyTransfers(t *testing.T) {
	db := openResolverValidationCopy(t)

	var transferCount int
	err := db.QueryRow(`
		SELECT COUNT(*)
		FROM records
		WHERE action IN ('U_trasf', 'R_trasf')
	`).Scan(&transferCount)
	if err != nil {
		t.Fatalf("count legacy transfer records: %v", err)
	}

	if transferCount <= 0 {
		t.Fatalf("expected legacy transfer history in validation copy, got %d", transferCount)
	}

	t.Logf("legacy transfer records present in validation copy: %d", transferCount)
}

func TestResolverValidationCopyInOutClosesOpenSession(t *testing.T) {
	db := openResolverValidationCopy(t)

	var employeeID int
	var timestampStr string
	err := db.QueryRow(`
		SELECT r.employee_id, r.timestamp
		FROM records r
		WHERE r.action = 'In'
		  AND NOT EXISTS (
			SELECT 1
			FROM records prev
			WHERE prev.employee_id = r.employee_id
			  AND date(prev.timestamp) = date(r.timestamp)
			  AND prev.timestamp < r.timestamp
		  )
		ORDER BY r.timestamp DESC
		LIMIT 1
	`).Scan(&employeeID, &timestampStr)
	if err != nil {
		t.Fatalf("select first daily In candidate: %v", err)
	}

	timestamp := mustParseRFC3339ForTest(t, timestampStr)
	resolution, err := resolveDeviceActionUsing(db, employeeID, timestamp.Add(time.Second), "in_out", -1)
	if err != nil {
		t.Fatalf("resolve grouped in_out on validation copy: %v", err)
	}

	if resolution.FinalAction != "Out" || resolution.FinalStatusCode != 1 {
		t.Fatalf("expected in_out after open session to resolve Out/1, got %s/%d", resolution.FinalAction, resolution.FinalStatusCode)
	}

	t.Logf("in_out validation candidate employee=%d ts=%s resolved=%s/%d", employeeID, timestamp.Format(time.RFC3339), resolution.FinalAction, resolution.FinalStatusCode)
}

func TestResolverValidationCopyPausaOpenAndClose(t *testing.T) {
	db := openResolverValidationCopy(t)

	var openEmployeeID int
	var openTimestampStr string
	err := db.QueryRow(`
		SELECT r.employee_id, r.timestamp
		FROM records r
		WHERE r.action = 'In'
		  AND NOT EXISTS (
			SELECT 1
			FROM records prev
			WHERE prev.employee_id = r.employee_id
			  AND date(prev.timestamp) = date(r.timestamp)
			  AND prev.timestamp < r.timestamp
		  )
		ORDER BY r.timestamp DESC
		LIMIT 1
	`).Scan(&openEmployeeID, &openTimestampStr)
	if err != nil {
		t.Fatalf("select pause-open candidate: %v", err)
	}

	openTimestamp := mustParseRFC3339ForTest(t, openTimestampStr)
	openResolution, err := resolveDeviceActionUsing(db, openEmployeeID, openTimestamp.Add(time.Second), "pausa", -1)
	if err != nil {
		t.Fatalf("resolve grouped pausa open on validation copy: %v", err)
	}

	if openResolution.FinalAction != "I_pausa" || openResolution.FinalStatusCode != 2 {
		t.Fatalf("expected pausa after In to resolve I_pausa/2, got %s/%d", openResolution.FinalAction, openResolution.FinalStatusCode)
	}

	var closeEmployeeID int
	var closeTimestampStr string
	err = db.QueryRow(`
		SELECT r.employee_id, r.timestamp
		FROM records r
		WHERE r.action = 'I_pausa'
		  AND EXISTS (
			SELECT 1
			FROM records prev
			WHERE prev.employee_id = r.employee_id
			  AND date(prev.timestamp) = date(r.timestamp)
			  AND prev.timestamp < r.timestamp
			  AND prev.action = 'In'
		  )
		ORDER BY r.timestamp DESC
		LIMIT 1
	`).Scan(&closeEmployeeID, &closeTimestampStr)
	if err != nil {
		t.Fatalf("select pause-close candidate: %v", err)
	}

	closeTimestamp := mustParseRFC3339ForTest(t, closeTimestampStr)
	closeResolution, err := resolveDeviceActionUsing(db, closeEmployeeID, closeTimestamp.Add(time.Second), "pausa", -1)
	if err != nil {
		t.Fatalf("resolve grouped pausa close on validation copy: %v", err)
	}

	if closeResolution.FinalAction != "F_pausa" || closeResolution.FinalStatusCode != 3 {
		t.Fatalf("expected pausa after open pause to resolve F_pausa/3, got %s/%d", closeResolution.FinalAction, closeResolution.FinalStatusCode)
	}

	t.Logf("pausa validation open employee=%d ts=%s -> %s/%d", openEmployeeID, openTimestamp.Format(time.RFC3339), openResolution.FinalAction, openResolution.FinalStatusCode)
	t.Logf("pausa validation close employee=%d ts=%s -> %s/%d", closeEmployeeID, closeTimestamp.Format(time.RFC3339), closeResolution.FinalAction, closeResolution.FinalStatusCode)
}
