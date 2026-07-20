//go:build ignore

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
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	badgeHistorySchemaVersion = 1
	badgeAdminSchemaVersion   = 2
	maxCardSerial             = int64(4294967295)
)

type commandConfig struct {
	DBPath           string
	PersonKey        string
	DisplayName      string
	CreatePerson     bool
	ReactivatePerson bool
	AnvizEmployeeID  int
	CardSerial       int64
	EffectiveAt      time.Time
	Reason           string
	Actor            string
	Apply            bool
	BackupPath       string
	AllowFuture      bool
	ListCurrent      bool
}

type personRecord struct {
	ID          int64
	PersonKey   string
	DisplayName string
	IsActive    bool
}

type assignmentRecord struct {
	ID              int64
	PersonID        int64
	PersonKey       string
	DisplayName     string
	AnvizEmployeeID int
	CardSerial      int64
	ValidFromUTC    int64
	ValidToUTC      *int64
}

type reassignmentPlan struct {
	Person             personRecord
	CreatePerson       bool
	EmployeeName       string
	KeepAssignment     *assignmentRecord
	CloseAssignments   []assignmentRecord
	ConflictingFuture  []assignmentRecord
	AffectedRecords    int64
	AffectedRawRecords int64
}

type queryRunner interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

func main() {
	cfg, err := parseFlags()
	if err != nil {
		log.Fatal(err)
	}

	db, err := openDatabase(cfg.DBPath, true)
	if err != nil {
		log.Fatalf("apertura database: %v", err)
	}

	if err := verifyDatabase(db); err != nil {
		db.Close()
		log.Fatalf("preflight database: %v", err)
	}
	if cfg.ListCurrent {
		if err := writeCurrentAssignmentsCSV(db, os.Stdout); err != nil {
			db.Close()
			log.Fatalf("inventario assegnazioni correnti: %v", err)
		}
		db.Close()
		return
	}
	plan, err := buildPlan(db, cfg)
	if err != nil {
		db.Close()
		log.Fatalf("preflight riassegnazione: %v", err)
	}
	printPlan(cfg, plan)
	db.Close()

	if len(plan.ConflictingFuture) > 0 {
		log.Fatal("preflight bloccante: esistono assegnazioni future o con inizio coincidente")
	}
	if !cfg.Apply {
		fmt.Println("DRY-RUN OK: nessuna modifica eseguita")
		return
	}

	backup, err := createBackup(cfg.DBPath, cfg.BackupPath)
	if err != nil {
		log.Fatalf("backup fallito: %v", err)
	}
	fmt.Printf("Backup verificato: %s\n", backup)

	beforeRecords, beforeRaw, err := recordCounts(cfg.DBPath)
	if err != nil {
		log.Fatalf("conteggi pre-apply: %v", err)
	}
	result, err := applyReassignment(cfg)
	if err != nil {
		log.Fatalf("riassegnazione annullata: %v", err)
	}

	if err := verifyAfterApply(cfg.DBPath, beforeRecords, beforeRaw); err != nil {
		log.Fatalf("verifica post-apply fallita: %v", err)
	}

	fmt.Printf("Operazione audit: %s\n", result.OperationID)
	fmt.Printf("Persona creata: %t\n", result.PersonCreated)
	fmt.Printf("Persona riattivata: %t\n", result.PersonReactivated)
	fmt.Printf("Assegnazioni chiuse: %d\n", result.ClosedCount)
	fmt.Printf("Assegnazione creata: %t\n", result.AssignmentCreated)
	fmt.Println("APPLY OK: records e device_raw_records invariati")
}

