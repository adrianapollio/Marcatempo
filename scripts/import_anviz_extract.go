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

type sstXML struct {
	XMLName xml.Name `xml:"sst"`
	Si      []struct {
		T string `xml:"t"`
		R []struct {
			T string `xml:"t"`
		} `xml:"r"`
	} `xml:"si"`
}

type worksheetXML struct {
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

type importRow struct {
	EmployeeID   int
	EmployeeName string
	Timestamp    time.Time
	Action       string
	StatusCode   int
}

type importSummary struct {
	Processed  int
	Inserted   int
	Duplicates int
	Skipped    int
}

type shiftRule struct {
	Date      string
	ShiftBy   time.Duration
	Enabled   bool
	Location  *time.Location
}

var errDuplicateRecord = errors.New("record duplicato")

func main() {
	var (
		filePath  string
		dbPath    string
		deviceID  int
		dryRun    bool
		delimiter string
		shiftDate string
		shiftHours int
	)

	flag.StringVar(&filePath, "file", "", "Percorso file estratto (.xlsx, .csv, .txt, .tsv)")
	flag.StringVar(&dbPath, "db", "", "Percorso database SQLite target")
	flag.IntVar(&deviceID, "device-id", 0, "ID device interno (245 -> 1, 246 -> 2)")
	flag.BoolVar(&dryRun, "dry-run", false, "Legge e valida il file senza inserire nulla")
	flag.StringVar(&delimiter, "delimiter", "", "Separatore forzato per file testo: comma, semicolon, tab, pipe")
	flag.StringVar(&shiftDate, "shift-date", "", "Applica uno shift solo alle righe di questa data (YYYY-MM-DD)")
	flag.IntVar(&shiftHours, "shift-hours", 0, "Numero di ore da aggiungere alle righe filtrate da -shift-date")
	flag.Parse()

	if strings.TrimSpace(filePath) == "" || deviceID <= 0 {
		fmt.Println("Uso:")
		fmt.Println(`  go run import_anviz_extract.go -file "data\\excels\\estratto245.xlsx" -device-id 1`)
		fmt.Println(`  go run import_anviz_extract.go -file "data\\excels\\estratto246.csv" -device-id 2 -db "C:\\path\\attendance.db"`)
		os.Exit(2)
	}

	resolvedDBPath := resolveDBPath(dbPath)
	location := europeRome()
	rows, err := readExtractRows(filePath, delimiter, location)
	if err != nil {
		log.Fatalf("Lettura file fallita: %v", err)
	}

	rule, err := buildShiftRule(shiftDate, shiftHours, location)
	if err != nil {
		log.Fatalf("Configurazione shift non valida: %v", err)
	}

	log.Printf("Import Anviz extract: file=%s device_id=%d rows=%d db=%s dry_run=%v shift_date=%s shift_hours=%d", filePath, deviceID, len(rows), resolvedDBPath, dryRun, shiftDate, shiftHours)

	if dryRun {
		for i, row := range rows {
			if i >= 5 {
				break
			}
			row.Timestamp = applyShiftRule(row.Timestamp, rule)
			rawTS, _ := rawDeviceTimestampFromTime(row.Timestamp)
			log.Printf("DRY RUN row=%d employee=%d name=%q ts=%s action=%s status=%d raw_ts=%d", i+1, row.EmployeeID, row.EmployeeName, row.Timestamp.Format(time.RFC3339), row.Action, row.StatusCode, rawTS)
		}
		log.Printf("Dry run completato: righe valide=%d", len(rows))
		return
	}

	db, err := sql.Open("sqlite", resolvedDBPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		log.Fatalf("Impossibile aprire DB %s: %v", resolvedDBPath, err)
	}
	defer db.Close()

	summary, err := importRows(db, uint32(deviceID), rows, rule)
	if err != nil {
		log.Fatalf("Import fallito: %v", err)
	}

	log.Printf("Import completato: processate=%d inserite=%d duplicate=%d saltate=%d", summary.Processed, summary.Inserted, summary.Duplicates, summary.Skipped)
}

func buildShiftRule(shiftDate string, shiftHours int, loc *time.Location) (shiftRule, error) {
	rule := shiftRule{
		Date:     strings.TrimSpace(shiftDate),
		ShiftBy:  time.Duration(shiftHours) * time.Hour,
		Location: loc,
	}
	if rule.Date == "" || shiftHours == 0 {
		return rule, nil
	}
	if _, err := time.ParseInLocation("2006-01-02", rule.Date, loc); err != nil {
		return shiftRule{}, fmt.Errorf("data %q non valida, usa YYYY-MM-DD", shiftDate)
	}
	rule.Enabled = true
	return rule, nil
}

func applyShiftRule(timestamp time.Time, rule shiftRule) time.Time {
	if !rule.Enabled {
		return timestamp
	}
	if timestamp.In(rule.Location).Format("2006-01-02") != rule.Date {
		return timestamp
	}
	return timestamp.Add(rule.ShiftBy)
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

func readExtractRows(path string, delimiter string, loc *time.Location) ([]importRow, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".xlsx":
		return readXLSXRows(path, loc)
	case ".csv", ".txt", ".tsv":
		return readDelimitedRows(path, delimiter, loc)
	default:
		return nil, fmt.Errorf("formato non supportato: %s", ext)
	}
}

func readXLSXRows(path string, loc *time.Location) ([]importRow, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	var sharedStrings sstXML
	var worksheet worksheetXML

	for _, f := range r.File {
		name := strings.ToLower(f.Name)
		switch {
		case name == "xl/sharedstrings.xml":
			if err := decodeXMLFile(f, &sharedStrings); err != nil {
				return nil, err
			}
		case name == "xl/worksheets/sheet1.xml":
			if err := decodeXMLFile(f, &worksheet); err != nil {
				return nil, err
			}
		}
	}

	matrix := make([][]string, 0, len(worksheet.SheetData.Row))
	for _, row := range worksheet.SheetData.Row {
		valuesByCol := map[int]string{}
		maxCol := -1
		for _, cell := range row.C {
			colIndex := excelColumnIndex(cell.R)
			if colIndex > maxCol {
				maxCol = colIndex
			}
			valuesByCol[colIndex] = resolveExcelCellValue(cell, sharedStrings)
		}
		if maxCol < 0 {
			continue
		}
		rowValues := make([]string, maxCol+1)
		for idx, value := range valuesByCol {
			rowValues[idx] = strings.TrimSpace(value)
		}
		matrix = append(matrix, rowValues)
	}

	return parseTabularRows(matrix, loc)
}

func decodeXMLFile(f *zip.File, target interface{}) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return err
	}

	return xml.Unmarshal(data, target)
}

