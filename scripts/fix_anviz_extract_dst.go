package main

import (
	"archive/zip"
	"database/sql"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type fixSstXML struct {
	XMLName xml.Name `xml:"sst"`
	Si      []struct {
		T string `xml:"t"`
		R []struct {
			T string `xml:"t"`
		} `xml:"r"`
	} `xml:"si"`
}

type fixWorksheetXML struct {
	XMLName   xml.Name `xml:"worksheet"`
	SheetData struct {
		Row []struct {
			C []struct {
				R  string `xml:"r,attr"`
				T  string `xml:"t,attr"`
				V  string `xml:"v"`
				Is struct {
					T string `xml:"t"`
					R []struct {
						T string `xml:"t"`
					} `xml:"r"`
				} `xml:"is"`
			} `xml:"c"`
		} `xml:"row"`
	} `xml:"sheetData"`
}

type fixRow struct {
	EmployeeID        int
	EmployeeName      string
	Action            string
	StatusCode        int
	CorrectTimestamp  time.Time
	BuggyTimestamp    time.Time
	CorrectRawTS      uint32
	BuggyRawTS        uint32
}

type fixSummary struct {
	Candidates          int
	UpdatedRecords      int
	UpdatedRawRecords   int
	DeletedRecords      int
	DeletedRawRecords   int
	AlreadyCorrect      int
	MissingRecords      int
	MissingRawRecords   int
	SkippedByFilter     int
}

type fixColumnMapping struct {
	idIndex       int
	nameIndex     int
	dateIndex     int
	timeIndex     int
	dateTimeIndex int
	actionIndex   int
}

func main() {
	var (
		filePath       string
		dbPath         string
		deviceID       int
		dryRun         bool
		minTimeValue   string
		includeActions string
		excludeActions string
	)

	flag.StringVar(&filePath, "file", "", "Percorso file Anviz (.xlsx)")
	flag.StringVar(&dbPath, "db", "", "Percorso database SQLite target")
	flag.IntVar(&deviceID, "device-id", 0, "ID device interno (245 -> 1, 246 -> 2)")
	flag.BoolVar(&dryRun, "dry-run", false, "Mostra le righe che verrebbero corrette senza modificare il DB")
	flag.StringVar(&minTimeValue, "min-time", "", "Corregge solo le righe con orario locale >= HH:MM")
	flag.StringVar(&includeActions, "include-actions", "", "Azioni da includere separate da virgola")
	flag.StringVar(&excludeActions, "exclude-actions", "", "Azioni da escludere separate da virgola")
	flag.Parse()

	if strings.TrimSpace(filePath) == "" || deviceID <= 0 {
		fmt.Println(`Uso: go run fix_anviz_extract_dst.go -file "data\excels\estratto245.xlsx" -device-id 1 -min-time 10:30`)
		os.Exit(2)
	}

	loc := europeRomeFix()
	rows, err := readFixRows(filePath, loc)
	if err != nil {
		log.Fatalf("Lettura file fallita: %v", err)
	}

	minutesFromMidnight, hasMinTime, err := parseClockThreshold(minTimeValue)
	if err != nil {
		log.Fatalf("min-time non valido: %v", err)
	}

	includeSet := parseActionSetFix(includeActions)
	excludeSet := parseActionSetFix(excludeActions)

	filtered := make([]fixRow, 0, len(rows))
	for _, row := range rows {
		if shouldSkipFixRow(row, hasMinTime, minutesFromMidnight, includeSet, excludeSet) {
			continue
		}
		filtered = append(filtered, row)
	}

	log.Printf("Fix Anviz DST: file=%s device_id=%d db=%s dry_run=%v min_time=%s candidates=%d filtered_out=%d",
		filePath, deviceID, resolveDBPathFix(dbPath), dryRun, minTimeValue, len(filtered), len(rows)-len(filtered))

	if dryRun {
		for i, row := range filtered {
			if i >= 10 {
				break
			}
			log.Printf(
				"DRY RUN employee=%d name=%q action=%s status=%d ts=%s -> %s raw=%d -> %d",
				row.EmployeeID,
				row.EmployeeName,
				row.Action,
				row.StatusCode,
				row.BuggyTimestamp.Format(time.RFC3339),
				row.CorrectTimestamp.Format(time.RFC3339),
				row.BuggyRawTS,
				row.CorrectRawTS,
			)
		}
		log.Printf("Dry run completato: righe correggibili=%d", len(filtered))
		return
	}

	db, err := sql.Open("sqlite", resolveDBPathFix(dbPath)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		log.Fatalf("Impossibile aprire DB: %v", err)
	}
	defer db.Close()

	summary, err := applyFixRows(db, uint32(deviceID), filtered)
	if err != nil {
		log.Fatalf("Fix fallito: %v", err)
	}
	summary.SkippedByFilter = len(rows) - len(filtered)

	log.Printf(
		"Fix completato: candidates=%d updated_records=%d updated_raw_records=%d deleted_records=%d deleted_raw_records=%d already_correct=%d missing_records=%d missing_raw_records=%d filtered_out=%d",
		summary.Candidates,
		summary.UpdatedRecords,
		summary.UpdatedRawRecords,
		summary.DeletedRecords,
		summary.DeletedRawRecords,
		summary.AlreadyCorrect,
		summary.MissingRecords,
		summary.MissingRawRecords,
		summary.SkippedByFilter,
	)
}

func readFixRows(path string, loc *time.Location) ([]fixRow, error) {
	if strings.ToLower(filepath.Ext(path)) != ".xlsx" {
		return nil, fmt.Errorf("formato non supportato per il fix: %s", filepath.Ext(path))
	}

	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	var sharedStrings fixSstXML
	var worksheet fixWorksheetXML

	for _, file := range r.File {
		name := strings.ToLower(file.Name)
		switch name {
		case "xl/sharedstrings.xml":
			rc, err := file.Open()
			if err != nil {
				return nil, err
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, err
			}
			if err := xml.Unmarshal(data, &sharedStrings); err != nil {
				return nil, err
			}
		case "xl/worksheets/sheet1.xml":
			rc, err := file.Open()
			if err != nil {
				return nil, err
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, err
			}
			if err := xml.Unmarshal(data, &worksheet); err != nil {
				return nil, err
			}
		}
	}

	shared := make([]string, 0, len(sharedStrings.Si))
	for _, item := range sharedStrings.Si {
		if item.T != "" {
			shared = append(shared, item.T)
			continue
		}
		var builder strings.Builder
		for _, run := range item.R {
			builder.WriteString(run.T)
		}
		shared = append(shared, builder.String())
	}

	matrix := make([][]string, 0, len(worksheet.SheetData.Row))
	for _, row := range worksheet.SheetData.Row {
		valuesByCol := map[int]string{}
		maxCol := 0
		for _, cell := range row.C {
			colIndex := excelColumnIndexFix(cell.R)
			if colIndex > maxCol {
				maxCol = colIndex
			}
			valuesByCol[colIndex] = resolveFixCellValue(cell, shared)
		}
		values := make([]string, maxCol+1)
		for idx, value := range valuesByCol {
			values[idx] = value
		}
		matrix = append(matrix, values)
	}

	if len(matrix) == 0 {
		return nil, errors.New("nessuna riga trovata")
	}

	header := matrix[0]
	columnMap := detectFixColumns(header)
	rows := make([]fixRow, 0, len(matrix)-1)
	for rowIndex, row := range matrix[1:] {
		parsed, ok, err := parseFixRow(row, columnMap, loc)
		if err != nil {
			log.Printf("Riga %d saltata: %v", rowIndex+2, err)
			continue
		}
		if ok {
			rows = append(rows, parsed)
		}
	}

	if len(rows) == 0 {
		return nil, errors.New("nessuna riga valida trovata")
	}

	return rows, nil
}

func resolveFixCellValue(cell struct {
	R  string `xml:"r,attr"`
	T  string `xml:"t,attr"`
	V  string `xml:"v"`
	Is struct {
		T string `xml:"t"`
		R []struct {
			T string `xml:"t"`
		} `xml:"r"`
	} `xml:"is"`
}, shared []string) string {
	switch cell.T {
	case "s":
		idx, err := strconv.Atoi(strings.TrimSpace(cell.V))
		if err != nil || idx < 0 || idx >= len(shared) {
			return strings.TrimSpace(cell.V)
		}
		return strings.TrimSpace(shared[idx])
	case "inlineStr":
		if cell.Is.T != "" {
			return strings.TrimSpace(cell.Is.T)
		}
		var builder strings.Builder
		for _, run := range cell.Is.R {
			builder.WriteString(run.T)
		}
		return strings.TrimSpace(builder.String())
	default:
		return strings.TrimSpace(cell.V)
	}
}

func excelColumnIndexFix(ref string) int {
	ref = strings.TrimSpace(ref)
	col := 0
	for _, ch := range ref {
		if ch < 'A' || ch > 'Z' {
			break
		}
		col = col*26 + int(ch-'A'+1)
	}
	if col == 0 {
		return 0
	}
	return col - 1
}

func detectFixColumns(header []string) fixColumnMapping {
	mapping := fixColumnMapping{
		idIndex:       -1,
		nameIndex:     -1,
		dateIndex:     -1,
		timeIndex:     -1,
		dateTimeIndex: -1,
		actionIndex:   -1,
	}

	for idx, col := range header {
		norm := normalizeFixHeader(col)
		switch {
		case mapping.idIndex == -1 && (strings.Contains(norm, "userid") || strings.Contains(norm, "employeeid") || strings.Contains(norm, "dipendenteid") || norm == "id" || strings.Contains(norm, "matricola") || strings.Contains(norm, "badge")):
			mapping.idIndex = idx
		case mapping.nameIndex == -1 && (strings.Contains(norm, "name") || strings.Contains(norm, "nome") || strings.Contains(norm, "employee")):
			mapping.nameIndex = idx
		case mapping.dateTimeIndex == -1 && (strings.Contains(norm, "timestamp") || strings.Contains(norm, "datetime") || strings.Contains(norm, "dataora")):
			mapping.dateTimeIndex = idx
		case mapping.dateIndex == -1 && (norm == "data" || norm == "date" || strings.Contains(norm, "giorno")):
			mapping.dateIndex = idx
		case mapping.timeIndex == -1 && (norm == "ora" || norm == "time" || strings.Contains(norm, "orario")):
			mapping.timeIndex = idx
		case mapping.actionIndex == -1 && (strings.Contains(norm, "azione") || strings.Contains(norm, "stato") || strings.Contains(norm, "status") || strings.Contains(norm, "type") || strings.Contains(norm, "tipo")):
			mapping.actionIndex = idx
		}
	}

	if mapping.idIndex == -1 && len(header) > 0 {
		mapping.idIndex = 0
	}
	if mapping.nameIndex == -1 && len(header) > 1 {
		mapping.nameIndex = 1
	}
	if mapping.dateTimeIndex == -1 && mapping.dateIndex == -1 && len(header) > 2 {
		mapping.dateTimeIndex = 2
	}
	if mapping.actionIndex == -1 && len(header) > 3 {
		mapping.actionIndex = 3
	}

	return mapping
}

func normalizeFixHeader(input string) string {
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
		"Ã ", "a",
		"Ã¡", "a",
		"Ã¨", "e",
		"Ã©", "e",
		"Ã¬", "i",
		"Ã­", "i",
		"Ã²", "o",
		"Ã³", "o",
		"Ã¹", "u",
		"Ãº", "u",
	)
	return replacer.Replace(strings.TrimSpace(strings.ToLower(input)))
}