func parseFlags() (commandConfig, error) {
	var cfg commandConfig
	var effectiveRaw string
	var cardRaw string

	flag.StringVar(&cfg.DBPath, "db", "", "percorso attendance.db")
	flag.StringVar(&cfg.PersonKey, "person-key", "", "identificativo stabile della persona")
	flag.StringVar(&cfg.DisplayName, "display-name", "", "nome persona; obbligatorio con -create-person")
	flag.BoolVar(&cfg.CreatePerson, "create-person", false, "consente la creazione di una nuova persona")
	flag.BoolVar(&cfg.ReactivatePerson, "reactivate-person", false, "riattiva esplicitamente una persona esistente inattiva")
	flag.IntVar(&cfg.AnvizEmployeeID, "anviz-id", 0, "ID dipendente configurato su Anviz")
	flag.StringVar(&cardRaw, "card-serial", "", "seriale numerico del badge")
	flag.StringVar(&effectiveRaw, "effective-at", "", "inizio assegnazione RFC3339 con fuso orario")
	flag.StringVar(&cfg.Reason, "reason", "", "replacement, found, reassigned oppure correction")
	flag.StringVar(&cfg.Actor, "actor", "", "operatore responsabile")
	flag.BoolVar(&cfg.Apply, "apply", false, "applica la riassegnazione; default dry-run")
	flag.StringVar(&cfg.BackupPath, "backup", "", "backup obbligatorio con -apply")
	flag.BoolVar(&cfg.AllowFuture, "allow-future", false, "consente effective-at oltre cinque minuti nel futuro")
	flag.BoolVar(&cfg.ListCurrent, "list-current", false, "stampa CSV delle assegnazioni correnti senza dati sensibili")
	flag.Parse()

	cfg.DBPath = strings.TrimSpace(cfg.DBPath)
	cfg.PersonKey = strings.TrimSpace(cfg.PersonKey)
	cfg.DisplayName = strings.TrimSpace(cfg.DisplayName)
	cfg.Reason = strings.ToLower(strings.TrimSpace(cfg.Reason))
	cfg.Actor = strings.TrimSpace(cfg.Actor)
	cfg.BackupPath = strings.TrimSpace(cfg.BackupPath)

	if cfg.DBPath == "" {
		return cfg, errors.New("richiesto: -db")
	}
	if cfg.ListCurrent {
		if cfg.Apply {
			return cfg, errors.New("-list-current non puo essere combinato con -apply")
		}
		return cfg, nil
	}
	if cfg.PersonKey == "" || cfg.AnvizEmployeeID <= 0 || strings.TrimSpace(cardRaw) == "" || strings.TrimSpace(effectiveRaw) == "" {
		return cfg, errors.New("richiesti: -db, -person-key, -anviz-id, -card-serial, -effective-at")
	}
	if cfg.Actor == "" || cfg.Reason == "" {
		return cfg, errors.New("richiesti: -actor e -reason")
	}
	if cfg.CreatePerson && cfg.DisplayName == "" {
		return cfg, errors.New("-create-person richiede -display-name")
	}
	if cfg.CreatePerson && cfg.ReactivatePerson {
		return cfg, errors.New("-create-person e -reactivate-person non possono essere usati insieme")
	}
	if cfg.Apply && cfg.BackupPath == "" {
		return cfg, errors.New("-apply richiede -backup con un percorso nuovo")
	}
	validReasons := map[string]bool{"replacement": true, "found": true, "reassigned": true, "correction": true}
	if !validReasons[cfg.Reason] {
		return cfg, fmt.Errorf("reason %q non valido", cfg.Reason)
	}

	card, err := strconv.ParseInt(strings.TrimSpace(cardRaw), 10, 64)
	if err != nil || card <= 0 || card > maxCardSerial {
		return cfg, errors.New("card-serial deve essere compreso tra 1 e 4294967295")
	}
	cfg.CardSerial = card

	effective, err := time.Parse(time.RFC3339, strings.TrimSpace(effectiveRaw))
	if err != nil {
		return cfg, fmt.Errorf("effective-at non valido: usa RFC3339 con fuso, ad esempio 2026-07-21T09:00:00+02:00")
	}
	cfg.EffectiveAt = effective
	if !cfg.AllowFuture && effective.After(time.Now().Add(5*time.Minute)) {
		return cfg, errors.New("effective-at e nel futuro: correggi l'orario oppure usa esplicitamente -allow-future")
	}

	return cfg, nil
}

