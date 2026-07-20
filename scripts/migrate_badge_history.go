package main

import (
	"database/sql"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const badgeSchemaVersion = 1

type manifestRow struct {
	Line              int
	PersonKey         string
	DisplayName       string
	IsActive          bool
	AnvizEmployeeID   int
	CardSerial        int64
	ValidFrom         time.Time
	ValidTo           *time.Time
	ValidFromUnix     int64
	ValidToUnix       *int64
	BoundaryQuality   string
	Reason            string
	OriginalValidFrom string
	OriginalValidTo   string
}

type existingAssignment struct {
	ID              int64
	PersonKey       string
	DisplayName     string
	IsActive        bool
	AnvizEmployeeID int
	CardSerial      int64
	ValidFromUnix   int64
	ValidToUnix     *int64
	BoundaryQuality string
	Reason          string
	VoidedAtUnix    *int64
}

type preflightReport struct {
	ManifestRows            int
	People                  int
	EmployeeIDs             int
	ExistingSchemaVersion   int
	ExistingAssignments     int
	MissingEmployeeIDs      []int
	RecordCountBefore       int64
	RawRecordCountBefore    int64
	ManagedRecordCount      int64
	ManagedRawRecordCount   int64
	UncoveredRecordCount    int64
	UncoveredRawRecordCount int64
	UnmanagedRecordIDs      []int
	UnmanagedRawRecordIDs   []int
}

func main() {
	var dbPath string
	var manifestPath string
	var backupPath string
	var actor string
	var apply bool

	flag.StringVar(&dbPath, "db", "", "percorso del database attendance.db")
	flag.StringVar(&manifestPath, "file", "", "manifest CSV approvato")
	flag.StringVar(&backupPath, "backup", "", "backup obbligatorio per -apply")
	flag.StringVar(&actor, "actor", "badge-migration", "operatore registrato nello storico")
	flag.BoolVar(&apply, "apply", false, "applica schema e import; senza questo flag esegue solo il dry-run")
	flag.Parse()

	if strings.TrimSpace(dbPath) == "" || strings.TrimSpace(manifestPath) == "" {
		log.Fatal("parametri obbligatori: -db attendance.db -file badge-history.csv")
	}
	if apply && strings.TrimSpace(backupPath) == "" {
		log.Fatal("-apply richiede -backup /percorso/backup.db")
	}
	if strings.TrimSpace(actor) == "" {
		log.Fatal("-actor non puo essere vuoto")
	}

	manifest, err := readManifest(manifestPath)
	if err != nil {
		log.Fatalf("manifest non valido: %v", err)
	}
	if err := validateManifest(manifest); err != nil {
		log.Fatalf("manifest non valido: %v", err)
	}

	report, err := preflight(dbPath, manifest)
	if err != nil {
		log.Fatalf("preflight fallito: %v", err)
	}
	printPreflight(report)
	if len(report.MissingEmployeeIDs) > 0 || report.UncoveredRecordCount > 0 || report.UncoveredRawRecordCount > 0 {
		log.Fatal("preflight bloccante: correggere le anomalie prima dell'apply")
	}

	if !apply {
		fmt.Println("DRY-RUN OK: nessuna modifica eseguita")
		return
	}

	backupAbsolute, err := createBackup(dbPath, backupPath)
	if err != nil {
		log.Fatalf("backup fallito: %v", err)
	}
	fmt.Printf("Backup verificato: %s\n", backupAbsolute)

	insertedPeople, insertedAssignments, skippedAssignments, err := applyMigration(dbPath, manifest, actor)
	if err != nil {
		log.Fatalf("migrazione annullata: %v", err)
	}

	after, err := auditAfterApply(dbPath, report.RecordCountBefore, report.RawRecordCountBefore)
	if err != nil {
		log.Fatalf("migrazione applicata ma audit finale fallito: %v", err)
	}

	fmt.Printf("Persone create: %d\n", insertedPeople)
	fmt.Printf("Assegnazioni create: %d\n", insertedAssignments)
	fmt.Printf("Assegnazioni gia identiche: %d\n", skippedAssignments)
	fmt.Printf("Persone totali: %d\n", after.People)
	fmt.Printf("Assegnazioni totali: %d\n", after.Assignments)
	fmt.Println("APPLY OK: records e device_raw_records invariati")
}

func readManifest(path string) ([]manifestRow, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.TrimLeadingSpace = true
	header, err := reader.Read()
	if err != nil {
		return nil, err
	}
	columns := make(map[string]int)
	for index, name := range header {
		columns[strings.ToLower(strings.TrimSpace(name))] = index
	}
	required := []string{
		"person_key", "display_name", "is_active", "anviz_employee_id",
		"card_serial", "valid_from", "valid_to", "boundary_quality", "reason",
	}
	for _, name := range required {
		if _, ok := columns[name]; !ok {
			return nil, fmt.Errorf("colonna obbligatoria assente: %s", name)
		}
	}

	result := make([]manifestRow, 0)
	line := 1
	for {
		line++
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("riga %d: %w", line, err)
		}
		value := func(name string) string {
			index := columns[name]
			if index >= len(record) {
				return ""
			}
			return strings.TrimSpace(record[index])
		}

		active, err := strconv.ParseBool(value("is_active"))
		if err != nil {
			return nil, fmt.Errorf("riga %d: is_active non valido", line)
		}
		employeeID, err := strconv.Atoi(value("anviz_employee_id"))
		if err != nil || employeeID <= 0 {
			return nil, fmt.Errorf("riga %d: anviz_employee_id non valido", line)
		}
		cardSerial, err := strconv.ParseInt(value("card_serial"), 10, 64)
		if err != nil || cardSerial <= 0 || cardSerial > int64(^uint32(0)) {
			return nil, fmt.Errorf("riga %d: card_serial non valido", line)
		}
		validFrom, err := time.Parse(time.RFC3339, value("valid_from"))
		if err != nil {
			return nil, fmt.Errorf("riga %d: valid_from non RFC3339: %w", line, err)
		}

		var validTo *time.Time
		var validToUnix *int64
		if raw := value("valid_to"); raw != "" {
			parsed, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return nil, fmt.Errorf("riga %d: valid_to non RFC3339: %w", line, err)
			}
			unix := parsed.Unix()
			validTo = &parsed
			validToUnix = &unix
		}

		result = append(result, manifestRow{
			Line:              line,
			PersonKey:         value("person_key"),
			DisplayName:       value("display_name"),
			IsActive:          active,
			AnvizEmployeeID:   employeeID,
			CardSerial:        cardSerial,
			ValidFrom:         validFrom,
			ValidTo:           validTo,
			ValidFromUnix:     validFrom.Unix(),
			ValidToUnix:       validToUnix,
			BoundaryQuality:   value("boundary_quality"),
			Reason:            value("reason"),
			OriginalValidFrom: value("valid_from"),
			OriginalValidTo:   value("valid_to"),
		})
	}
	if len(result) == 0 {
		return nil, errors.New("manifest vuoto")
	}
	return result, nil
}