func parseFixRow(row []string, mapping fixColumnMapping, loc *time.Location) (fixRow, bool, error) {
	var result fixRow

	idRaw := fixCellValue(row, mapping.idIndex)
	if idRaw == "" {
		return result, false, nil
	}

	employeeID, err := strconv.Atoi(strings.TrimSpace(idRaw))
	if err != nil || employeeID <= 0 {
		return result, false, fmt.Errorf("employee id non valido: %q", idRaw)
	}
	result.EmployeeID = employeeID
	result.EmployeeName = fixCellValue(row, mapping.nameIndex)

	action, statusCode, err := mapFixAction(fixCellValue(row, mapping.actionIndex))
	if err != nil {
		return result, false, err
	}
	result.Action = action
	result.StatusCode = statusCode

	var dateValue string
	var timeValue string
	switch {
	case mapping.dateTimeIndex >= 0:
		dateValue = fixCellValue(row, mapping.dateTimeIndex)
	case mapping.dateIndex >= 0 && mapping.timeIndex >= 0:
		dateValue = fixCellValue(row, mapping.dateIndex)
		timeValue = fixCellValue(row, mapping.timeIndex)
	case mapping.dateIndex >= 0:
		dateValue = fixCellValue(row, mapping.dateIndex)
	default:
		return result, false, errors.New("colonna data/ora non trovata")
	}

	correctTS, err := parseFixTimestamp(dateValue, timeValue, loc, false)
	if err != nil {
		return result, false, err
	}
	buggyTS, err := parseFixTimestamp(dateValue, timeValue, loc, true)
	if err != nil {
		return result, false, err
	}

	correctRaw, err := rawDeviceTimestampFromTimeFix(correctTS)
	if err != nil {
		return result, false, err
	}
	buggyRaw, err := rawDeviceTimestampFromTimeFix(buggyTS)
	if err != nil {
		return result, false, err
	}

	result.CorrectTimestamp = correctTS
	result.BuggyTimestamp = buggyTS
	result.CorrectRawTS = correctRaw
	result.BuggyRawTS = buggyRaw

	return result, true, nil
}