func resolveExcelCellValue(cell struct {
	R  string `xml:"r,attr"`
	T  string `xml:"t,attr"`
	V  string `xml:"v"`
	Is struct {
		T string `xml:"t"`
		R []struct {
			T string `xml:"t"`
		} `xml:"r"`
	} `xml:"is"`
}, sharedStrings sstXML) string {
	switch cell.T {
	case "s":
		idx, err := strconv.Atoi(strings.TrimSpace(cell.V))
		if err != nil || idx < 0 || idx >= len(sharedStrings.Si) {
			return ""
		}
		if sharedStrings.Si[idx].T != "" {
			return sharedStrings.Si[idx].T
		}
		var builder strings.Builder
		for _, part := range sharedStrings.Si[idx].R {
			builder.WriteString(part.T)
		}
		return builder.String()
	case "inlineStr":
		if cell.Is.T != "" {
			return cell.Is.T
		}
		var builder strings.Builder
		for _, part := range cell.Is.R {
			builder.WriteString(part.T)
		}
		return builder.String()
	default:
		return cell.V
	}
}

func excelColumnIndex(ref string) int {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return -1
	}
	col := 0
	for _, r := range ref {
		if r < 'A' || r > 'Z' {
			if r < 'a' || r > 'z' {
				break
			}
			r = r - 'a' + 'A'
		}
		col = col*26 + int(r-'A'+1)
	}
	if col == 0 {
		return -1
	}
	return col - 1
}