func validateManifest(rows []manifestRow) error {
	people := make(map[string]manifestRow)
	byEmployee := make(map[int][]manifestRow)
	byCard := make(map[int64][]manifestRow)
	allowedQuality := map[string]bool{"exact": true, "inferred": true, "unknown": true}
	allowedReason := map[string]bool{
		"initial": true, "replacement": true, "found": true,
		"reassigned": true, "legacy_import": true, "correction": true,
	}

	for _, row := range rows {
		if row.PersonKey == "" || row.DisplayName == "" {
			return fmt.Errorf("riga %d: person_key/display_name vuoti", row.Line)
		}
		if row.ValidToUnix != nil && *row.ValidToUnix <= row.ValidFromUnix {
			return fmt.Errorf("riga %d: intervallo non valido", row.Line)
		}
		if !allowedQuality[row.BoundaryQuality] {
			return fmt.Errorf("riga %d: boundary_quality non valido", row.Line)
		}
		if !allowedReason[row.Reason] {
			return fmt.Errorf("riga %d: reason non valido", row.Line)
		}
		if previous, ok := people[row.PersonKey]; ok {
			if previous.DisplayName != row.DisplayName || previous.IsActive != row.IsActive {
				return fmt.Errorf("riga %d: persona %s incoerente", row.Line, row.PersonKey)
			}
		} else {
			people[row.PersonKey] = row
		}
		byEmployee[row.AnvizEmployeeID] = append(byEmployee[row.AnvizEmployeeID], row)
		byCard[row.CardSerial] = append(byCard[row.CardSerial], row)
	}

	for employeeID, group := range byEmployee {
		if overlap := findOverlap(group); overlap != "" {
			return fmt.Errorf("employee_id %d: %s", employeeID, overlap)
		}
	}
	for cardSerial, group := range byCard {
		if overlap := findOverlap(group); overlap != "" {
			return fmt.Errorf("card_serial %d: %s", cardSerial, overlap)
		}
	}
	return nil
}