func fixCellValue(row []string, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[idx])
}

func parseFixTimestamp(datePart string, timePart string, loc *time.Location, legacyExcel bool) (time.Time, error) {
	datePart = strings.TrimSpace(datePart)
	timePart = strings.TrimSpace(timePart)

	if datePart == "" && timePart == "" {
		return time.Time{}, errors.New("timestamp vuoto")
	}
	if timePart != "" {
		return parseFixTimestamp(strings.TrimSpace(datePart+" "+timePart), "", loc, legacyExcel)
	}

	if excelFloat, err := strconv.ParseFloat(datePart, 64); err == nil && excelFloat > 30000 {
		if legacyExcel {
			return legacyExcelDateToTimeFix(excelFloat, loc), nil
		}
		return excelDateToTimeFix(excelFloat, loc), nil
	}

	layouts := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006/01/02 15:04:05",
		"2006/01/02 15:04",
		"02/01/2006 15:04:05",
		"02/01/2006 15:04",
		"2/1/2006 15:04:05",
		"2/1/2006 15:04",
		"02-01-2006 15:04:05",
		"02-01-2006 15:04",
		"2-1-2006 15:04:05",
		"2-1-2006 15:04",
		"02/01/2006",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if ts, err := time.ParseInLocation(layout, datePart, loc); err == nil {
			return ts, nil
		}
	}
	return time.Time{}, fmt.Errorf("timestamp non riconosciuto: %q", datePart)
}