func writeCurrentAssignmentsCSV(runner queryRunner, output io.Writer) error {
	rows, err := runner.Query(`
		SELECT p.person_key, p.display_name, p.is_active,
		       h.anviz_employee_id, h.card_serial, h.valid_from_utc
		FROM person_badge_history h
		JOIN people p ON p.id = h.person_id
		WHERE h.voided_at_utc IS NULL
		  AND h.valid_to_utc IS NULL
		ORDER BY lower(p.display_name), p.person_key, h.anviz_employee_id
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	writer := csv.NewWriter(output)
	if err := writer.Write([]string{"person_key", "display_name", "is_active", "anviz_employee_id", "card_serial", "valid_from"}); err != nil {
		return err
	}
	for rows.Next() {
		var personKey, displayName string
		var active int
		var employeeID int
		var cardSerial, validFrom int64
		if err := rows.Scan(&personKey, &displayName, &active, &employeeID, &cardSerial, &validFrom); err != nil {
			return err
		}
		if err := writer.Write([]string{
			personKey,
			displayName,
			strconv.FormatBool(active == 1),
			strconv.Itoa(employeeID),
			strconv.FormatInt(cardSerial, 10),
			formatUnix(validFrom),
		}); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writer.Flush()
	return writer.Error()
}

func buildPlan(runner queryRunner, cfg commandConfig) (reassignmentPlan, error) {
	var plan reassignmentPlan
	person, found, err := findPerson(runner, cfg.PersonKey)
	if err != nil {
		return plan, err
	}
	if !found {
		if !cfg.CreatePerson {
			return plan, fmt.Errorf("persona %q non trovata; per crearla usa -create-person e -display-name", cfg.PersonKey)
		}
		plan.CreatePerson = true
		plan.Person = personRecord{PersonKey: cfg.PersonKey, DisplayName: cfg.DisplayName, IsActive: true}
	} else {
		if cfg.DisplayName != "" && !strings.EqualFold(strings.TrimSpace(cfg.DisplayName), strings.TrimSpace(person.DisplayName)) {
			return plan, fmt.Errorf("display-name diverso: DB=%q richiesto=%q", person.DisplayName, cfg.DisplayName)
		}
		if !person.IsActive && !cfg.ReactivatePerson {
			return plan, fmt.Errorf("persona %q inattiva: usa -reactivate-person solo se il dipendente e realmente rientrato", cfg.PersonKey)
		}
		plan.Person = person
	}

	if err := runner.QueryRow(`SELECT COALESCE(name, '') FROM employees WHERE id = ?`, cfg.AnvizEmployeeID).Scan(&plan.EmployeeName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return plan, fmt.Errorf("anviz-id %d assente da employees: esegui prima Sync Staff", cfg.AnvizEmployeeID)
		}
		return plan, err
	}

	conflicts, err := loadOverlappingAssignments(runner, plan.Person.ID, cfg)
	if err != nil {
		return plan, err
	}
	effectiveUnix := cfg.EffectiveAt.Unix()
	for i := range conflicts {
		assignment := conflicts[i]
		isTarget := plan.Person.ID != 0 &&
			assignment.PersonID == plan.Person.ID &&
			assignment.AnvizEmployeeID == cfg.AnvizEmployeeID &&
			assignment.CardSerial == cfg.CardSerial

		if isTarget && assignment.ValidFromUTC <= effectiveUnix && assignment.ValidToUTC == nil {
			copy := assignment
			plan.KeepAssignment = &copy
			continue
		}
		if assignment.ValidFromUTC >= effectiveUnix {
			plan.ConflictingFuture = append(plan.ConflictingFuture, assignment)
			continue
		}
		plan.CloseAssignments = append(plan.CloseAssignments, assignment)
	}

	plan.AffectedRecords, err = countRowsFrom(runner, "records", "timestamp", cfg.AnvizEmployeeID, effectiveUnix)
	if err != nil {
		return plan, err
	}
	plan.AffectedRawRecords, err = countRowsFrom(runner, "device_raw_records", "parsed_timestamp", cfg.AnvizEmployeeID, effectiveUnix)
	if err != nil {
		return plan, err
	}
	return plan, nil
}

func findPerson(runner queryRunner, personKey string) (personRecord, bool, error) {
	var person personRecord
	var active int
	err := runner.QueryRow(`
		SELECT id, person_key, display_name, is_active
		FROM people
		WHERE person_key = ?
	`, personKey).Scan(&person.ID, &person.PersonKey, &person.DisplayName, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return person, false, nil
	}
	if err != nil {
		return person, false, err
	}
	person.IsActive = active == 1
	return person, true, nil
}

func loadOverlappingAssignments(runner queryRunner, personID int64, cfg commandConfig) ([]assignmentRecord, error) {
	query := `
		SELECT h.id, h.person_id, p.person_key, p.display_name,
		       h.anviz_employee_id, h.card_serial, h.valid_from_utc, h.valid_to_utc
		FROM person_badge_history h
		JOIN people p ON p.id = h.person_id
		WHERE h.voided_at_utc IS NULL
		  AND (h.valid_to_utc IS NULL OR h.valid_to_utc > ?)
		  AND (h.anviz_employee_id = ? OR h.card_serial = ?`
	args := []interface{}{cfg.EffectiveAt.Unix(), cfg.AnvizEmployeeID, cfg.CardSerial}
	if personID != 0 {
		query += ` OR h.person_id = ?`
		args = append(args, personID)
	}
	query += `) ORDER BY h.valid_from_utc, h.id`

	rows, err := runner.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []assignmentRecord
	for rows.Next() {
		var row assignmentRecord
		var validTo sql.NullInt64
		if err := rows.Scan(
			&row.ID, &row.PersonID, &row.PersonKey, &row.DisplayName,
			&row.AnvizEmployeeID, &row.CardSerial, &row.ValidFromUTC, &validTo,
		); err != nil {
			return nil, err
		}
		if validTo.Valid {
			value := validTo.Int64
			row.ValidToUTC = &value
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func countRowsFrom(runner queryRunner, table, timestampColumn string, employeeID int, effectiveUnix int64) (int64, error) {
	query := fmt.Sprintf(`SELECT %s FROM %s WHERE employee_id = ?`, timestampColumn, table)
	rows, err := runner.Query(query, employeeID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var count int64
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return 0, err
		}
		timestamp, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return 0, fmt.Errorf("%s contiene timestamp non RFC3339: %q", table, raw)
		}
		if timestamp.Unix() >= effectiveUnix {
			count++
		}
	}
	return count, rows.Err()
}

func printPlan(cfg commandConfig, plan reassignmentPlan) {
	fmt.Printf("Database: %s\n", cfg.DBPath)
	fmt.Printf("Persona: %s (%s)\n", cfg.PersonKey, chooseName(plan.Person.DisplayName, cfg.DisplayName))
	fmt.Printf("Persona da creare: %t\n", plan.CreatePerson)
	fmt.Printf("Persona da riattivare: %t\n", !plan.CreatePerson && !plan.Person.IsActive && cfg.ReactivatePerson)
	fmt.Printf("ID Anviz destinazione: %d (%s)\n", cfg.AnvizEmployeeID, strings.TrimSpace(plan.EmployeeName))
	fmt.Printf("Seriale badge: %d\n", cfg.CardSerial)
	fmt.Printf("Decorrenza: %s\n", cfg.EffectiveAt.Format(time.RFC3339))
	fmt.Printf("Motivo: %s\n", cfg.Reason)
	fmt.Printf("Assegnazioni da chiudere: %d\n", len(plan.CloseAssignments))
	for _, row := range plan.CloseAssignments {
		fmt.Printf("  CHIUDI id=%d persona=%s anviz=%d seriale=%d dal=%s al=%s\n",
			row.ID, row.PersonKey, row.AnvizEmployeeID, row.CardSerial,
			formatUnix(row.ValidFromUTC), cfg.EffectiveAt.Format(time.RFC3339))
	}
	if plan.KeepAssignment != nil {
		fmt.Printf("Assegnazione destinazione gia attiva: id=%d\n", plan.KeepAssignment.ID)
	} else {
		fmt.Println("Nuova assegnazione da creare: 1")
	}
	fmt.Printf("Conflitti futuri bloccanti: %d\n", len(plan.ConflictingFuture))
	for _, row := range plan.ConflictingFuture {
		fmt.Printf("  BLOCCO id=%d persona=%s anviz=%d seriale=%d dal=%s\n",
			row.ID, row.PersonKey, row.AnvizEmployeeID, row.CardSerial, formatUnix(row.ValidFromUTC))
	}
	fmt.Printf("Records destinazione dalla decorrenza: %d\n", plan.AffectedRecords)
	fmt.Printf("Raw destinazione dalla decorrenza: %d\n", plan.AffectedRawRecords)
}

func chooseName(existing, requested string) string {
	if strings.TrimSpace(existing) != "" {
		return strings.TrimSpace(existing)
	}
	return strings.TrimSpace(requested)
}

func formatUnix(value int64) string {
	return time.Unix(value, 0).UTC().Format(time.RFC3339)
}

type applyResult struct {
	OperationID       string
	PersonCreated     bool
	PersonReactivated bool
	ClosedCount       int
	AssignmentCreated bool
}

func applyReassignment(cfg commandConfig) (applyResult, error) {
	var result applyResult
	db, err := openDatabase(cfg.DBPath, false)
	if err != nil {
		return result, err
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		return result, err
	}

	tx, err := db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(badgeAdminSchemaSQL); err != nil {
		return result, err
	}
	now := time.Now().Unix()
	if _, err := tx.Exec(`
		INSERT INTO schema_migrations (version, name, applied_at_utc)
		VALUES (?, 'badge_reassignment_audit', ?)
		ON CONFLICT(version) DO NOTHING
	`, badgeAdminSchemaVersion, now); err != nil {
		return result, err
	}

	person, found, err := findPerson(tx, cfg.PersonKey)
	if err != nil {
		return result, err
	}
	if !found {
		if !cfg.CreatePerson {
			return result, fmt.Errorf("persona %q non trovata durante apply", cfg.PersonKey)
		}
		insert, err := tx.Exec(`
			INSERT INTO people (
				person_key, display_name, is_active, inactive_at_utc,
				created_at_utc, updated_at_utc
			) VALUES (?, ?, 1, NULL, ?, ?)
		`, cfg.PersonKey, cfg.DisplayName, now, now)
		if err != nil {
			return result, err
		}
		person.ID, err = insert.LastInsertId()
		if err != nil {
			return result, err
		}
		person.PersonKey = cfg.PersonKey
		person.DisplayName = cfg.DisplayName
		person.IsActive = true
		result.PersonCreated = true
	} else if !person.IsActive && cfg.ReactivatePerson {
		update, err := tx.Exec(`
			UPDATE people
			SET is_active = 1, inactive_at_utc = NULL, updated_at_utc = ?
			WHERE id = ? AND is_active = 0
		`, now, person.ID)
		if err != nil {
			return result, err
		}
		affected, err := update.RowsAffected()
		if err != nil || affected != 1 {
			return result, fmt.Errorf("riattivazione persona %q non applicata", cfg.PersonKey)
		}
		person.IsActive = true
		result.PersonReactivated = true
	}

	recheckCfg := cfg
	recheckCfg.CreatePerson = false
	plan, err := buildPlan(tx, recheckCfg)
	if err != nil {
		return result, err
	}
	if len(plan.ConflictingFuture) > 0 {
		return result, errors.New("conflitto rilevato nuovamente dentro la transazione")
	}

	result.OperationID = fmt.Sprintf("badge-%d-%d", now, time.Now().UnixNano()%1000000)
	for _, row := range plan.CloseAssignments {
		update, err := tx.Exec(`
			UPDATE person_badge_history
			SET valid_to_utc = ?
			WHERE id = ?
			  AND voided_at_utc IS NULL
			  AND valid_from_utc < ?
			  AND (valid_to_utc IS NULL OR valid_to_utc > ?)
		`, cfg.EffectiveAt.Unix(), row.ID, cfg.EffectiveAt.Unix(), cfg.EffectiveAt.Unix())
		if err != nil {
			return result, err
		}
		affected, err := update.RowsAffected()
		if err != nil || affected != 1 {
			return result, fmt.Errorf("chiusura assegnazione %d non applicata in modo univoco", row.ID)
		}
		if err := insertAudit(tx, result.OperationID, "close", &row.ID, row.PersonID, row.PersonKey,
			row.AnvizEmployeeID, row.CardSerial, row.ValidToUTC, int64Pointer(cfg.EffectiveAt.Unix()), cfg, now); err != nil {
			return result, err
		}
		result.ClosedCount++
	}

	if plan.KeepAssignment == nil {
		insert, err := tx.Exec(`
			INSERT INTO person_badge_history (
				person_id, anviz_employee_id, card_serial,
				valid_from_utc, valid_to_utc, boundary_quality, reason,
				created_by, created_at_utc, voided_at_utc, void_reason
			) VALUES (?, ?, ?, ?, NULL, 'exact', ?, ?, ?, NULL, NULL)
		`, person.ID, cfg.AnvizEmployeeID, cfg.CardSerial, cfg.EffectiveAt.Unix(), cfg.Reason, cfg.Actor, now)
		if err != nil {
			return result, err
		}
		assignmentID, err := insert.LastInsertId()
		if err != nil {
			return result, err
		}
		if err := insertAudit(tx, result.OperationID, "create", &assignmentID, person.ID, person.PersonKey,
			cfg.AnvizEmployeeID, cfg.CardSerial, nil, nil, cfg, now); err != nil {
			return result, err
		}
		result.AssignmentCreated = true
	}

	if result.PersonCreated {
		if err := insertAudit(tx, result.OperationID, "person_create", nil, person.ID, person.PersonKey,
			cfg.AnvizEmployeeID, cfg.CardSerial, nil, nil, cfg, now); err != nil {
			return result, err
		}
	}
	if result.PersonReactivated {
		if err := insertAudit(tx, result.OperationID, "person_reactivate", nil, person.ID, person.PersonKey,
			cfg.AnvizEmployeeID, cfg.CardSerial, nil, nil, cfg, now); err != nil {
			return result, err
		}
	}

	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func insertAudit(tx *sql.Tx, operationID, action string, assignmentID *int64, personID int64, personKey string,
	employeeID int, cardSerial int64, previousValidTo, newValidTo *int64, cfg commandConfig, createdAt int64) error {
	_, err := tx.Exec(`
		INSERT INTO badge_assignment_audit (
			operation_id, action, assignment_id, person_id, person_key,
			anviz_employee_id, card_serial, previous_valid_to_utc, new_valid_to_utc,
			effective_at_utc, actor, reason, created_at_utc
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, operationID, action, nullableInt64(assignmentID), personID, personKey,
		employeeID, cardSerial, nullableInt64(previousValidTo), nullableInt64(newValidTo),
		cfg.EffectiveAt.Unix(), cfg.Actor, cfg.Reason, createdAt)
	return err
}

func nullableInt64(value *int64) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

func int64Pointer(value int64) *int64 {
	return &value
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

func verifyDatabase(db *sql.DB) error {
	var integrity string
	if err := db.QueryRow(`PRAGMA quick_check`).Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("quick_check: %s", integrity)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	if rows.Next() {
		rows.Close()
		return errors.New("foreign_key_check ha rilevato errori")
	}
	rows.Close()

	var version int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return fmt.Errorf("schema_migrations non disponibile: %w", err)
	}
	if version < badgeHistorySchemaVersion {
		return fmt.Errorf("schema badge versione %d richiesto, trovato %d", badgeHistorySchemaVersion, version)
	}
	return nil
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
	escaped := strings.ReplaceAll(filepath.ToSlash(absoluteBackup), "'", "''")
	_, vacuumErr := db.Exec("VACUUM INTO '" + escaped + "'")
	db.Close()
	if vacuumErr != nil {
		return "", vacuumErr
	}

	backupDB, err := openDatabase(absoluteBackup, true)
	if err != nil {
		return "", err
	}
	defer backupDB.Close()
	if err := verifyQuickCheck(backupDB); err != nil {
		return "", err
	}
	return absoluteBackup, nil
}

func verifyQuickCheck(db *sql.DB) error {
	var integrity string
	if err := db.QueryRow(`PRAGMA quick_check`).Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("backup non integro: %s", integrity)
	}
	return nil
}

