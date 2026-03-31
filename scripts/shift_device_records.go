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
	Action             string
	StatusCode         int
}

type rawRow struct {
	ID                 int64
	DeviceID           int64
	EmployeeID         int
	EmployeeName       string
	ParsedTimestamp    time.Time
	RawDeviceTimestamp int64
	Action             string
	StatusCode         int
}

type shiftFilter struct {
	StartTS        time.Time
	HasStart       bool
	EndExclusiveTS time.Time
	HasEnd         bool
	IncludeActions map[string]struct{}
	ExcludeActions map[string]struct{}
}

func main() {
	var (
		dbPath         string
		dateValue      string
		fromDateValue  string
		toDateValue    string
		fromTSValue    string
		toTSValue      string
		shiftHours     int
		deviceIDs      string
		includeActions string
		excludeActions string
		dryRun         bool
	)

	flag.StringVar(&dbPath, "db", "", "Percorso database SQLite target")
	flag.StringVar(&dateValue, "date", "", "Data da correggere (YYYY-MM-DD)")
	flag.StringVar(&fromDateValue, "from-date", "", "Corregge da questa data inclusa (YYYY-MM-DD)")
	flag.StringVar(&toDateValue, "to-date", "", "Corregge fino a questa data inclusa (YYYY-MM-DD)")
	flag.StringVar(&fromTSValue, "from-ts", "", "Corregge da questo timestamp incluso (RFC3339, es. 2026-03-30T10:30:00+02:00)")
	flag.StringVar(&toTSValue, "to-ts", "", "Corregge fino a questo timestamp escluso (RFC3339)")
	flag.IntVar(&shiftHours, "hours", 0, "Ore da aggiungere")
	flag.StringVar(&deviceIDs, "device-ids", "1,2", "Lista device_id separati da virgola")
	flag.StringVar(&includeActions, "include-actions", "", "Azioni da includere separate da virgola, es. In,U_trasf")
	flag.StringVar(&excludeActions, "exclude-actions", "", "Azioni da escludere separate da virgola, es. In")
	flag.BoolVar(&dryRun, "dry-run", false, "Mostra quante righe verrebbero corrette senza modificare il DB")
	flag.Parse()

	if shiftHours == 0 || (strings.TrimSpace(dateValue) == "" && strings.TrimSpace(fromDateValue) == "" && strings.TrimSpace(fromTSValue) == "") {
		fmt.Println(`Uso: go run shift_device_records.go -date 2026-03-30 -hours 1 -device-ids 1,2`)
		fmt.Println(`Oppure: go run shift_device_records.go -from-ts 2026-03-30T10:30:00+02:00 -hours -1 -device-ids 1,2 -include-actions In,U_trasf`)
		os.Exit(2)
	}

	location := europeRome()
	filter, err := buildShiftFilter(location, dateValue, fromDateValue, toDateValue, fromTSValue, toTSValue, includeActions, excludeActions)
	if err != nil {
		log.Fatalf("Filtro non valido: %v", err)
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

	recordRows, err := loadRecordRows(db, ids, filter)
	if err != nil {
		log.Fatalf("Errore lettura records: %v", err)
	}
	rawRows, err := loadRawRows(db, ids, filter)
	if err != nil {
		log.Fatalf("Errore lettura device_raw_records: %v", err)
	}

	log.Printf("Shift device records: db=%s date=%s from_date=%s to_date=%s from_ts=%s to_ts=%s hours=%d devices=%v include_actions=%v exclude_actions=%v dry_run=%v records=%d raw_records=%d",
		resolvedDB, dateValue, fromDateValue, toDateValue, fromTSValue, toTSValue, shiftHours, ids, keysOf(filter.IncludeActions), keysOf(filter.ExcludeActions), dryRun, len(recordRows), len(rawRows))

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

func buildShiftFilter(loc *time.Location, dateValue string, fromDateValue string, toDateValue string, fromTSValue string, toTSValue string, includeActions string, excludeActions string) (shiftFilter, error) {
	filter := shiftFilter{
		IncludeActions: parseActionSet(includeActions),
		ExcludeActions: parseActionSet(excludeActions),
	}

	if dateValue != "" {
		targetDate, err := time.ParseInLocation("2006-01-02", dateValue, loc)
		if err != nil {
			return shiftFilter{}, fmt.Errorf("date non valida: %w", err)
		}
		filter.StartTS = targetDate
		filter.EndExclusiveTS = targetDate.Add(24 * time.Hour)
		filter.HasStart = true
		filter.HasEnd = true
		return filter, nil
	}

	if fromTSValue != "" {
		startTS, err := time.Parse(time.RFC3339, fromTSValue)
		if err != nil {
			return shiftFilter{}, fmt.Errorf("from-ts non valido: %w", err)
		}
		filter.StartTS = startTS
		filter.HasStart = true
	} else if fromDateValue != "" {
		startTS, err := time.ParseInLocation("2006-01-02", fromDateValue, loc)
		if err != nil {
			return shiftFilter{}, fmt.Errorf("from-date non valida: %w", err)
		}
		filter.StartTS = startTS
		filter.HasStart = true
	}

	if toTSValue != "" {
		endTS, err := time.Parse(time.RFC3339, toTSValue)
		if err != nil {
			return shiftFilter{}, fmt.Errorf("to-ts non valido: %w", err)
		}
		filter.EndExclusiveTS = endTS
		filter.HasEnd = true
	} else if toDateValue != "" {
		endTS, err := time.ParseInLocation("2006-01-02", toDateValue, loc)
		if err != nil {
			return shiftFilter{}, fmt.Errorf("to-date non valida: %w", err)
		}
		filter.EndExclusiveTS = endTS.Add(24 * time.Hour)
		filter.HasEnd = true
	}

	if filter.HasStart && filter.HasEnd && !filter.EndExclusiveTS.After(filter.StartTS) {
		return shiftFilter{}, errors.New("to-date deve essere maggiore o uguale a from-date")
	}

	return filter, nil
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

func parseActionSet(raw string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, part := range strings.Split(raw, ",") {
		action := normalizeAction(part)
		if action == "" {
			continue
		}
		set[action] = struct{}{}
	}
	return set
}

func normalizeAction(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func keysOf(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	return keys
}

func shouldSkipAction(action string, included map[string]struct{}, excluded map[string]struct{}) bool {
	normalized := normalizeAction(action)
	if len(included) > 0 {
		if _, ok := included[normalized]; !ok {
			return true
		}
	}
	if len(excluded) == 0 {
		return false
	}
	_, ok := excluded[normalized]
	return ok
}

func buildTimeWhereClause(column string, filter shiftFilter) (string, []interface{}) {
	clauses := []string{}
	args := []interface{}{}
	if filter.HasStart {
		clauses = append(clauses, fmt.Sprintf("%s >= ?", column))
		args = append(args, filter.StartTS.Format(time.RFC3339))
	}
	if filter.HasEnd {
		clauses = append(clauses, fmt.Sprintf("%s < ?", column))
		args = append(args, filter.EndExclusiveTS.Format(time.RFC3339))
	}
	return strings.Join(clauses, " AND "), args
}

func loadRecordRows(db *sql.DB, deviceIDs []int64, filter shiftFilter) ([]recordRow, error) {
	timeClause, timeArgs := buildTimeWhereClause("timestamp", filter)
	query := fmt.Sprintf(`
		SELECT id, device_id, employee_id, employee_name, timestamp, raw_device_timestamp, action, status_code
		FROM records
		WHERE source = 'device'
		  %s
		  AND device_id IN (%s)
		ORDER BY id
	`, prefixWithAnd(timeClause), placeholders(len(deviceIDs)))

	args := make([]interface{}, 0, len(timeArgs)+len(deviceIDs))
	args = append(args, timeArgs...)
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
		if err := rows.Scan(&row.ID, &row.DeviceID, &row.EmployeeID, &row.EmployeeName, &timestampStr, &row.RawDeviceTimestamp, &row.Action, &row.StatusCode); err != nil {
			return nil, err
		}
		row.Timestamp, err = time.Parse(time.RFC3339, timestampStr)
		if err != nil {
			return nil, err
		}
		if shouldSkipAction(row.Action, filter.IncludeActions, filter.ExcludeActions) {
			continue
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func loadRawRows(db *sql.DB, deviceIDs []int64, filter shiftFilter) ([]rawRow, error) {
	timeClause, timeArgs := buildTimeWhereClause("parsed_timestamp", filter)
	query := fmt.Sprintf(`
		SELECT id, device_id, employee_id, employee_name, parsed_timestamp, raw_device_timestamp, action, status_code
		FROM device_raw_records
		WHERE 1=1
		  %s
		  AND device_id IN (%s)
		ORDER BY id
	`, prefixWithAnd(timeClause), placeholders(len(deviceIDs)))

	args := make([]interface{}, 0, len(timeArgs)+len(deviceIDs))
	args = append(args, timeArgs...)
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
		if err := rows.Scan(&row.ID, &row.DeviceID, &row.EmployeeID, &row.EmployeeName, &parsedTS, &row.RawDeviceTimestamp, &row.Action, &row.StatusCode); err != nil {
			return nil, err
		}
		row.ParsedTimestamp, err = time.Parse(time.RFC3339, parsedTS)
		if err != nil {
			return nil, err
		}
		if shouldSkipAction(row.Action, filter.IncludeActions, filter.ExcludeActions) {
			continue
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func prefixWithAnd(clause string) string {
	if strings.TrimSpace(clause) == "" {
		return ""
	}
	return "AND " + clause
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