func readDelimitedRows(path string, forcedDelimiter string, loc *time.Location) ([]importRow, error) {
	contentBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := strings.ReplaceAll(string(contentBytes), "\r\n", "\n")
	lines := strings.Split(content, "\n")
	matrix := make([][]string, 0, len(lines))
	delimiter := detectDelimiter(lines, forcedDelimiter)

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		parts := splitLine(line, delimiter)
		matrix = append(matrix, parts)
	}

	return parseTabularRows(matrix, loc)
}

func detectDelimiter(lines []string, forced string) rune {
	switch strings.ToLower(strings.TrimSpace(forced)) {
	case "comma", ",":
		return ','
	case "semicolon", ";":
		return ';'
	case "tab", "\\t":
		return '\t'
	case "pipe", "|":
		return '|'
	}

	candidates := []rune{';', '\t', ',', '|'}
	bestDelimiter := ';'
	bestScore := -1
	sampleLimit := 5
	if len(lines) < sampleLimit {
		sampleLimit = len(lines)
	}
	for _, candidate := range candidates {
		score := 0
		for i := 0; i < sampleLimit; i++ {
			score += strings.Count(lines[i], string(candidate))
		}
		if score > bestScore {
			bestScore = score
			bestDelimiter = candidate
		}
	}
	return bestDelimiter
}

func splitLine(line string, delimiter rune) []string {
	parts := strings.Split(line, string(delimiter))
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		result = append(result, strings.Trim(strings.TrimSpace(part), `"`))
	}
	return result
}