func recordCounts(dbPath string) (int64, int64, error) {
	db, err := openDatabase(dbPath, true)
	if err != nil {
		return 0, 0, err
	}
	defer db.Close()
	var records, raw int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM records`).Scan(&records); err != nil {
		return 0, 0, err
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM device_raw_records`).Scan(&raw); err != nil {
		return 0, 0, err
	}
	return records, raw, nil
}

func verifyAfterApply(dbPath string, expectedRecords, expectedRaw int64) error {
	db, err := openDatabase(dbPath, true)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := verifyDatabase(db); err != nil {
		return err
	}
	var records, raw int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM records`).Scan(&records); err != nil {
		return err
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM device_raw_records`).Scan(&raw); err != nil {
		return err
	}
	if records != expectedRecords || raw != expectedRaw {
		return fmt.Errorf("conteggi record modificati: records %d->%d raw %d->%d", expectedRecords, records, expectedRaw, raw)
	}
	return nil
}

const badgeAdminSchemaSQL = `
CREATE TABLE IF NOT EXISTS badge_assignment_audit (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    operation_id TEXT NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('close', 'create', 'person_create', 'person_reactivate')),
    assignment_id INTEGER,
    person_id INTEGER NOT NULL,
    person_key TEXT NOT NULL,
    anviz_employee_id INTEGER NOT NULL,
    card_serial INTEGER NOT NULL,
    previous_valid_to_utc INTEGER,
    new_valid_to_utc INTEGER,
    effective_at_utc INTEGER NOT NULL,
    actor TEXT NOT NULL,
    reason TEXT NOT NULL,
    created_at_utc INTEGER NOT NULL,
    FOREIGN KEY (person_id) REFERENCES people(id) ON DELETE RESTRICT,
    FOREIGN KEY (assignment_id) REFERENCES person_badge_history(id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_badge_assignment_audit_operation
ON badge_assignment_audit (operation_id, id);

CREATE INDEX IF NOT EXISTS idx_badge_assignment_audit_person
ON badge_assignment_audit (person_id, effective_at_utc);
`
