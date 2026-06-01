package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
	_ "modernc.org/sqlite"
)

type extractRow struct {
	EmployeeID   int
	EmployeeName string
	Timestamp    time.Time
	RawAction    string
	RawStatus    int
	LegacyAction string
}

type summary struct {
	Count        int
	Employees    int
	FirstTS      time.Time
	LastTS       time.Time
	ActionCounts map[string]int
}

type dbSummary struct {
	RecordsCount  int
	RawCount      int
	RangeRecords  int
	RangeRaw      int
	FirstRecordTS *time.Time
	LastRecordTS  *time.Time
	FirstRawTS    *time.Time
	LastRawTS     *time.Time
	RecordActions map[string]int
	RawActions    map[string]int
}

type timelineRecord struct {
	Action string
}

type resolvedAction struct {
	FinalAction string
	FinalStatus int
}

type replaceResult struct {
	DeletedRecords  int64
	DeletedRaw      int64
	InsertedRecords int
	InsertedRaw     int
}

func main() {
	var (
		filePath string
		dbPath   string
		deviceID int
		apply    bool
	)

	flag.StringVar(&filePath, "file", "", "Percorso file Excel esportato da Anviz")
	flag.StringVar(&dbPath, "db", filepath.Join("..", "data", "attendance.db"), "Percorso database SQLite target")
	flag.IntVar(&deviceID, "device-id", 0, "device_id interno da riallineare")
	flag.BoolVar(&apply, "apply", false, "Applica il riallineamento del device nel range coperto dall'estratto")
	flag.Parse()

	if strings.TrimSpace(filePath) == "" || deviceID <= 0 {
		log.Fatal(`Uso: go run .\replace_device_from_extract.go -file "..\data\20260601_R.xlsx" -device-id 3 -db "PATH_DEPLOY\attendance.db" [-apply]`)
	}

	loc, err := time.LoadLocation("Europe/Rome")
	if err != nil || loc == nil {
		loc = time.Local
	}

	rows, err := readExtractRows(filePath, loc)
	if err != nil {
		log.Fatalf("lettura extract fallita: %v", err)
	}
	sortExtractRows(rows)
	extractSummary := summarizeExtract(rows)

	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		log.Fatalf("apertura db fallita: %v", err)
	}
	defer db.Close()

	currentSummary, err := loadDBSummary(db, deviceID, extractSummary.FirstTS, extractSummary.LastTS)
	if err != nil {
		log.Fatalf("lettura riepilogo db fallita: %v", err)
	}

	printExtractSummary(filePath, deviceID, extractSummary)
	printDBSummary(deviceID, currentSummary)

	if !apply {
		log.Printf("Dry run: nessuna modifica applicata. Usa -apply per riallineare il device %d nel range %s -> %s.", deviceID, extractSummary.FirstTS.Format(time.RFC3339), extractSummary.LastTS.Format(time.RFC3339))
		return
	}

	replaced, err := replaceDeviceRange(db, deviceID, rows, extractSummary)
	if err != nil {
		log.Fatalf("riallineamento fallito: %v", err)
	}

	log.Printf("Riallineamento completato: cancellati records=%d raw=%d, inseriti records=%d raw=%d",
		replaced.DeletedRecords,
		replaced.DeletedRaw,
		replaced.InsertedRecords,
		replaced.InsertedRaw,
	)

	finalSummary, err := loadDBSummary(db, deviceID, extractSummary.FirstTS, extractSummary.LastTS)
	if err != nil {
		log.Fatalf("lettura riepilogo finale db fallita: %v", err)
	}
	printDBSummary(deviceID, finalSummary)
}

func readExtractRows(path string, loc *time.Location) ([]extractRow, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, fmt.Errorf("nessun foglio trovato")
	}

	allRows, err := f.GetRows(sheets[0])
	if err != nil {
		return nil, err
	}
	if len(allRows) < 2 {
		return nil, fmt.Errorf("nessuna riga dati trovata")
	}

	mapping := detectColumns(allRows[0])
	rows := make([]extractRow, 0, len(allRows)-1)

	for rowIndex, row := range allRows[1:] {
		parsed, ok, err := parseExtractRow(row, mapping, loc)
		if err != nil {
			return nil, fmt.Errorf("riga %d: %w", rowIndex+2, err)
		}
		if ok {
			rows = append(rows, parsed)
		}
	}

	if len(rows) == 0 {
		return nil, fmt.Errorf("nessuna riga valida nell'estratto")
	}

	return rows, nil
}