func parseTabularRows(matrix [][]string, loc *time.Location) ([]importRow, error) {
	if len(matrix) == 0 {
		return nil, errors.New("nessuna riga trovata")
	}

	header := matrix[0]
	columnMap := detectColumns(header)

	rows := make([]importRow, 0, len(matrix)-1)
	for rowIndex, row := range matrix[1:] {
		parsed, ok, err := parseRow(row, columnMap, loc)
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

type columnMapping struct {
	idIndex       int
	nameIndex     int
	dateIndex     int
	timeIndex     int
	dateTimeIndex int
	actionIndex   int
}

func detectColumns(header []string) columnMapping {
	mapping := columnMapping{
		idIndex:       -1,
		nameIndex:     -1,
		dateIndex:     -1,
		timeIndex:     -1,
		dateTimeIndex: -1,
		actionIndex:   -1,
	}

	for idx, col := range header {
		norm := normalizeHeader(col)
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

func normalizeHeader(input string) string {
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
		"a", "a",
		"A", "a",
		"e", "e",
		"E", "e",
		"i", "i",
		"I", "i",
		"o", "o",
		"O", "o",
		"u", "u",
		"U", "u",
		"à", "a",
		"á", "a",
		"è", "e",
		"é", "e",
		"ì", "i",
		"í", "i",
		"ò", "o",
		"ó", "o",
		"ù", "u",
		"ú", "u",
	)
	return replacer.Replace(strings.TrimSpace(strings.ToLower(input)))
}

func parseRow(row []string, mapping columnMapping, loc *time.Location) (importRow, bool, error) {
	var result importRow

	idRaw := cellValue(row, mapping.idIndex)
	if idRaw == "" {
		return result, false, nil
	}

	employeeID, err := strconv.Atoi(strings.TrimSpace(idRaw))
	if err != nil || employeeID <= 0 {
		return result, false, fmt.Errorf("employee id non valido: %q", idRaw)
	}
	result.EmployeeID = employeeID
	result.EmployeeName = cellValue(row, mapping.nameIndex)

	action, statusCode, err := mapAction(cellValue(row, mapping.actionIndex))
	if err != nil {
		return result, false, err
	}
	result.Action = action
	result.StatusCode = statusCode

	var timestamp time.Time
	switch {
	case mapping.dateTimeIndex >= 0:
		timestamp, err = parseFlexibleTimestamp(cellValue(row, mapping.dateTimeIndex), "", loc)
	case mapping.dateIndex >= 0 && mapping.timeIndex >= 0:
		timestamp, err = parseFlexibleTimestamp(cellValue(row, mapping.dateIndex), cellValue(row, mapping.timeIndex), loc)
	case mapping.dateIndex >= 0:
		timestamp, err = parseFlexibleTimestamp(cellValue(row, mapping.dateIndex), "", loc)
	default:
		return result, false, errors.New("colonna data/ora non trovata")
	}
	if err != nil {
		return result, false, err
	}
	result.Timestamp = timestamp

	return result, true, nil
}

func cellValue(row []string, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[idx])
}

func parseFlexibleTimestamp(datePart string, timePart string, loc *time.Location) (time.Time, error) {
	datePart = strings.TrimSpace(datePart)
	timePart = strings.TrimSpace(timePart)

	if datePart == "" && timePart == "" {
		return time.Time{}, errors.New("timestamp vuoto")
	}

	if timePart != "" {
		combined := strings.TrimSpace(datePart + " " + timePart)
		return parseFlexibleTimestamp(combined, "", loc)
	}

	if excelFloat, err := strconv.ParseFloat(datePart, 64); err == nil && excelFloat > 30000 {
		return excelDateToTime(excelFloat, loc), nil
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

func excelDateToTime(value float64, loc *time.Location) time.Time {
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

func mapAction(raw string) (string, int, error) {
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

	upper := normalizeHeader(strings.ToUpper(value))
	switch {
	case upper == "in" || upper == "entrata" || upper == "checkin":
		return "In", 0, nil
	case upper == "out" || upper == "uscita" || upper == "checkout":
		return "Out", 1, nil
	case strings.HasPrefix(upper, "ipausa") || strings.HasPrefix(upper, "iniziopausa") || upper == "ibreak":
		return "I_pausa", 2, nil
	case strings.HasPrefix(upper, "fpausa") || strings.HasPrefix(upper, "finepausa") || upper == "fbreak":
		return "F_pausa", 3, nil
	case strings.HasPrefix(upper, "utrasf") || strings.HasPrefix(upper, "uscitatrasf") || strings.HasPrefix(upper, "iniziotrasf"):
		return "U_trasf", 4, nil
	case strings.HasPrefix(upper, "rtrasf") || strings.HasPrefix(upper, "ritornotrasf") || strings.HasPrefix(upper, "rientrotrasf"):
		return "R_trasf", 5, nil
	case upper == "i_break":
		return "I_break", 6, nil
	case upper == "f_break":
		return "F_break", 7, nil
	default:
		return "", 0, fmt.Errorf("azione non riconosciuta: %q", raw)
	}
}

func importRows(db *sql.DB, deviceID uint32, rows []importRow, rule shiftRule) (importSummary, error) {
	summary := importSummary{}
	for _, row := range rows {
		summary.Processed++
		row.Timestamp = applyShiftRule(row.Timestamp, rule)

		if strings.TrimSpace(row.EmployeeName) == "" {
			if name, err := lookupEmployeeName(db, row.EmployeeID); err == nil && name != "" {
				row.EmployeeName = name
			}
		}

		rawTS, err := rawDeviceTimestampFromTime(row.Timestamp)
		if err != nil {
			summary.Skipped++
			log.Printf("Riga saltata employee=%d timestamp=%s: %v", row.EmployeeID, row.Timestamp.Format(time.RFC3339), err)
			continue
		}

		inserted, err := insertDeviceRecord(db, deviceID, row.EmployeeID, row.EmployeeName, row.Timestamp, rawTS, row.Action, row.StatusCode)
		switch {
		case err == nil && inserted:
			summary.Inserted++
		case errors.Is(err, errDuplicateRecord):
			summary.Duplicates++
		case err == nil && !inserted:
			summary.Duplicates++
		default:
			summary.Skipped++
			log.Printf("Errore inserimento employee=%d ts=%s action=%s: %v", row.EmployeeID, row.Timestamp.Format(time.RFC3339), row.Action, err)
		}
	}

	return summary, nil
}

func lookupEmployeeName(db *sql.DB, employeeID int) (string, error) {
	var name string
	err := db.QueryRow(`SELECT COALESCE(name, '') FROM employees WHERE id = ?`, employeeID).Scan(&name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(name), nil
}

func rawDeviceTimestampFromTime(timestamp time.Time) (uint32, error) {
	localTS := timestamp.In(europeRome())
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

func insertDeviceRecord(db *sql.DB, deviceID uint32, employeeID int, employeeName string, timestamp time.Time, rawDeviceTimestamp uint32, action string, statusCode int) (bool, error) {
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	rawInserted, err := insertDeviceRawRecord(tx, deviceID, employeeID, employeeName, timestamp, rawDeviceTimestamp, action, statusCode)
	if err != nil {
		return false, err
	}

	adopted, err := adoptLegacyDeviceRecord(tx, deviceID, employeeID, timestamp, rawDeviceTimestamp, statusCode)
	if err != nil {
		return false, err
	}

	if !adopted {
		_, err := tx.Exec(
			`INSERT INTO records (employee_id, employee_name, timestamp, action, status_code, source, device_id, raw_device_timestamp, latitude, longitude)
			 VALUES (?, ?, ?, ?, ?, 'device', ?, ?, NULL, NULL)`,
			employeeID,
			employeeName,
			timestamp.Format(time.RFC3339),
			action,
			statusCode,
			int64(deviceID),
			int64(rawDeviceTimestamp),
		)
		if err != nil {
			if isUniqueConstraintError(err) {
				return false, errDuplicateRecord
			}
			return false, err
		}
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}

	return rawInserted, nil
}

func insertDeviceRawRecord(tx *sql.Tx, deviceID uint32, employeeID int, employeeName string, timestamp time.Time, rawDeviceTimestamp uint32, action string, statusCode int) (bool, error) {
	_, err := tx.Exec(
		`INSERT INTO device_raw_records (device_id, employee_id, employee_name, raw_device_timestamp, parsed_timestamp, action, status_code, imported_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		int64(deviceID),
		employeeID,
		employeeName,
		int64(rawDeviceTimestamp),
		timestamp.Format(time.RFC3339),
		action,
		statusCode,
		time.Now().Format(time.RFC3339),
	)
	if err != nil {
		if isUniqueConstraintError(err) {
			return false, errDuplicateRecord
		}
		return false, err
	}
	return true, nil
}

func adoptLegacyDeviceRecord(tx *sql.Tx, deviceID uint32, employeeID int, timestamp time.Time, rawDeviceTimestamp uint32, statusCode int) (bool, error) {
	res, err := tx.Exec(
		`UPDATE records
		 SET device_id = ?, raw_device_timestamp = ?
		 WHERE id = (
			 SELECT id
			 FROM records
			 WHERE source = 'device'
			   AND employee_id = ?
			   AND timestamp = ?
			   AND status_code = ?
			   AND device_id IS NULL
			 ORDER BY id ASC
			 LIMIT 1
		 )`,
		int64(deviceID),
		int64(rawDeviceTimestamp),
		employeeID,
		timestamp.Format(time.RFC3339),
		statusCode,
	)
	if err != nil {
		return false, err
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func isUniqueConstraintError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
