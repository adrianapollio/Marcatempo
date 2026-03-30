package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type recordRow struct {
	ID                 int64
	DeviceID           int64
	EmployeeID         int
	EmployeeName       string
	Timestamp          time.Time
	RawDeviceTimestamp sql.NullInt64
	StatusCode         int
}

type rawRow struct {
	ID                 int64
	DeviceID           int64
	EmployeeID         int
	EmployeeName       string
	ParsedTimestamp    time.Time
	RawDeviceTimestamp int64
	StatusCode         int
}

func main() {
	var (
		dbPath     string
		dateValue  string
		shiftHours int
		deviceIDs  string
		dryRun     bool
	)

	flag.StringVar(&dbPath, "db", "", "Percorso database SQLite target")
	flag.StringVar(&dateValue, "date", "", "Data da correggere (YYYY-MM-DD)")
	flag.IntVar(&shiftHours, "hours", 0, "Ore da aggiungere")
	flag.StringVar(&deviceIDs, "device-ids", "1,2", "Lista device_id separati da virgola")
	flag.BoolVar(&dryRun, "dry-run", false, "Mostra quante righe verrebbero corrette senza modificare il DB")
	flag.Parse()

	if strings.TrimSpace(dateValue) == "" || shiftHours == 0 {
		fmt.Println(`Uso: go run shift_device_records.go -date 2026-03-30 -hours 1 -device-ids 1,2`)
		os.Exit(2)
	}

	location := europeRome()
	targetDate, err := time.ParseInLocation("2006-01-02", dateValue, location)
	if err != nil {
		log.Fatalf("Data non valida: %v", err)
	}

	ids, err := parseDeviceIDs(deviceIDs)
	if err != nil {
		log.Fatalf("device-ids non validi: %v", err)
	}

	resolvedDB := resolveDBPath(dbPath)
	db, err := sql.Open("sqlite", resolvedDB+"?_pragma=busy_timeout(5000)")
	if err != nil {
		log.Fatalf("Impossibile aprire DB %s: %v", resolvedDB, err)
	}
	defer db.Close()

	recordRows, err := loadRecordRows(db, targetDate, ids)
	if err != nil {
		log.Fatalf("Errore lettura records: %v", err)
	}
	rawRows, err := loadRawRows(db, targetDate, ids)
	if err != nil {
		log.Fatalf("Errore lettura device_raw_records: %v", err)
	}

	log.Printf("Shift device records: db=%s date=%s hours=%d devices=%v dry_run=%v records=%d raw_records=%d", resolvedDB, dateValue, shiftHours, ids, dryRun, len(recordRows), len(rawRows))

	if dryRun {
		for i, row := range recordRows {
			if i >= 5 {
				break
			}
			newTS := row.Timestamp.Add(time.Duration(shiftHours) * time.Hour)
			newRaw := row.RawDeviceTimestamp.Int64
			if row.RawDeviceTimestamp.Valid {
				newRaw += int64(shiftHours) * 3600
			}
			log.Printf("DRY RUN records id=%d device=%d employee=%d ts=%s -> %s raw=%d -> %d", row.ID, row.DeviceID, row.EmployeeID, row.Timestamp.Format(time.RFC3339), newTS.Format(time.RFC3339), row.RawDeviceTimestamp.Int64, newRaw)
		}
		for i, row := range rawRows {
			if i >= 5 {
				break
			}
			newTS := row.ParsedTimestamp.Add(time.Duration(shiftHours) * time.Hour)
			newRaw := row.RawDeviceTimestamp + int64(shiftHours)*3600
			log.Printf("DRY RUN raw id=%d device=%d employee=%d ts=%s -> %s raw=%d -> %d", row.ID, row.DeviceID, row.EmployeeID, row.ParsedTimestamp.Format(time.RFC3339), newTS.Format(time.RFC3339), row.RawDeviceTimestamp, newRaw)
		}
		return
	}

	if err := applyShift(db, recordRows, rawRows, shiftHours); err != nil {
		log.Fatalf("Applicazione shift fallita: %v", err)
	}

	log.Printf("Shift completato con successo: records=%d raw_records=%d", len(recordRows), len(rawRows))
}