type columnMapping struct {
	idIndex       int
	nameIndex     int
	dateTimeIndex int
	actionIndex   int
}

func detectColumns(header []string) columnMapping {
	mapping := columnMapping{
		idIndex:       -1,
		nameIndex:     -1,
		dateTimeIndex: -1,
		actionIndex:   -1,
	}

	for idx, value := range header {
		norm := normalizeHeader(value)
		switch {
		case mapping.idIndex == -1 && (norm == "id" || strings.Contains(norm, "userid") || strings.Contains(norm, "employeeid") || strings.Contains(norm, "matricola")):
			mapping.idIndex = idx
		case mapping.nameIndex == -1 && (strings.Contains(norm, "nome") || strings.Contains(norm, "name")):
			mapping.nameIndex = idx
		case mapping.dateTimeIndex == -1 && (strings.Contains(norm, "data") || strings.Contains(norm, "date") || strings.Contains(norm, "timestamp") || strings.Contains(norm, "ora")):
			mapping.dateTimeIndex = idx
		case mapping.actionIndex == -1 && (strings.Contains(norm, "stato") || strings.Contains(norm, "azione") || strings.Contains(norm, "status") || strings.Contains(norm, "tipo")):
			mapping.actionIndex = idx
		}
	}

	if mapping.idIndex == -1 {
		mapping.idIndex = 0
	}
	if mapping.nameIndex == -1 {
		mapping.nameIndex = 1
	}
	if mapping.dateTimeIndex == -1 {
		mapping.dateTimeIndex = 2
	}
	if mapping.actionIndex == -1 {
		mapping.actionIndex = 3
	}

	return mapping
}

func normalizeHeader(value string) string {
	replacer := strings.NewReplacer(
		" ", "",
		"_", "",
		"-", "",
		"/", "",
		"\\", "",
		":", "",
		".", "",
		"(", "",
		")", "",
		"'", "",
		"`", "",
		"\"", "",
	)
	return replacer.Replace(strings.ToLower(strings.TrimSpace(value)))
}

func parseExtractRow(row []string, mapping columnMapping, loc *time.Location) (extractRow, bool, error) {
	var result extractRow

	idRaw := cellValue(row, mapping.idIndex)
	if idRaw == "" {
		return result, false, nil
	}

	employeeID, err := strconv.Atoi(idRaw)
	if err != nil || employeeID <= 0 {
		return result, false, fmt.Errorf("id dipendente non valido: %q", idRaw)
	}
	result.EmployeeID = employeeID
	result.EmployeeName = cellValue(row, mapping.nameIndex)

	result.Timestamp, err = time.ParseInLocation("2006-01-02 15:04:05", cellValue(row, mapping.dateTimeIndex), loc)
	if err != nil {
		return result, false, fmt.Errorf("timestamp non valido: %w", err)
	}

	result.RawAction, result.RawStatus, result.LegacyAction, err = mapLegacyActionToSimplifiedRaw(cellValue(row, mapping.actionIndex))
	if err != nil {
		return result, false, err
	}

	return result, true, nil
}

func cellValue(row []string, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[idx])
}

func mapLegacyActionToSimplifiedRaw(raw string) (string, int, string, error) {
	upper := strings.ToUpper(strings.TrimSpace(raw))
	norm := strings.ReplaceAll(upper, " ", "")
	norm = strings.ReplaceAll(norm, "_", "")

	switch {
	case upper == "ENTRATA" || upper == "IN":
		return "in_out", 0, "In", nil
	case upper == "USCITA" || upper == "OUT":
		return "in_out", 1, "Out", nil
	case upper == "I_PAUSA" || upper == "INIZIO PAUSA" || upper == "I BREAK" || norm == "IPAUSA" || norm == "INIZIOPAUSA" || norm == "IBREAK":
		return "pausa", 2, "I_pausa", nil
	case upper == "F_PAUSA" || upper == "FINE PAUSA" || upper == "F BREAK" || norm == "FPAUSA" || norm == "FINEPAUSA" || norm == "FBREAK":
		return "pausa", 3, "F_pausa", nil
	case upper == "U_TRASF" || upper == "USCITA TRASFERTA" || norm == "UTRASF" || norm == "UTRASFER" || norm == "USCITATRASFERTA" || norm == "INIZIOTRASFERTA":
		return "in_out", 4, "U_trasf", nil
	case upper == "R_TRASF" || upper == "RIENTRO TRASFERTA" || norm == "RTRASF" || norm == "RTRASFER" || norm == "RIENTROTRASFERTA" || norm == "RITORNOTRASFERTA":
		return "in_out", 5, "R_trasf", nil
	default:
		return "", 0, "", fmt.Errorf("azione non supportata nell'estratto: %q", raw)
	}
}