func excelDateToTimeFix(value float64, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.Local
	}

	wholeDays, fractionalDay := math.Modf(value)
	baseDateUTC := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(wholeDays))

	totalNanos := int64(math.Round(fractionalDay * float64(24*time.Hour)))
	dayNanos := int64(24 * time.Hour)
	if totalNanos >= dayNanos {
		baseDateUTC = baseDateUTC.AddDate(0, 0, 1)
		totalNanos -= dayNanos
	}

	hour := int(totalNanos / int64(time.Hour))
	totalNanos %= int64(time.Hour)
	minute := int(totalNanos / int64(time.Minute))
	totalNanos %= int64(time.Minute)
	second := int(totalNanos / int64(time.Second))
	nanosecond := int(totalNanos % int64(time.Second))

	return time.Date(baseDateUTC.Year(), baseDateUTC.Month(), baseDateUTC.Day(), hour, minute, second, nanosecond, loc)
}

func legacyExcelDateToTimeFix(value float64, loc *time.Location) time.Time {
	return time.Date(1899, 12, 30, 0, 0, 0, 0, loc).Add(time.Duration(value * float64(24*time.Hour)))
}

func mapFixAction(raw string) (string, int, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", 0, errors.New("azione vuota")
	}

	if numeric, err := strconv.Atoi(value); err == nil {
		switch numeric {
		case 0:
			return "In", 0, nil
		case 1:
			return "Out", 1, nil
		case 2:
			return "I_pausa", 2, nil
		case 3:
			return "F_pausa", 3, nil
		case 4:
			return "U_trasf", 4, nil
		case 5:
			return "R_trasf", 5, nil
		case 6:
			return "I_break", 6, nil
		case 7:
			return "F_break", 7, nil
		}
	}

	upper := normalizeFixHeader(strings.ToUpper(value))
	switch {
	case upper == "in" || upper == "entrata" || upper == "checkin":
		return "In", 0, nil
	case upper == "out" || upper == "uscita" || upper == "checkout":
		return "Out", 1, nil
	case strings.HasPrefix(upper, "ipausa") || strings.HasPrefix(upper, "iniziopausa") || upper == "ibreak":
		return "I_pausa", 2, nil
	case strings.HasPrefix(upper, "fpausa") || strings.HasPrefix(upper, "finepausa") || upper == "fbreak":
		return "F_pausa", 3, nil
	case strings.HasPrefix(upper, "utrasf") || strings.HasPrefix(upper, "utrasfer") || strings.HasPrefix(upper, "uscitatrasf") || strings.HasPrefix(upper, "iniziotrasf"):
		return "U_trasf", 4, nil
	case strings.HasPrefix(upper, "rtrasf") || strings.HasPrefix(upper, "rtrasfer") || strings.HasPrefix(upper, "ritornotrasf") || strings.HasPrefix(upper, "rientrotrasf"):
		return "R_trasf", 5, nil
	case upper == "ibreak":
		return "I_break", 6, nil
	case upper == "fbreak":
		return "F_break", 7, nil
	default:
		return "", 0, fmt.Errorf("azione non riconosciuta: %q", raw)
	}
}