func resolveDBPath(flagValue string) string {
	if strings.TrimSpace(flagValue) != "" {
		return flagValue
	}
	if envPath := strings.TrimSpace(os.Getenv("DB_PATH")); envPath != "" {
		return envPath
	}
	for _, candidate := range []string{
		filepath.Join("data", "attendance.db"),
		"attendance.db",
		filepath.Join("..", "data", "attendance.db"),
		filepath.Join("..", "attendance.db"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return filepath.Join("data", "attendance.db")
}

func europeRome() *time.Location {
	loc, err := time.LoadLocation("Europe/Rome")
	if err != nil || loc == nil {
		return time.Local
	}
	return loc
}

func parseDeviceIDs(raw string) ([]int64, error) {
	parts := strings.Split(raw, ",")
	ids := make([]int64, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		value, err := strconv.ParseInt(part, 10, 64)
		if err != nil || value <= 0 {
			return nil, fmt.Errorf("device_id non valido: %q", part)
		}
		ids = append(ids, value)
	}
	if len(ids) == 0 {
		return nil, errors.New("nessun device_id fornito")
	}
	return ids, nil
}

func loadRecordRows(db *sql.DB, targetDate time.Time, deviceIDs []int64) ([]recordRow, error) {
	query := fmt.Sprintf(`
		SELECT id, device_id, employee_id, employee_name, timestamp, raw_device_timestamp, status_code
		FROM records
		WHERE source = 'device'
		  AND timestamp LIKE ?
		  AND device_id IN (%s)
		ORDER BY id
	`, placeholders(len(deviceIDs)))

	args := make([]interface{}, 0, len(deviceIDs)+1)
	args = append(args, targetDate.Format("2006-01-02")+"%")
	for _, id := range deviceIDs {
		args = append(args, id)
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []recordRow
	for rows.Next() {
		var row recordRow
		var timestampStr string
		if err := rows.Scan(&row.ID, &row.DeviceID, &row.EmployeeID, &row.EmployeeName, &timestampStr, &row.RawDeviceTimestamp, &row.StatusCode); err != nil {
			return nil, err
		}
		row.Timestamp, err = time.Parse(time.RFC3339, timestampStr)
		if err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func loadRawRows(db *sql.DB, targetDate time.Time, deviceIDs []int64) ([]rawRow, error) {
	query := fmt.Sprintf(`
		SELECT id, device_id, employee_id, employee_name, parsed_timestamp, raw_device_timestamp, status_code
		FROM device_raw_records
		WHERE parsed_timestamp LIKE ?
		  AND device_id IN (%s)
		ORDER BY id
	`, placeholders(len(deviceIDs)))

	args := make([]interface{}, 0, len(deviceIDs)+1)
	args = append(args, targetDate.Format("2006-01-02")+"%")
	for _, id := range deviceIDs {
		args = append(args, id)
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []rawRow
	for rows.Next() {
		var row rawRow
		var parsedTS string
		if err := rows.Scan(&row.ID, &row.DeviceID, &row.EmployeeID, &row.EmployeeName, &parsedTS, &row.RawDeviceTimestamp, &row.StatusCode); err != nil {
			return nil, err
		}
		row.ParsedTimestamp, err = time.Parse(time.RFC3339, parsedTS)
		if err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func placeholders(n int) string {
	values := make([]string, n)
	for i := 0; i < n; i++ {
		values[i] = "?"
	}
	return strings.Join(values, ",")
}

func applyShift(db *sql.DB, recordRows []recordRow, rawRows []rawRow, shiftHours int) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	shiftBy := time.Duration(shiftHours) * time.Hour
	shiftSeconds := int64(shiftHours) * 3600

	for _, row := range rawRows {
		newTS := row.ParsedTimestamp.Add(shiftBy)
		newRaw := row.RawDeviceTimestamp + shiftSeconds
		if _, err := tx.Exec(
			`UPDATE device_raw_records SET parsed_timestamp = ?, raw_device_timestamp = ? WHERE id = ?`,
			newTS.Format(time.RFC3339),
			newRaw,
			row.ID,
		); err != nil {
			return fmt.Errorf("update device_raw_records id=%d: %w", row.ID, err)
		}
	}

	for _, row := range recordRows {
		newTS := row.Timestamp.Add(shiftBy)
		var newRaw interface{}
		if row.RawDeviceTimestamp.Valid {
			newRaw = row.RawDeviceTimestamp.Int64 + shiftSeconds
		}
		if _, err := tx.Exec(
			`UPDATE records SET timestamp = ?, raw_device_timestamp = ? WHERE id = ?`,
			newTS.Format(time.RFC3339),
			newRaw,
			row.ID,
		); err != nil {
			return fmt.Errorf("update records id=%d: %w", row.ID, err)
		}
	}

	return tx.Commit()
}