func sortExtractRows(rows []extractRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Timestamp.Equal(rows[j].Timestamp) {
			return rows[i].EmployeeID < rows[j].EmployeeID
		}
		return rows[i].Timestamp.Before(rows[j].Timestamp)
	})
}

func summarizeExtract(rows []extractRow) summary {
	out := summary{
		Count:        len(rows),
		ActionCounts: map[string]int{},
	}
	employees := map[int]struct{}{}

	for idx, row := range rows {
		employees[row.EmployeeID] = struct{}{}
		key := fmt.Sprintf("%s/raw_status_%d", row.RawAction, row.RawStatus)
		out.ActionCounts[key]++
		if idx == 0 || row.Timestamp.Before(out.FirstTS) {
			out.FirstTS = row.Timestamp
		}
		if idx == 0 || row.Timestamp.After(out.LastTS) {
			out.LastTS = row.Timestamp
		}
	}

	out.Employees = len(employees)
	return out
}

func loadDBSummary(db *sql.DB, deviceID int, firstTS time.Time, lastTS time.Time) (dbSummary, error) {
	out := dbSummary{
		RecordActions: map[string]int{},
		RawActions:    map[string]int{},
	}

	if err := db.QueryRow(`
		SELECT COUNT(*), MIN(timestamp), MAX(timestamp)
		FROM records
		WHERE source = 'device' AND device_id = ?
	`, deviceID).Scan(&out.RecordsCount, timeStringPtr(&out.FirstRecordTS), timeStringPtr(&out.LastRecordTS)); err != nil {
		return out, err
	}

	if err := db.QueryRow(`
		SELECT COUNT(*), MIN(parsed_timestamp), MAX(parsed_timestamp)
		FROM device_raw_records
		WHERE device_id = ?
	`, deviceID).Scan(&out.RawCount, timeStringPtr(&out.FirstRawTS), timeStringPtr(&out.LastRawTS)); err != nil {
		return out, err
	}

	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM records
		WHERE source = 'device'
		  AND device_id = ?
		  AND timestamp >= ?
		  AND timestamp <= ?
	`, deviceID, firstTS.Format(time.RFC3339), lastTS.Format(time.RFC3339)).Scan(&out.RangeRecords); err != nil {
		return out, err
	}

	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM device_raw_records
		WHERE device_id = ?
		  AND parsed_timestamp >= ?
		  AND parsed_timestamp <= ?
	`, deviceID, firstTS.Format(time.RFC3339), lastTS.Format(time.RFC3339)).Scan(&out.RangeRaw); err != nil {
		return out, err
	}

	if err := loadActionCounts(db, &out, deviceID); err != nil {
		return out, err
	}

	return out, nil
}

func loadActionCounts(db *sql.DB, out *dbSummary, deviceID int) error {
	recordRows, err := db.Query(`
		SELECT action, status_code, COUNT(*)
		FROM records
		WHERE source = 'device' AND device_id = ?
		GROUP BY action, status_code
		ORDER BY COUNT(*) DESC, action ASC
	`, deviceID)
	if err != nil {
		return err
	}
	defer recordRows.Close()

	for recordRows.Next() {
		var action string
		var statusCode int
		var count int
		if err := recordRows.Scan(&action, &statusCode, &count); err != nil {
			return err
		}
		out.RecordActions[fmt.Sprintf("%s/%d", action, statusCode)] = count
	}
	if err := recordRows.Err(); err != nil {
		return err
	}

	rawRows, err := db.Query(`
		SELECT action, status_code, COUNT(*)
		FROM device_raw_records
		WHERE device_id = ?
		GROUP BY action, status_code
		ORDER BY COUNT(*) DESC, action ASC
	`, deviceID)
	if err != nil {
		return err
	}
	defer rawRows.Close()

	for rawRows.Next() {
		var action string
		var statusCode int
		var count int
		if err := rawRows.Scan(&action, &statusCode, &count); err != nil {
			return err
		}
		out.RawActions[fmt.Sprintf("%s/%d", action, statusCode)] = count
	}

	return rawRows.Err()
}

func timeStringPtr(target **time.Time) interface{} {
	return &nullableTime{target: target}
}

type nullableTime struct {
	target **time.Time
}