func findOverlap(rows []manifestRow) string {
	sort.Slice(rows, func(i, j int) bool { return rows[i].ValidFromUnix < rows[j].ValidFromUnix })
	for index := 1; index < len(rows); index++ {
		previous := rows[index-1]
		current := rows[index]
		if intervalsOverlap(previous.ValidFromUnix, previous.ValidToUnix, current.ValidFromUnix, current.ValidToUnix) {
			return fmt.Sprintf("intervalli sovrapposti alle righe %d e %d", previous.Line, current.Line)
		}
	}
	return ""
}

func intervalsOverlap(leftFrom int64, leftTo *int64, rightFrom int64, rightTo *int64) bool {
	const maxInt64 = int64(^uint64(0) >> 1)
	leftEnd := maxInt64
	if leftTo != nil {
		leftEnd = *leftTo
	}
	rightEnd := maxInt64
	if rightTo != nil {
		rightEnd = *rightTo
	}
	return leftFrom < rightEnd && rightFrom < leftEnd
}

func preflight(dbPath string, manifest []manifestRow) (preflightReport, error) {
	report := preflightReport{ManifestRows: len(manifest)}
	people := make(map[string]struct{})
	employeeIDs := make(map[int]struct{})
	for _, row := range manifest {
		people[row.PersonKey] = struct{}{}
		employeeIDs[row.AnvizEmployeeID] = struct{}{}
	}
	report.People = len(people)
	report.EmployeeIDs = len(employeeIDs)

	db, err := openDatabase(dbPath, true)
	if err != nil {
		return report, err
	}
	defer db.Close()

	var integrity string
	if err := db.QueryRow("PRAGMA quick_check").Scan(&integrity); err != nil || integrity != "ok" {
		return report, fmt.Errorf("integrita SQLite non valida: %s (%v)", integrity, err)
	}

	report.ExistingSchemaVersion, err = currentSchemaVersion(db)
	if err != nil {
		return report, err
	}
	if exists, err := tableExists(db, "person_badge_history"); err != nil {
		return report, err
	} else if exists {
		if err := db.QueryRow(`SELECT COUNT(*) FROM person_badge_history WHERE voided_at_utc IS NULL`).Scan(&report.ExistingAssignments); err != nil {
			return report, err
		}
	}

	existingEmployees, err := loadIDSet(db, "SELECT id FROM employees")
	if err != nil {
		return report, err
	}
	for id := range employeeIDs {
		if _, ok := existingEmployees[id]; !ok {
			report.MissingEmployeeIDs = append(report.MissingEmployeeIDs, id)
		}
	}
	sort.Ints(report.MissingEmployeeIDs)

	if err := db.QueryRow(`SELECT COUNT(*) FROM records`).Scan(&report.RecordCountBefore); err != nil {
		return report, err
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM device_raw_records`).Scan(&report.RawRecordCountBefore); err != nil {
		return report, err
	}

	assignmentsByEmployee := make(map[int][]manifestRow)
	for _, row := range manifest {
		assignmentsByEmployee[row.AnvizEmployeeID] = append(assignmentsByEmployee[row.AnvizEmployeeID], row)
	}
	report.ManagedRecordCount, report.UncoveredRecordCount, report.UnmanagedRecordIDs, err = coverageForTable(
		db, "records", "timestamp", assignmentsByEmployee,
	)
	if err != nil {
		return report, err
	}
	report.ManagedRawRecordCount, report.UncoveredRawRecordCount, report.UnmanagedRawRecordIDs, err = coverageForTable(
		db, "device_raw_records", "parsed_timestamp", assignmentsByEmployee,
	)
	if err != nil {
		return report, err
	}
	return report, nil
}

func coverageForTable(db *sql.DB, table, timestampColumn string, assignments map[int][]manifestRow) (int64, int64, []int, error) {
	rows, err := db.Query(fmt.Sprintf("SELECT employee_id, %s FROM %s", timestampColumn, table))
	if err != nil {
		return 0, 0, nil, err
	}
	defer rows.Close()

	var covered int64
	var uncovered int64
	unmanaged := make(map[int]struct{})
	for rows.Next() {
		var id int
		var timestampRaw string
		if err := rows.Scan(&id, &timestampRaw); err != nil {
			return 0, 0, nil, err
		}
		intervals, managed := assignments[id]
		if !managed {
			unmanaged[id] = struct{}{}
			continue
		}
		timestamp, err := time.Parse(time.RFC3339, timestampRaw)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("%s: timestamp non RFC3339 per employee_id %d: %q", table, id, timestampRaw)
		}
		unix := timestamp.Unix()
		matched := false
		for _, interval := range intervals {
			if unix < interval.ValidFromUnix {
				continue
			}
			if interval.ValidToUnix != nil && unix >= *interval.ValidToUnix {
				continue
			}
			matched = true
			break
		}
		if matched {
			covered++
		} else {
			uncovered++
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, nil, err
	}
	ids := make([]int, 0, len(unmanaged))
	for id := range unmanaged {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return covered, uncovered, ids, nil
}

func printPreflight(report preflightReport) {
	fmt.Printf("Manifest righe: %d\n", report.ManifestRows)
	fmt.Printf("Persone: %d\n", report.People)
	fmt.Printf("ID Anviz: %d\n", report.EmployeeIDs)
	fmt.Printf("Versione schema esistente: %d\n", report.ExistingSchemaVersion)
	fmt.Printf("Assegnazioni esistenti: %d\n", report.ExistingAssignments)
	fmt.Printf("ID manifest assenti da employees: %d\n", len(report.MissingEmployeeIDs))
	fmt.Printf("Records totali: %d\n", report.RecordCountBefore)
	fmt.Printf("Raw records totali: %d\n", report.RawRecordCountBefore)
	fmt.Printf("Records coperti dal manifest: %d\n", report.ManagedRecordCount)
	fmt.Printf("Raw coperti dal manifest: %d\n", report.ManagedRawRecordCount)
	fmt.Printf("Records non coperti negli ID gestiti: %d\n", report.UncoveredRecordCount)
	fmt.Printf("Raw non coperti negli ID gestiti: %d\n", report.UncoveredRawRecordCount)
	fmt.Printf("ID records esclusi dal manifest: %s\n", formatIDs(report.UnmanagedRecordIDs))
	fmt.Printf("ID raw esclusi dal manifest: %s\n", formatIDs(report.UnmanagedRawRecordIDs))
}

func formatIDs(ids []int) string {
	if len(ids) == 0 {
		return "nessuno"
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.Itoa(id))
	}
	return strings.Join(parts, ",")
}

func createBackup(dbPath, backupPath string) (string, error) {
	absoluteBackup, err := filepath.Abs(backupPath)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(absoluteBackup); err == nil {
		return "", fmt.Errorf("il backup esiste gia: %s", absoluteBackup)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(absoluteBackup), 0750); err != nil {
		return "", err
	}

	db, err := openDatabase(dbPath, false)
	if err != nil {
		return "", err
	}
	defer db.Close()
	escaped := strings.ReplaceAll(filepath.ToSlash(absoluteBackup), "'", "''")
	if _, err := db.Exec("VACUUM INTO '" + escaped + "'"); err != nil {
		return "", err
	}

	backupDB, err := openDatabase(absoluteBackup, true)
	if err != nil {
		return "", err
	}
	defer backupDB.Close()
	var integrity string
	if err := backupDB.QueryRow("PRAGMA quick_check").Scan(&integrity); err != nil {
		return "", err
	}
	if integrity != "ok" {
		return "", fmt.Errorf("backup non integro: %s", integrity)
	}
	return absoluteBackup, nil
}

func applyMigration(dbPath string, manifest []manifestRow, actor string) (int, int, int, error) {
	db, err := openDatabase(dbPath, false)
	if err != nil {
		return 0, 0, 0, err
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return 0, 0, 0, err
	}

	tx, err := db.Begin()
	if err != nil {
		return 0, 0, 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(badgeSchemaSQL); err != nil {
		return 0, 0, 0, err
	}
	if _, err := tx.Exec(`
		INSERT INTO schema_migrations (version, name, applied_at_utc)
		VALUES (?, ?, ?)
		ON CONFLICT(version) DO NOTHING
	`, badgeSchemaVersion, "people_and_person_badge_history", time.Now().Unix()); err != nil {
		return 0, 0, 0, err
	}

	peopleCreated := 0
	peopleSeen := make(map[string]int64)
	for _, row := range manifest {
		if _, ok := peopleSeen[row.PersonKey]; ok {
			continue
		}
		var existingID int64
		err := tx.QueryRow(`SELECT id FROM people WHERE person_key = ?`, row.PersonKey).Scan(&existingID)
		switch {
		case err == nil:
			if _, err := tx.Exec(`
				UPDATE people
				SET display_name = ?, is_active = ?, updated_at_utc = ?
				WHERE id = ?
			`, row.DisplayName, boolInt(row.IsActive), time.Now().Unix(), existingID); err != nil {
				return 0, 0, 0, err
			}
			peopleSeen[row.PersonKey] = existingID
		case errors.Is(err, sql.ErrNoRows):
			result, err := tx.Exec(`
				INSERT INTO people (
					person_key, display_name, is_active, inactive_at_utc,
					created_at_utc, updated_at_utc
				) VALUES (?, ?, ?, NULL, ?, ?)
			`, row.PersonKey, row.DisplayName, boolInt(row.IsActive), time.Now().Unix(), time.Now().Unix())
			if err != nil {
				return 0, 0, 0, err
			}
			id, err := result.LastInsertId()
			if err != nil {
				return 0, 0, 0, err
			}
			peopleSeen[row.PersonKey] = id
			peopleCreated++
		default:
			return 0, 0, 0, err
		}
	}

	assignmentsCreated := 0
	assignmentsSkipped := 0
	for _, row := range manifest {
		personID := peopleSeen[row.PersonKey]
		existing, found, err := findExistingAssignment(tx, personID, row.AnvizEmployeeID, row.ValidFromUnix)
		if err != nil {
			return 0, 0, 0, err
		}
		if found {
			if !assignmentMatches(existing, row) {
				return 0, 0, 0, fmt.Errorf("assegnazione esistente diversa per employee_id %d", row.AnvizEmployeeID)
			}
			assignmentsSkipped++
			continue
		}
		if _, err := tx.Exec(`
			INSERT INTO person_badge_history (
				person_id, anviz_employee_id, card_serial,
				valid_from_utc, valid_to_utc, boundary_quality, reason,
				created_by, created_at_utc, voided_at_utc, void_reason
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL)
		`, personID, row.AnvizEmployeeID, row.CardSerial, row.ValidFromUnix,
			nullableInt64(row.ValidToUnix), row.BoundaryQuality, row.Reason,
			actor, time.Now().Unix()); err != nil {
			return 0, 0, 0, fmt.Errorf("riga manifest %d: %w", row.Line, err)
		}
		assignmentsCreated++
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, 0, err
	}
	return peopleCreated, assignmentsCreated, assignmentsSkipped, nil
}

func findExistingAssignment(tx *sql.Tx, personID int64, employeeID int, validFrom int64) (existingAssignment, bool, error) {
	var result existingAssignment
	var active int
	var validTo sql.NullInt64
	var voidedAt sql.NullInt64
	err := tx.QueryRow(`
		SELECT h.id, p.person_key, p.display_name, p.is_active,
		       h.anviz_employee_id, h.card_serial,
		       h.valid_from_utc, h.valid_to_utc,
		       h.boundary_quality, h.reason, h.voided_at_utc
		FROM person_badge_history h
		JOIN people p ON p.id = h.person_id
		WHERE h.person_id = ?
		  AND h.anviz_employee_id = ?
		  AND h.valid_from_utc = ?
		  AND h.voided_at_utc IS NULL
	`, personID, employeeID, validFrom).Scan(
		&result.ID, &result.PersonKey, &result.DisplayName, &active,
		&result.AnvizEmployeeID, &result.CardSerial,
		&result.ValidFromUnix, &validTo,
		&result.BoundaryQuality, &result.Reason, &voidedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	result.IsActive = active == 1
	if validTo.Valid {
		value := validTo.Int64
		result.ValidToUnix = &value
	}
	if voidedAt.Valid {
		value := voidedAt.Int64
		result.VoidedAtUnix = &value
	}
	return result, true, nil
}

func assignmentMatches(existing existingAssignment, row manifestRow) bool {
	return existing.PersonKey == row.PersonKey &&
		existing.DisplayName == row.DisplayName &&
		existing.IsActive == row.IsActive &&
		existing.AnvizEmployeeID == row.AnvizEmployeeID &&
		existing.CardSerial == row.CardSerial &&
		existing.ValidFromUnix == row.ValidFromUnix &&
		equalNullableInt64(existing.ValidToUnix, row.ValidToUnix) &&
		existing.BoundaryQuality == row.BoundaryQuality &&
		existing.Reason == row.Reason &&
		existing.VoidedAtUnix == nil
}

func equalNullableInt64(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func nullableInt64(value *int64) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

type afterApplyAudit struct {
	People      int
	Assignments int
}

func auditAfterApply(dbPath string, expectedRecords, expectedRaw int64) (afterApplyAudit, error) {
	var report afterApplyAudit
	db, err := openDatabase(dbPath, true)
	if err != nil {
		return report, err
	}
	defer db.Close()

	var integrity string
	if err := db.QueryRow("PRAGMA quick_check").Scan(&integrity); err != nil || integrity != "ok" {
		return report, fmt.Errorf("quick_check: %s (%v)", integrity, err)
	}
	rows, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		return report, err
	}
	if rows.Next() {
		rows.Close()
		return report, errors.New("foreign_key_check ha trovato errori")
	}
	rows.Close()

	var records int64
	var raw int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM records`).Scan(&records); err != nil {
		return report, err
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM device_raw_records`).Scan(&raw); err != nil {
		return report, err
	}
	if records != expectedRecords || raw != expectedRaw {
		return report, fmt.Errorf("conteggi storici cambiati: records %d/%d raw %d/%d", records, expectedRecords, raw, expectedRaw)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM people`).Scan(&report.People); err != nil {
		return report, err
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM person_badge_history WHERE voided_at_utc IS NULL`).Scan(&report.Assignments); err != nil {
		return report, err
	}
	return report, nil
}

func openDatabase(path string, readOnly bool) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("il percorso DB e una directory: %s", absolute)
	}
	mode := "rwc"
	if readOnly {
		mode = "ro"
	}
	dsn := "file:" + filepath.ToSlash(absolute) + "?mode=" + mode + "&_pragma=busy_timeout(5000)"
	if readOnly {
		dsn += "&_pragma=query_only(1)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func tableExists(db *sql.DB, name string) (bool, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&count)
	return count > 0, err
}

func loadIDSet(db *sql.DB, query string) (map[int]struct{}, error) {
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[int]struct{})
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = struct{}{}
	}
	return result, rows.Err()
}

func currentSchemaVersion(db *sql.DB) (int, error) {
	exists, err := tableExists(db, "schema_migrations")
	if err != nil || !exists {
		return 0, err
	}
	var version int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}

const badgeSchemaSQL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at_utc INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS people (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    person_key TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    is_active INTEGER NOT NULL DEFAULT 1 CHECK (is_active IN (0, 1)),
    inactive_at_utc INTEGER,
    created_at_utc INTEGER NOT NULL,
    updated_at_utc INTEGER NOT NULL,
    CHECK ((is_active = 1 AND inactive_at_utc IS NULL) OR is_active = 0)
);

CREATE TABLE IF NOT EXISTS person_badge_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    person_id INTEGER NOT NULL,
    anviz_employee_id INTEGER NOT NULL CHECK (anviz_employee_id > 0),
    card_serial INTEGER NOT NULL CHECK (card_serial BETWEEN 1 AND 4294967295),
    valid_from_utc INTEGER NOT NULL,
    valid_to_utc INTEGER,
    boundary_quality TEXT NOT NULL CHECK (boundary_quality IN ('exact', 'inferred', 'unknown')),
    reason TEXT NOT NULL CHECK (reason IN ('initial', 'replacement', 'found', 'reassigned', 'legacy_import', 'correction')),
    created_by TEXT NOT NULL,
    created_at_utc INTEGER NOT NULL,
    voided_at_utc INTEGER,
    void_reason TEXT,
    FOREIGN KEY (person_id) REFERENCES people(id) ON DELETE RESTRICT,
    CHECK (valid_to_utc IS NULL OR valid_to_utc > valid_from_utc),
    CHECK ((voided_at_utc IS NULL AND void_reason IS NULL) OR (voided_at_utc IS NOT NULL AND void_reason IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS idx_badge_history_employee_time
ON person_badge_history (anviz_employee_id, valid_from_utc, valid_to_utc)
WHERE voided_at_utc IS NULL;

CREATE INDEX IF NOT EXISTS idx_badge_history_person_time
ON person_badge_history (person_id, valid_from_utc, valid_to_utc)
WHERE voided_at_utc IS NULL;

CREATE INDEX IF NOT EXISTS idx_badge_history_card_time
ON person_badge_history (card_serial, valid_from_utc, valid_to_utc)
WHERE voided_at_utc IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_badge_history_identity
ON person_badge_history (person_id, anviz_employee_id, valid_from_utc)
WHERE voided_at_utc IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_badge_history_open_employee
ON person_badge_history (anviz_employee_id)
WHERE valid_to_utc IS NULL AND voided_at_utc IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_badge_history_open_card
ON person_badge_history (card_serial)
WHERE valid_to_utc IS NULL AND voided_at_utc IS NULL;

CREATE INDEX IF NOT EXISTS idx_records_employee_timestamp
ON records (employee_id, timestamp);

CREATE TRIGGER IF NOT EXISTS trg_badge_history_employee_overlap_insert
BEFORE INSERT ON person_badge_history
WHEN NEW.voided_at_utc IS NULL AND EXISTS (
    SELECT 1 FROM person_badge_history existing
    WHERE existing.voided_at_utc IS NULL
      AND existing.anviz_employee_id = NEW.anviz_employee_id
      AND NEW.valid_from_utc < COALESCE(existing.valid_to_utc, 9223372036854775807)
      AND existing.valid_from_utc < COALESCE(NEW.valid_to_utc, 9223372036854775807)
)
BEGIN
    SELECT RAISE(ABORT, 'sovrapposizione assegnazioni per anviz_employee_id');
END;

CREATE TRIGGER IF NOT EXISTS trg_badge_history_card_overlap_insert
BEFORE INSERT ON person_badge_history
WHEN NEW.voided_at_utc IS NULL AND EXISTS (
    SELECT 1 FROM person_badge_history existing
    WHERE existing.voided_at_utc IS NULL
      AND existing.card_serial = NEW.card_serial
      AND NEW.valid_from_utc < COALESCE(existing.valid_to_utc, 9223372036854775807)
      AND existing.valid_from_utc < COALESCE(NEW.valid_to_utc, 9223372036854775807)
)
BEGIN
    SELECT RAISE(ABORT, 'sovrapposizione assegnazioni per card_serial');
END;

CREATE TRIGGER IF NOT EXISTS trg_badge_history_employee_overlap_update
BEFORE UPDATE OF anviz_employee_id, valid_from_utc, valid_to_utc, voided_at_utc ON person_badge_history
WHEN NEW.voided_at_utc IS NULL AND EXISTS (
    SELECT 1 FROM person_badge_history existing
    WHERE existing.id <> OLD.id
      AND existing.voided_at_utc IS NULL
      AND existing.anviz_employee_id = NEW.anviz_employee_id
      AND NEW.valid_from_utc < COALESCE(existing.valid_to_utc, 9223372036854775807)
      AND existing.valid_from_utc < COALESCE(NEW.valid_to_utc, 9223372036854775807)
)
BEGIN
    SELECT RAISE(ABORT, 'sovrapposizione assegnazioni per anviz_employee_id');
END;

CREATE TRIGGER IF NOT EXISTS trg_badge_history_card_overlap_update
BEFORE UPDATE OF card_serial, valid_from_utc, valid_to_utc, voided_at_utc ON person_badge_history
WHEN NEW.voided_at_utc IS NULL AND EXISTS (
    SELECT 1 FROM person_badge_history existing
    WHERE existing.id <> OLD.id
      AND existing.voided_at_utc IS NULL
      AND existing.card_serial = NEW.card_serial
      AND NEW.valid_from_utc < COALESCE(existing.valid_to_utc, 9223372036854775807)
      AND existing.valid_from_utc < COALESCE(NEW.valid_to_utc, 9223372036854775807)
)
BEGIN
    SELECT RAISE(ABORT, 'sovrapposizione assegnazioni per card_serial');
END;
`