func parseClockThreshold(value string) (int, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false, nil
	}
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, false, fmt.Errorf("usa HH:MM")
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 23 {
		return 0, false, fmt.Errorf("ora non valida: %q", parts[0])
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil || minute < 0 || minute > 59 {
		return 0, false, fmt.Errorf("minuti non validi: %q", parts[1])
	}
	return hour*60 + minute, true, nil
}

func parseActionSetFix(raw string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, part := range strings.Split(raw, ",") {
		action := strings.ToLower(strings.TrimSpace(part))
		if action == "" {
			continue
		}
		set[action] = struct{}{}
	}
	return set
}

func shouldSkipFixRow(row fixRow, hasMinTime bool, minMinutes int, includeSet map[string]struct{}, excludeSet map[string]struct{}) bool {
	actionKey := strings.ToLower(strings.TrimSpace(row.Action))
	if len(includeSet) > 0 {
		if _, ok := includeSet[actionKey]; !ok {
			return true
		}
	}
	if _, ok := excludeSet[actionKey]; ok {
		return true
	}
	if !hasMinTime {
		return false
	}
	localTS := row.CorrectTimestamp.In(europeRomeFix())
	return localTS.Hour()*60+localTS.Minute() < minMinutes
}

func resolveDBPathFix(flagValue string) string {
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

func europeRomeFix() *time.Location {
	loc, err := time.LoadLocation("Europe/Rome")
	if err != nil || loc == nil {
		return time.Local
	}
	return loc
}

func rawDeviceTimestampFromTimeFix(timestamp time.Time) (uint32, error) {
	localTS := timestamp.In(europeRomeFix())
	baseUTC := time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC)
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

func applyFixRows(db *sql.DB, deviceID uint32, rows []fixRow) (fixSummary, error) {
	tx, err := db.Begin()
	if err != nil {
		return fixSummary{}, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	summary := fixSummary{Candidates: len(rows)}
	for _, row := range rows {
		rawResult, rawErr := reconcileRawRecord(tx, deviceID, row)
		if rawErr != nil {
			return summary, rawErr
		}
		recordResult, recordErr := reconcileRecord(tx, deviceID, row)
		if recordErr != nil {
			return summary, recordErr
		}

		switch rawResult {
		case "updated":
			summary.UpdatedRawRecords++
		case "deleted":
			summary.DeletedRawRecords++
		case "missing":
			summary.MissingRawRecords++
		}

		switch recordResult {
		case "updated":
			summary.UpdatedRecords++
		case "deleted":
			summary.DeletedRecords++
		case "missing":
			summary.MissingRecords++
		}

		if (rawResult == "already_correct" || rawResult == "deleted") && (recordResult == "already_correct" || recordResult == "deleted") {
			summary.AlreadyCorrect++
		}
	}

	if err := tx.Commit(); err != nil {
		return summary, err
	}
	return summary, nil
}

func reconcileRawRecord(tx *sql.Tx, deviceID uint32, row fixRow) (string, error) {
	res, err := tx.Exec(
		`UPDATE device_raw_records
		 SET parsed_timestamp = ?, raw_device_timestamp = ?
		 WHERE device_id = ?
		   AND employee_id = ?
		   AND status_code = ?
		   AND raw_device_timestamp = ?
		   AND parsed_timestamp = ?`,
		row.CorrectTimestamp.Format(time.RFC3339),
		int64(row.CorrectRawTS),
		int64(deviceID),
		row.EmployeeID,
		row.StatusCode,
		int64(row.BuggyRawTS),
		row.BuggyTimestamp.Format(time.RFC3339),
	)
	if err != nil {
		if isUniqueConstraintFix(err) {
			deleted, deleteErr := deleteBuggyRawRecord(tx, deviceID, row)
			if deleteErr != nil {
				return "", deleteErr
			}
			if deleted {
				return "deleted", nil
			}
			if rawAlreadyCorrect(tx, deviceID, row) {
				return "already_correct", nil
			}
		}
		return "", fmt.Errorf("update device_raw_records employee=%d action=%s: %w", row.EmployeeID, row.Action, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return "", err
	}
	if affected > 0 {
		return "updated", nil
	}
	if rawAlreadyCorrect(tx, deviceID, row) {
		return "already_correct", nil
	}
	return "missing", nil
}

func reconcileRecord(tx *sql.Tx, deviceID uint32, row fixRow) (string, error) {
	res, err := tx.Exec(
		`UPDATE records
		 SET timestamp = ?, raw_device_timestamp = ?
		 WHERE source = 'device'
		   AND device_id = ?
		   AND employee_id = ?
		   AND status_code = ?
		   AND raw_device_timestamp = ?
		   AND timestamp = ?`,
		row.CorrectTimestamp.Format(time.RFC3339),
		int64(row.CorrectRawTS),
		int64(deviceID),
		row.EmployeeID,
		row.StatusCode,
		int64(row.BuggyRawTS),
		row.BuggyTimestamp.Format(time.RFC3339),
	)
	if err != nil {
		if isUniqueConstraintFix(err) {
			deleted, deleteErr := deleteBuggyRecord(tx, deviceID, row)
			if deleteErr != nil {
				return "", deleteErr
			}
			if deleted {
				return "deleted", nil
			}
			if recordAlreadyCorrect(tx, deviceID, row) {
				return "already_correct", nil
			}
		}
		return "", fmt.Errorf("update records employee=%d action=%s: %w", row.EmployeeID, row.Action, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return "", err
	}
	if affected > 0 {
		return "updated", nil
	}
	if recordAlreadyCorrect(tx, deviceID, row) {
		return "already_correct", nil
	}
	return "missing", nil
}

func rawAlreadyCorrect(tx *sql.Tx, deviceID uint32, row fixRow) bool {
	var count int
	err := tx.QueryRow(
		`SELECT COUNT(*)
		 FROM device_raw_records
		 WHERE device_id = ?
		   AND employee_id = ?
		   AND status_code = ?
		   AND raw_device_timestamp = ?
		   AND parsed_timestamp = ?`,
		int64(deviceID),
		row.EmployeeID,
		row.StatusCode,
		int64(row.CorrectRawTS),
		row.CorrectTimestamp.Format(time.RFC3339),
	).Scan(&count)
	return err == nil && count > 0
}

func recordAlreadyCorrect(tx *sql.Tx, deviceID uint32, row fixRow) bool {
	var count int
	err := tx.QueryRow(
		`SELECT COUNT(*)
		 FROM records
		 WHERE source = 'device'
		   AND device_id = ?
		   AND employee_id = ?
		   AND status_code = ?
		   AND raw_device_timestamp = ?
		   AND timestamp = ?`,
		int64(deviceID),
		row.EmployeeID,
		row.StatusCode,
		int64(row.CorrectRawTS),
		row.CorrectTimestamp.Format(time.RFC3339),
	).Scan(&count)
	return err == nil && count > 0
}

func deleteBuggyRawRecord(tx *sql.Tx, deviceID uint32, row fixRow) (bool, error) {
	res, err := tx.Exec(
		`DELETE FROM device_raw_records
		 WHERE device_id = ?
		   AND employee_id = ?
		   AND status_code = ?
		   AND raw_device_timestamp = ?
		   AND parsed_timestamp = ?`,
		int64(deviceID),
		row.EmployeeID,
		row.StatusCode,
		int64(row.BuggyRawTS),
		row.BuggyTimestamp.Format(time.RFC3339),
	)
	if err != nil {
		return false, fmt.Errorf("delete device_raw_records employee=%d action=%s: %w", row.EmployeeID, row.Action, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func deleteBuggyRecord(tx *sql.Tx, deviceID uint32, row fixRow) (bool, error) {
	res, err := tx.Exec(
		`DELETE FROM records
		 WHERE source = 'device'
		   AND device_id = ?
		   AND employee_id = ?
		   AND status_code = ?
		   AND raw_device_timestamp = ?
		   AND timestamp = ?`,
		int64(deviceID),
		row.EmployeeID,
		row.StatusCode,
		int64(row.BuggyRawTS),
		row.BuggyTimestamp.Format(time.RFC3339),
	)
	if err != nil {
		return false, fmt.Errorf("delete records employee=%d action=%s: %w", row.EmployeeID, row.Action, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func isUniqueConstraintFix(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