func (n *nullableTime) Scan(src interface{}) error {
	if src == nil {
		*n.target = nil
		return nil
	}

	switch value := src.(type) {
	case time.Time:
		parsed := value
		*n.target = &parsed
		return nil
	case string:
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return err
		}
		*n.target = &parsed
		return nil
	case []byte:
		parsed, err := time.Parse(time.RFC3339, string(value))
		if err != nil {
			return err
		}
		*n.target = &parsed
		return nil
	default:
		return fmt.Errorf("tipo timestamp inatteso %T", src)
	}
}

func printExtractSummary(filePath string, deviceID int, s summary) {
	log.Printf("Extract: file=%s device=%d righe=%d dipendenti=%d range=%s -> %s",
		filePath,
		deviceID,
		s.Count,
		s.Employees,
		s.FirstTS.Format(time.RFC3339),
		s.LastTS.Format(time.RFC3339),
	)
	printCountMap("Extract raw semplificati", s.ActionCounts)
}

func printDBSummary(deviceID int, s dbSummary) {
	log.Printf("DB device=%d records=%d raw=%d records_range=%s -> %s raw_range=%s -> %s",
		deviceID,
		s.RecordsCount,
		s.RawCount,
		formatTimePtr(s.FirstRecordTS),
		formatTimePtr(s.LastRecordTS),
		formatTimePtr(s.FirstRawTS),
		formatTimePtr(s.LastRawTS),
	)
	log.Printf("DB nel range extract: records=%d raw=%d", s.RangeRecords, s.RangeRaw)
	printCountMap("DB record actions", s.RecordActions)
	printCountMap("DB raw actions", s.RawActions)
}

func printCountMap(label string, counts map[string]int) {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	log.Printf("%s:", label)
	for _, key := range keys {
		log.Printf("  %s = %d", key, counts[key])
	}
}

func formatTimePtr(value *time.Time) string {
	if value == nil {
		return "-"
	}
	return value.Format(time.RFC3339)
}

func replaceDeviceRange(db *sql.DB, deviceID int, rows []extractRow, s summary) (replaceResult, error) {
	tx, err := db.Begin()
	if err != nil {
		return replaceResult{}, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	rawDeleteResult, err := tx.Exec(`
		DELETE FROM device_raw_records
		WHERE device_id = ?
		  AND parsed_timestamp >= ?
		  AND parsed_timestamp <= ?
	`, deviceID, s.FirstTS.Format(time.RFC3339), s.LastTS.Format(time.RFC3339))
	if err != nil {
		return replaceResult{}, err
	}
	deletedRaw, err := rawDeleteResult.RowsAffected()
	if err != nil {
		return replaceResult{}, err
	}

	recordDeleteResult, err := tx.Exec(`
		DELETE FROM records
		WHERE source = 'device'
		  AND device_id = ?
		  AND timestamp >= ?
		  AND timestamp <= ?
	`, deviceID, s.FirstTS.Format(time.RFC3339), s.LastTS.Format(time.RFC3339))
	if err != nil {
		return replaceResult{}, err
	}
	deletedRecords, err := recordDeleteResult.RowsAffected()
	if err != nil {
		return replaceResult{}, err
	}

	insertedRaw := 0
	insertedRecords := 0
	importedAt := time.Now().Format(time.RFC3339)

	for _, row := range rows {
		rawTS, err := rawDeviceTimestampFromTime(row.Timestamp)
		if err != nil {
			return replaceResult{}, fmt.Errorf("employee=%d timestamp=%s: %w", row.EmployeeID, row.Timestamp.Format(time.RFC3339), err)
		}

		if _, err := tx.Exec(`
			INSERT INTO device_raw_records (
				device_id,
				employee_id,
				employee_name,
				raw_device_timestamp,
				parsed_timestamp,
				action,
				status_code,
				imported_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`,
			deviceID,
			row.EmployeeID,
			row.EmployeeName,
			int64(rawTS),
			row.Timestamp.Format(time.RFC3339),
			row.RawAction,
			row.RawStatus,
			importedAt,
		); err != nil {
			return replaceResult{}, err
		}
		insertedRaw++

		resolution, err := resolveSimplifiedAction(tx, row.EmployeeID, row.Timestamp, row.RawAction)
		if err != nil {
			return replaceResult{}, err
		}

		if _, err := tx.Exec(`
			INSERT INTO records (
				employee_id,
				employee_name,
				timestamp,
				action,
				status_code,
				source,
				device_id,
				raw_device_timestamp,
				latitude,
				longitude
			) VALUES (?, ?, ?, ?, ?, 'device', ?, ?, NULL, NULL)
		`,
			row.EmployeeID,
			row.EmployeeName,
			row.Timestamp.Format(time.RFC3339),
			resolution.FinalAction,
			resolution.FinalStatus,
			deviceID,
			int64(rawTS),
		); err != nil {
			return replaceResult{}, err
		}
		insertedRecords++
	}

	if err := tx.Commit(); err != nil {
		return replaceResult{}, err
	}

	return replaceResult{
		DeletedRecords:  deletedRecords,
		DeletedRaw:      deletedRaw,
		InsertedRecords: insertedRecords,
		InsertedRaw:     insertedRaw,
	}, nil
}

func resolveSimplifiedAction(tx *sql.Tx, employeeID int, timestamp time.Time, rawAction string) (resolvedAction, error) {
	timeline, err := loadTimelineBefore(tx, employeeID, timestamp)
	if err != nil {
		return resolvedAction{}, err
	}

	workOpen := false
	pauseOpen := false
	sawPauseMarker := false

	for _, item := range timeline {
		switch item.Action {
		case "In":
			workOpen = true
		case "Out":
			workOpen = false
			pauseOpen = false
		case "I_pausa":
			sawPauseMarker = true
			if workOpen {
				pauseOpen = true
			}
		case "F_pausa":
			sawPauseMarker = true
			if workOpen {
				pauseOpen = false
			}
		}
	}

	switch rawAction {
	case "in_out":
		if pauseOpen {
			return resolvedAction{FinalAction: "Out", FinalStatus: 1}, nil
		}
		if workOpen {
			return resolvedAction{FinalAction: "Out", FinalStatus: 1}, nil
		}
		if sawPauseMarker {
			return resolvedAction{FinalAction: "Out", FinalStatus: 1}, nil
		}
		return resolvedAction{FinalAction: "In", FinalStatus: 0}, nil
	case "pausa":
		if pauseOpen {
			return resolvedAction{FinalAction: "F_pausa", FinalStatus: 3}, nil
		}
		if workOpen {
			return resolvedAction{FinalAction: "I_pausa", FinalStatus: 2}, nil
		}
		return resolvedAction{FinalAction: "I_pausa", FinalStatus: 2}, nil
	default:
		return resolvedAction{}, fmt.Errorf("raw action semplificata non supportata: %s", rawAction)
	}
}

func loadTimelineBefore(tx *sql.Tx, employeeID int, timestamp time.Time) ([]timelineRecord, error) {
	loc, err := time.LoadLocation("Europe/Rome")
	if err != nil || loc == nil {
		loc = time.Local
	}

	localTS := timestamp.In(loc)
	dayStart := time.Date(localTS.Year(), localTS.Month(), localTS.Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.Add(24 * time.Hour)

	rows, err := tx.Query(`
		SELECT action
		FROM records
		WHERE employee_id = ?
		  AND timestamp >= ?
		  AND timestamp < ?
		  AND timestamp < ?
		ORDER BY timestamp ASC, id ASC
	`, employeeID, dayStart.Format(time.RFC3339), dayEnd.Format(time.RFC3339), timestamp.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var timeline []timelineRecord
	for rows.Next() {
		var item timelineRecord
		if err := rows.Scan(&item.Action); err != nil {
			return nil, err
		}
		timeline = append(timeline, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return timeline, nil
}

func rawDeviceTimestampFromTime(timestamp time.Time) (uint32, error) {
	baseUTC := time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC)
	localTS := timestamp.In(time.FixedZone("Europe/Rome", int((1 * time.Hour).Seconds())))
	if loc, err := time.LoadLocation("Europe/Rome"); err == nil && loc != nil {
		localTS = timestamp.In(loc)
	}

	targetUTC := time.Date(localTS.Year(), localTS.Month(), localTS.Day(), 0, 0, 0, 0, time.UTC)
	days := targetUTC.Sub(baseUTC) / (24 * time.Hour)
	if days < 0 {
		return 0, fmt.Errorf("timestamp fuori range protocollo Anviz")
	}

	seconds := days*86400 + time.Duration(localTS.Hour()*3600+localTS.Minute()*60+localTS.Second())
	if seconds < 0 || uint64(seconds) > uint64(^uint32(0)) {
		return 0, fmt.Errorf("timestamp fuori range protocollo Anviz")
	}

	return uint32(seconds), nil
}

func init() {
	log.SetFlags(log.LstdFlags)
	log.SetOutput(os.Stdout)
}
