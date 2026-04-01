package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite" // Pure Go SQLite driver, no CGO or Windows DLL needed!
)

type Record struct {
	ID           int       `json:"id"`
	EmployeeID   int       `json:"employee_id"`
	EmployeeName string    `json:"employee_name"`
	Timestamp    time.Time `json:"timestamp"`
	Action       string    `json:"action"`      // "In", "Out", "I_pausa", "F_pausa", "U_trasf", "R_trasf", "I_break", "F_break"
	StatusCode   int       `json:"status_code"` // Raw Anviz attendance state 0-7
	Source       string    `json:"source"`      // "web" or "device"
	DeviceID     *int      `json:"device_id,omitempty"`
	RawDeviceTS  *int64    `json:"raw_device_timestamp,omitempty"`
	Latitude     *float64  `json:"latitude"`
	Longitude    *float64  `json:"longitude"`
}

type Employee struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	PIN     string `json:"-"` // Non esportare il PIN nel JSON per sicurezza
	IsAdmin bool   `json:"is_admin"`
}

type SystemAdmin struct {
	ID           int    `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	CreatedAt    string `json:"created_at"`
}

type SystemAdminBootstrapStatus struct {
	FixedUsername               string `json:"fixed_username"`
	SystemAdminCount            int    `json:"system_admin_count"`
	FixedAdminExists            bool   `json:"fixed_admin_exists"`
	BootstrapPasswordConfigured bool   `json:"bootstrap_password_configured"`
	BootstrapCanInitialize      bool   `json:"bootstrap_can_initialize"`
	Ready                       bool   `json:"ready"`
	Status                      string `json:"status"`
	Message                     string `json:"message"`
	RecoveryCommand             string `json:"recovery_command"`
}

// PendingValidation rappresenta una marcatura web in attesa di approvazione admin
type PendingValidation struct {
	ID           int        `json:"id"`
	EmployeeID   int        `json:"employee_id"`
	EmployeeName string     `json:"employee_name"`
	Timestamp    time.Time  `json:"timestamp"`
	Action       string     `json:"action"`
	StatusCode   int        `json:"status_code"`
	Latitude     *float64   `json:"latitude"`
	Longitude    *float64   `json:"longitude"`
	Status       string     `json:"status"` // "pending", "approved", "rejected"
	CreatedAt    time.Time  `json:"created_at"`
	ReviewedBy   *int       `json:"reviewed_by"`
	ReviewedAt   *time.Time `json:"reviewed_at"`
}

type DeviceAggregate struct {
	DeviceID             int        `json:"device_id"`
	RawCount             int        `json:"raw_count"`
	FinalCount           int        `json:"final_count"`
	LatestRawTimestamp   *time.Time `json:"latest_raw_timestamp,omitempty"`
	LatestFinalTimestamp *time.Time `json:"latest_final_timestamp,omitempty"`
}

type DeviceRawDiagnostic struct {
	DeviceID           int       `json:"device_id"`
	EmployeeID         int       `json:"employee_id"`
	EmployeeName       string    `json:"employee_name"`
	RawDeviceTimestamp int64     `json:"raw_device_timestamp"`
	ParsedTimestamp    time.Time `json:"parsed_timestamp"`
	Action             string    `json:"action"`
	StatusCode         int       `json:"status_code"`
	ImportedAt         time.Time `json:"imported_at"`
	Origin             string    `json:"origin"`
}

var DB *sql.DB

var ErrDuplicateRecord = errors.New("record duplicato")

func resolveDBPath() string {
	if envPath := os.Getenv("DB_PATH"); envPath != "" {
		return envPath
	}

	preferredPath := "data/attendance.db"
	if _, err := os.Stat(preferredPath); err == nil {
		return preferredPath
	}

	return "attendance.db"
}

// InitDB apre la connessione ed esegue l'inizializzazione della tabella
func InitDB() {
	var err error
	dbPath := resolveDBPath()
	log.Printf("Database SQLite in uso: %s", dbPath)
	// modernc.org/sqlite DSN supporta i parametri pragma
	DB, err = sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		log.Fatalf("Impossibile aprire il database SQLite: %v", err)
	}

	// Abilita Write-Ahead Logging per migliorare la concorrenza
	_, err = DB.Exec("PRAGMA journal_mode=WAL;")
	if err != nil {
		log.Printf("Attenzione: impossibile impostare WAL mode: %v", err)
	}

	// Tabella locale sqlite per centralizzare i dati del marcatempo
	createTableQuery := `
	CREATE TABLE IF NOT EXISTS records (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		employee_id INTEGER NOT NULL,
		employee_name TEXT NOT NULL DEFAULT '',
		timestamp DATETIME NOT NULL,
		action TEXT NOT NULL,
		status_code INTEGER NOT NULL DEFAULT 0,
		source TEXT NOT NULL,
		device_id INTEGER,
		raw_device_timestamp INTEGER,
		latitude REAL,
		longitude REAL
	);
	CREATE TABLE IF NOT EXISTS employees (
		id INTEGER PRIMARY KEY,
		pin TEXT,
		name TEXT,
		is_admin INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS pending_validations (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		employee_id INTEGER NOT NULL,
		employee_name TEXT NOT NULL DEFAULT '',
		timestamp DATETIME NOT NULL,
		action TEXT NOT NULL,
		status_code INTEGER NOT NULL DEFAULT 0,
		latitude REAL,
		longitude REAL,
		status TEXT NOT NULL DEFAULT 'pending',
		created_at DATETIME NOT NULL,
		reviewed_by INTEGER,
		reviewed_at DATETIME
	);
	CREATE TABLE IF NOT EXISTS device_raw_records (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		device_id INTEGER NOT NULL,
		employee_id INTEGER NOT NULL,
		employee_name TEXT NOT NULL DEFAULT '',
		raw_device_timestamp INTEGER NOT NULL,
		parsed_timestamp DATETIME NOT NULL,
		action TEXT NOT NULL,
		status_code INTEGER NOT NULL DEFAULT 0,
		imported_at DATETIME NOT NULL
	);
	CREATE TABLE IF NOT EXISTS custom_holidays (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		date TEXT UNIQUE NOT NULL,
		description TEXT
	);
	CREATE TABLE IF NOT EXISTS system_admins (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL,
		created_at DATETIME NOT NULL
	);
	`
	_, err = DB.Exec(createTableQuery)
	if err != nil {
		log.Fatalf("Impossibile creare le tabelle: %v", err)
	}

	// Migrazione: aggiunge la colonna status_code se non esiste già
	_, _ = DB.Exec("ALTER TABLE records ADD COLUMN status_code INTEGER NOT NULL DEFAULT 0")

	// Migrazione: aggiunge colonna employee_name per lo storico
	_, _ = DB.Exec("ALTER TABLE records ADD COLUMN employee_name TEXT NOT NULL DEFAULT ''")

	// Migrazione: aggiunge colonna is_admin agli impiegati se non esiste
	_, _ = DB.Exec("ALTER TABLE employees ADD COLUMN is_admin INTEGER NOT NULL DEFAULT 0")

	// Migrazione: aggiunge metadati device per deduplica robusta lato hardware
	_, _ = DB.Exec("ALTER TABLE records ADD COLUMN device_id INTEGER")
	_, _ = DB.Exec("ALTER TABLE records ADD COLUMN raw_device_timestamp INTEGER")

	// Helper for testing: se non c'è nessun admin, imposta un admin (potrai gestire manualmente)
	// (Decommentare per configurare automaticamente un admin di test sulla base dell'ID)
	// _, _ = DB.Exec("UPDATE employees SET is_admin = 1 WHERE id = 15")

	// Migrazione: rimuove il vecchio vincolo globale e separa i controlli duplicati
	// tra flusso device e flusso web/manuale.
	_, _ = DB.Exec("DROP INDEX IF EXISTS idx_emp_time")
	_, _ = DB.Exec("DROP INDEX IF EXISTS idx_records_device_unique")
	_, _ = DB.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_records_device_unique ON records(device_id, employee_id, raw_device_timestamp, status_code) WHERE source = 'device' AND device_id IS NOT NULL AND raw_device_timestamp IS NOT NULL")
	_, _ = DB.Exec("CREATE INDEX IF NOT EXISTS idx_records_device_legacy_lookup ON records(employee_id, timestamp, status_code) WHERE source = 'device' AND device_id IS NULL")
	_, _ = DB.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_records_web_unique ON records(employee_id, timestamp, action) WHERE source IN ('web', 'manual_web')")
	_, _ = DB.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_device_raw_unique ON device_raw_records(device_id, employee_id, raw_device_timestamp, status_code)")

	migrateLegacyDeviceTimestamps()
	backfillDeviceRawRecords()

	log.Println("Database SQLite inizializzato con successo, tabelle verificata.")

	ensureDefaultSystemAdmin()
}

func ensureDefaultSystemAdmin() {
	username := "admin"
	password := os.Getenv("DEFAULT_SYSTEM_ADMIN_PASSWORD")

	var totalAdmins int
	err := DB.QueryRow("SELECT COUNT(*) FROM system_admins").Scan(&totalAdmins)
	if err != nil {
		log.Printf("[WARN] Impossibile verificare la presenza di system admin: %v", err)
		return
	}

	// Niente credenziali hardcoded in codice.
	// Username system admin fisso a "admin".
	// Se non è stata fornita la password bootstrap via env, lasciamo intatto il DB.
	if strings.TrimSpace(password) == "" {
		if totalAdmins == 0 {
			log.Printf("[WARN] Nessun system admin configurato. Imposta DEFAULT_SYSTEM_ADMIN_PASSWORD oppure usa ./make_system_admin admin <password>.")
		}
		return
	}

	var userExists int
	err = DB.QueryRow("SELECT COUNT(*) FROM system_admins WHERE lower(username) = lower(?)", username).Scan(&userExists)
	if err != nil {
		log.Printf("[WARN] Impossibile verificare il super admin bootstrap %s: %v", username, err)
		return
	}
	if userExists > 0 {
		log.Printf("[INFO] System admin bootstrap gia presente per username=%s; nessuna sovrascrittura automatica eseguita", username)
		return
	}

	log.Printf("[INFO] Inizializzazione System Admin bootstrap: %s", username)
	if err := AddSystemAdmin(username, password); err != nil {
		log.Printf("[WARN] Impossibile creare il system admin bootstrap %s: %v", username, err)
	}
}

func GetSystemAdminBootstrapStatus() (SystemAdminBootstrapStatus, error) {
	status := SystemAdminBootstrapStatus{
		FixedUsername:   "admin",
		RecoveryCommand: "./make_system_admin admin <nuova_password>",
	}

	bootstrapPassword := strings.TrimSpace(os.Getenv("DEFAULT_SYSTEM_ADMIN_PASSWORD"))
	status.BootstrapPasswordConfigured = bootstrapPassword != ""

	if err := DB.QueryRow("SELECT COUNT(*) FROM system_admins").Scan(&status.SystemAdminCount); err != nil {
		return status, err
	}

	var fixedAdminCount int
	if err := DB.QueryRow("SELECT COUNT(*) FROM system_admins WHERE lower(username) = lower(?)", status.FixedUsername).Scan(&fixedAdminCount); err != nil {
		return status, err
	}

	status.FixedAdminExists = fixedAdminCount > 0
	status.BootstrapCanInitialize = !status.FixedAdminExists && status.BootstrapPasswordConfigured
	status.Ready = status.FixedAdminExists

	switch {
	case status.FixedAdminExists:
		status.Status = "ready"
		status.Message = "System admin configurato. In caso di recovery usa il comando make_system_admin."
	case status.BootstrapCanInitialize:
		status.Status = "bootstrap_available"
		status.Message = "Password bootstrap presente ma system admin non ancora creato. Il bootstrap avviene al primo avvio su DB vuoto."
	case status.SystemAdminCount == 0 && !status.BootstrapPasswordConfigured:
		status.Status = "missing_bootstrap"
		status.Message = "Nessun system admin configurato e nessuna password bootstrap presente. Imposta DEFAULT_SYSTEM_ADMIN_PASSWORD oppure usa make_system_admin."
	default:
		status.Status = "incomplete"
		status.Message = "Lo stato del system admin non e pronto. Verifica bootstrap e recovery."
	}

	return status, nil
}

func isUniqueConstraintError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

type sqlExecer interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
}

func anvizEpochLocation() *time.Location {
	localTZ, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		return time.Local
	}
	return localTZ
}

func anvizRawTimestampToTime(raw uint32) time.Time {
	loc := anvizEpochLocation()
	baseUTC := time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC)
	days := int(raw / 86400)
	secondsOfDay := int(raw % 86400)
	dateUTC := baseUTC.AddDate(0, 0, days)
	hour := secondsOfDay / 3600
	minute := (secondsOfDay % 3600) / 60
	second := secondsOfDay % 60
	return time.Date(dateUTC.Year(), dateUTC.Month(), dateUTC.Day(), hour, minute, second, 0, loc)
}

func rawDeviceTimestampFromTime(timestamp time.Time) (uint32, error) {
	localTS := timestamp.In(anvizEpochLocation())
	baseUTC := time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC)
	targetUTC := time.Date(localTS.Year(), localTS.Month(), localTS.Day(), 0, 0, 0, 0, time.UTC)
	days := targetUTC.Sub(baseUTC) / (24 * time.Hour)
	if days < 0 {
		return 0, fmt.Errorf("timestamp fuori range per protocollo Anviz")
	}
	seconds := days*86400 + time.Duration(localTS.Hour()*3600+localTS.Minute()*60+localTS.Second())
	if seconds < 0 || uint64(seconds) > uint64(^uint32(0)) {
		return 0, fmt.Errorf("timestamp fuori range per protocollo Anviz")
	}
	return uint32(seconds), nil
}

func migrateLegacyDeviceTimestamps() {
	rows, err := DB.Query(`SELECT id, timestamp FROM records WHERE source = 'device' AND raw_device_timestamp IS NULL`)
	if err != nil {
		log.Printf("Migrazione device legacy: query fallita: %v", err)
		return
	}
	defer rows.Close()

	updated := 0
	skipped := 0

	for rows.Next() {
		var id int
		var timestampStr string
		if err := rows.Scan(&id, &timestampStr); err != nil {
			skipped++
			continue
		}

		timestamp, err := time.Parse(time.RFC3339, timestampStr)
		if err != nil {
			skipped++
			continue
		}

		rawTS, err := rawDeviceTimestampFromTime(timestamp)
		if err != nil {
			skipped++
			continue
		}

		res, err := DB.Exec(`UPDATE records SET raw_device_timestamp = ? WHERE id = ? AND raw_device_timestamp IS NULL`, int64(rawTS), id)
		if err != nil {
			skipped++
			continue
		}

		rowsAffected, err := res.RowsAffected()
		if err == nil && rowsAffected > 0 {
			updated++
		}
	}

	if err := rows.Err(); err != nil {
		log.Printf("Migrazione device legacy: iterazione fallita: %v", err)
	}

	log.Printf("Migrazione device legacy: raw timestamp aggiornati=%d, saltati=%d", updated, skipped)
}

func adoptLegacyDeviceRecord(deviceID uint32, employeeID int, timestamp time.Time, rawDeviceTimestamp uint32, statusCode int) (bool, error) {
	return adoptLegacyDeviceRecordUsing(DB, deviceID, employeeID, timestamp, rawDeviceTimestamp, statusCode)
}

func adoptLegacyDeviceRecordUsing(execer sqlExecer, deviceID uint32, employeeID int, timestamp time.Time, rawDeviceTimestamp uint32, statusCode int) (bool, error) {
	res, err := execer.Exec(`
		UPDATE records
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
		)
	`, int64(deviceID), int64(rawDeviceTimestamp), employeeID, timestamp.Format(time.RFC3339), statusCode)
	if err != nil {
		return false, err
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}

	return rowsAffected > 0, nil
}

func insertDeviceRawRecordUsing(execer sqlExecer, deviceID uint32, employeeID int, employeeName string, timestamp time.Time, rawDeviceTimestamp uint32, action string, statusCode int) (bool, error) {
	res, err := execer.Exec(
		`INSERT INTO device_raw_records (device_id, employee_id, employee_name, raw_device_timestamp, parsed_timestamp, action, status_code, imported_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
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
			log.Printf("DEDUPE device_raw duplicate device=%d employee=%d raw_ts=%d action=%s status=%d timestamp=%s", deviceID, employeeID, rawDeviceTimestamp, action, statusCode, timestamp.Format(time.RFC3339))
			return false, ErrDuplicateRecord
		}
		log.Printf("DEDUPE device_raw insert error device=%d employee=%d raw_ts=%d action=%s status=%d timestamp=%s err=%v", deviceID, employeeID, rawDeviceTimestamp, action, statusCode, timestamp.Format(time.RFC3339), err)
		return false, err
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}

	if rowsAffected == 0 {
		log.Printf("DEDUPE device_raw duplicate(no rows) device=%d employee=%d raw_ts=%d action=%s status=%d timestamp=%s", deviceID, employeeID, rawDeviceTimestamp, action, statusCode, timestamp.Format(time.RFC3339))
		return false, ErrDuplicateRecord
	}

	log.Printf("DEDUPE device_raw inserted device=%d employee=%d raw_ts=%d action=%s status=%d timestamp=%s", deviceID, employeeID, rawDeviceTimestamp, action, statusCode, timestamp.Format(time.RFC3339))

	return true, nil
}

func backfillDeviceRawRecords() {
	rows, err := DB.Query(`
		SELECT device_id, employee_id, employee_name, raw_device_timestamp, timestamp, action, status_code
		FROM records
		WHERE source = 'device' AND device_id IS NOT NULL AND raw_device_timestamp IS NOT NULL
	`)
	if err != nil {
		log.Printf("Migrazione raw device: query fallita: %v", err)
		return
	}
	defer rows.Close()

	inserted := 0
	duplicates := 0
	skipped := 0

	for rows.Next() {
		var deviceID int64
		var employeeID int
		var employeeName string
		var rawDeviceTimestamp int64
		var timestampStr string
		var action string
		var statusCode int

		if err := rows.Scan(&deviceID, &employeeID, &employeeName, &rawDeviceTimestamp, &timestampStr, &action, &statusCode); err != nil {
			skipped++
			continue
		}

		timestamp, err := time.Parse(time.RFC3339, timestampStr)
		if err != nil {
			skipped++
			continue
		}

		ok, err := insertDeviceRawRecordUsing(DB, uint32(deviceID), employeeID, employeeName, timestamp, uint32(rawDeviceTimestamp), action, statusCode)
		if err == ErrDuplicateRecord {
			duplicates++
			continue
		}
		if err != nil {
			skipped++
			continue
		}
		if ok {
			inserted++
		}
	}

	if err := rows.Err(); err != nil {
		log.Printf("Migrazione raw device: iterazione fallita: %v", err)
	}

	log.Printf("Migrazione raw device: inseriti=%d duplicati=%d saltati=%d", inserted, duplicates, skipped)
}

func insertRecordWithDeviceMeta(employeeID int, employeeName string, timestamp time.Time, action string, statusCode int, source string, deviceID *uint32, rawDeviceTimestamp *uint32, lat, lon *float64) (bool, error) {
	return insertRecordWithDeviceMetaUsing(DB, employeeID, employeeName, timestamp, action, statusCode, source, deviceID, rawDeviceTimestamp, lat, lon)
}

func insertRecordWithDeviceMetaUsing(execer sqlExecer, employeeID int, employeeName string, timestamp time.Time, action string, statusCode int, source string, deviceID *uint32, rawDeviceTimestamp *uint32, lat, lon *float64) (bool, error) {
	query := `INSERT INTO records (employee_id, employee_name, timestamp, action, status_code, source, device_id, raw_device_timestamp, latitude, longitude) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	var deviceIDValue interface{}
	if deviceID != nil {
		deviceIDValue = int64(*deviceID)
	}

	var rawDeviceTimestampValue interface{}
	if rawDeviceTimestamp != nil {
		rawDeviceTimestampValue = int64(*rawDeviceTimestamp)
	}

	res, err := execer.Exec(query, employeeID, employeeName, timestamp.Format(time.RFC3339), action, statusCode, source, deviceIDValue, rawDeviceTimestampValue, lat, lon)
	if err != nil {
		if isUniqueConstraintError(err) {
			log.Printf("DEDUPE records duplicate source=%s employee=%d timestamp=%s action=%s status=%d device=%v raw_ts=%v", source, employeeID, timestamp.Format(time.RFC3339), action, statusCode, deviceIDValue, rawDeviceTimestampValue)
			return false, ErrDuplicateRecord
		}
		log.Printf("DEDUPE records insert error source=%s employee=%d timestamp=%s action=%s status=%d device=%v raw_ts=%v err=%v", source, employeeID, timestamp.Format(time.RFC3339), action, statusCode, deviceIDValue, rawDeviceTimestampValue, err)
		return false, err
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}

	if rowsAffected == 0 {
		log.Printf("DEDUPE records duplicate(no rows) source=%s employee=%d timestamp=%s action=%s status=%d device=%v raw_ts=%v", source, employeeID, timestamp.Format(time.RFC3339), action, statusCode, deviceIDValue, rawDeviceTimestampValue)
		return false, ErrDuplicateRecord
	}

	log.Printf("DEDUPE records inserted source=%s employee=%d timestamp=%s action=%s status=%d device=%v raw_ts=%v", source, employeeID, timestamp.Format(time.RFC3339), action, statusCode, deviceIDValue, rawDeviceTimestampValue)

	return true, nil
}

// InsertRecord salva un dato di presenza e applica regole distinte di deduplica
// per record device e per record inseriti via web/manuale.
func InsertRecord(employeeID int, employeeName string, timestamp time.Time, action string, statusCode int, source string, lat, lon *float64) (bool, error) {
	return insertRecordWithDeviceMeta(employeeID, employeeName, timestamp, action, statusCode, source, nil, nil, lat, lon)
}

// InsertDeviceRecord salva una timbratura hardware includendo identificativo terminale
// e timestamp raw del protocollo Anviz per la deduplica lato device.
func InsertDeviceRecord(deviceID uint32, employeeID int, employeeName string, timestamp time.Time, rawDeviceTimestamp uint32, action string, statusCode int) (bool, error) {
	log.Printf("DEDUPE pipeline start device=%d employee=%d timestamp=%s raw_ts=%d action=%s status=%d", deviceID, employeeID, timestamp.Format(time.RFC3339), rawDeviceTimestamp, action, statusCode)

	tx, err := DB.Begin()
	if err != nil {
		log.Printf("DEDUPE pipeline tx begin error device=%d employee=%d raw_ts=%d err=%v", deviceID, employeeID, rawDeviceTimestamp, err)
		return false, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	rawInserted, err := insertDeviceRawRecordUsing(tx, deviceID, employeeID, employeeName, timestamp, rawDeviceTimestamp, action, statusCode)
	if err != nil {
		log.Printf("DEDUPE pipeline raw step failed device=%d employee=%d raw_ts=%d action=%s status=%d err=%v", deviceID, employeeID, rawDeviceTimestamp, action, statusCode, err)
		return false, err
	}

	adopted, err := adoptLegacyDeviceRecordUsing(tx, deviceID, employeeID, timestamp, rawDeviceTimestamp, statusCode)
	if err != nil {
		log.Printf("DEDUPE pipeline legacy adopt error device=%d employee=%d raw_ts=%d status=%d err=%v", deviceID, employeeID, rawDeviceTimestamp, statusCode, err)
		return false, err
	}
	if adopted {
		log.Printf("DEDUPE pipeline legacy adopted device=%d employee=%d raw_ts=%d status=%d timestamp=%s", deviceID, employeeID, rawDeviceTimestamp, statusCode, timestamp.Format(time.RFC3339))
	}
	if !adopted {
		insertedFinal, err := insertRecordWithDeviceMetaUsing(tx, employeeID, employeeName, timestamp, action, statusCode, "device", &deviceID, &rawDeviceTimestamp, nil, nil)
		if err != nil && err != ErrDuplicateRecord {
			log.Printf("DEDUPE pipeline final insert error device=%d employee=%d raw_ts=%d action=%s status=%d err=%v", deviceID, employeeID, rawDeviceTimestamp, action, statusCode, err)
			return false, err
		}
		if err == ErrDuplicateRecord || !insertedFinal {
			log.Printf("DEDUPE pipeline final duplicate device=%d employee=%d raw_ts=%d action=%s status=%d timestamp=%s", deviceID, employeeID, rawDeviceTimestamp, action, statusCode, timestamp.Format(time.RFC3339))
		} else {
			log.Printf("DEDUPE pipeline final inserted device=%d employee=%d raw_ts=%d action=%s status=%d timestamp=%s", deviceID, employeeID, rawDeviceTimestamp, action, statusCode, timestamp.Format(time.RFC3339))
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("DEDUPE pipeline commit error device=%d employee=%d raw_ts=%d action=%s status=%d err=%v", deviceID, employeeID, rawDeviceTimestamp, action, statusCode, err)
		return false, err
	}

	log.Printf("DEDUPE pipeline commit ok device=%d employee=%d raw_ts=%d action=%s status=%d raw_inserted=%v adopted=%v", deviceID, employeeID, rawDeviceTimestamp, action, statusCode, rawInserted, adopted)

	return rawInserted, nil
}

func SyncEmployee(id int, name string, pin string) error {
	name = strings.TrimSpace(name)
	isInvalid := name == "" || strings.HasPrefix(strings.ToLower(name), "nome vuoto")

	if isInvalid {
		// Se il nome fornito dal dispositivo è non valido, aggiorna solo il PIN (se esistente, sennò crea Senza Nome)
		query := `
			INSERT INTO employees (id, name, pin) VALUES (?, 'Senza Nome', ?)
			ON CONFLICT(id) DO UPDATE SET pin=excluded.pin
		`
		_, err := DB.Exec(query, id, pin)
		return err
	}

	// Altrimenti, aggiorna sia nome che PIN
	query := `
		INSERT INTO employees (id, name, pin) VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, pin=excluded.pin
	`
	_, err := DB.Exec(query, id, name, pin)
	return err
}

func VerifyPIN(employeeID int, pin string) bool {
	var dbPin string
	err := DB.QueryRow(`SELECT pin FROM employees WHERE id = ?`, employeeID).Scan(&dbPin)
	if err != nil {
		return false // Dipendente non trovato o senza PIN
	}
	// Permettiamo il login solo se la password esiste e coincide
	return dbPin != "" && dbPin == pin
}

// VerifyAdminPIN controlla se il PIN fornito corrisponde e se l'utente ha privilegi di admin
func VerifyAdminPIN(employeeID int, pin string) bool {
	if employeeID == 0 {
		// Per il super admin (ID 0), verifichiamo se esiste almeno un admin di sistema con questa password
		// Cerchiamo qualsiasi admin per semplicità, o potremmo passare lo username se lo avessimo
		var hash string
		rows, err := DB.Query(`SELECT password_hash FROM system_admins`)
		if err != nil {
			return false
		}
		defer rows.Close()

		h := sha256.New()
		h.Write([]byte(pin))
		expected := hex.EncodeToString(h.Sum(nil))

		for rows.Next() {
			if err := rows.Scan(&hash); err == nil {
				if hash == expected {
					return true
				}
			}
		}
		return false
	}

	var dbPin string
	var isAdmin int
	err := DB.QueryRow(`SELECT pin, is_admin FROM employees WHERE id = ?`, employeeID).Scan(&dbPin, &isAdmin)
	if err != nil {
		return false // Dipendente non trovato
	}
	return dbPin != "" && dbPin == pin && isAdmin == 1
}

// IsEmployeeAdmin verifica se un dipendente ha privilegi di admin
func IsEmployeeAdmin(employeeID int) bool {
	var isAdmin int
	err := DB.QueryRow(`SELECT is_admin FROM employees WHERE id = ?`, employeeID).Scan(&isAdmin)
	if err != nil {
		return false
	}
	return isAdmin == 1
}

// GetEmployeeName recupera il nome del dipendente dato l'ID
func GetEmployeeName(employeeID int) string {
	var name string
	err := DB.QueryRow(`SELECT name FROM employees WHERE id = ?`, employeeID).Scan(&name)
	if err != nil {
		return ""
	}
	return name
}

// --- SYSTEM ADMIN FUNCTIONS ---

// VerifySystemAdmin controlla le credenziali di un amministratore di sistema
func VerifySystemAdmin(username, password string) bool {
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)
	if username == "" || password == "" {
		return false
	}

	var hash string
	// Per ora usiamo un hash sha256.
	err := DB.QueryRow(`SELECT password_hash FROM system_admins WHERE lower(username) = lower(?)`, username).Scan(&hash)
	if err != nil {
		return false
	}

	h := sha256.New()
	h.Write([]byte(password))
	expected := hex.EncodeToString(h.Sum(nil))

	return hash == expected
}

// AddSystemAdmin aggiunge o aggiorna un amministratore di sistema
func AddSystemAdmin(username, password string) error {
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)
	if username == "" || password == "" {
		return fmt.Errorf("username/password non validi")
	}

	h := sha256.New()
	h.Write([]byte(password))
	hash := hex.EncodeToString(h.Sum(nil))

	query := `
		INSERT INTO system_admins (username, password_hash, created_at) VALUES (?, ?, ?)
		ON CONFLICT(username) DO UPDATE SET username=excluded.username, password_hash=excluded.password_hash
	`
	_, err := DB.Exec(query, username, hash, time.Now().Format(time.RFC3339))
	return err
}

// UpdateSystemAdminPassword aggiorna la password di un amministratore esistente
func UpdateSystemAdminPassword(username, newPassword string) error {
	username = strings.TrimSpace(username)
	newPassword = strings.TrimSpace(newPassword)
	if username == "" || newPassword == "" {
		return fmt.Errorf("username/password non validi")
	}

	h := sha256.New()
	h.Write([]byte(newPassword))
	hash := hex.EncodeToString(h.Sum(nil))

	query := `UPDATE system_admins SET password_hash = ? WHERE lower(username) = lower(?)`
	res, err := DB.Exec(query, hash, username)
	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("utente non trovato")
	}
	return nil
}

// GetAllEmployees restituisce la lista di tutti i dipendenti salvati nel DB
func GetAllEmployees() ([]Employee, error) {
	query := `SELECT id, name, is_admin FROM employees ORDER BY name ASC, id ASC`
	rows, err := DB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var employees []Employee
	for rows.Next() {
		var e Employee
		var isAdminInt int
		err := rows.Scan(&e.ID, &e.Name, &isAdminInt)
		if err != nil {
			return nil, err
		}
		e.IsAdmin = isAdminInt == 1
		employees = append(employees, e)
	}
	return employees, nil
}

func parseNullableRFC3339(value sql.NullString) (*time.Time, error) {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil, nil
	}
	timestamp, err := time.Parse(time.RFC3339, value.String)
	if err != nil {
		return nil, err
	}
	return &timestamp, nil
}

func GetDeviceAggregates() ([]DeviceAggregate, error) {
	backfillDeviceRawRecords()

	aggregates := map[int]*DeviceAggregate{}

	rawRows, err := DB.Query(`
		SELECT device_id, COUNT(*), MAX(parsed_timestamp)
		FROM device_raw_records
		GROUP BY device_id
		ORDER BY device_id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rawRows.Close()

	for rawRows.Next() {
		var deviceID int
		var rawCount int
		var latestRaw sql.NullString
		if err := rawRows.Scan(&deviceID, &rawCount, &latestRaw); err != nil {
			return nil, err
		}

		agg := &DeviceAggregate{DeviceID: deviceID, RawCount: rawCount}
		agg.LatestRawTimestamp, err = parseNullableRFC3339(latestRaw)
		if err != nil {
			return nil, err
		}
		aggregates[deviceID] = agg
	}
	if err := rawRows.Err(); err != nil {
		return nil, err
	}

	finalRows, err := DB.Query(`
		SELECT device_id, COUNT(*), MAX(timestamp)
		FROM records
		WHERE source = 'device' AND device_id IS NOT NULL
		GROUP BY device_id
		ORDER BY device_id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer finalRows.Close()

	for finalRows.Next() {
		var deviceID int
		var finalCount int
		var latestFinal sql.NullString
		if err := finalRows.Scan(&deviceID, &finalCount, &latestFinal); err != nil {
			return nil, err
		}

		agg, ok := aggregates[deviceID]
		if !ok {
			agg = &DeviceAggregate{DeviceID: deviceID}
			aggregates[deviceID] = agg
		}
		agg.FinalCount = finalCount
		agg.LatestFinalTimestamp, err = parseNullableRFC3339(latestFinal)
		if err != nil {
			return nil, err
		}
	}
	if err := finalRows.Err(); err != nil {
		return nil, err
	}

	result := make([]DeviceAggregate, 0, len(aggregates))
	for _, aggregate := range aggregates {
		result = append(result, *aggregate)
	}

	return result, nil
}

func GetLegacyUnassignedDeviceRecordCount() (int, error) {
	var count int
	err := DB.QueryRow(`SELECT COUNT(*) FROM records WHERE source = 'device' AND (device_id IS NULL OR raw_device_timestamp IS NULL)`).Scan(&count)
	return count, err
}

func getRecentDeviceRawRecordsFromFinalRecords(limit int) ([]DeviceRawDiagnostic, error) {
	rows, err := DB.Query(`
		SELECT device_id, employee_id, employee_name, raw_device_timestamp, timestamp, action, status_code
		FROM records
		WHERE source = 'device'
		ORDER BY timestamp DESC, id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []DeviceRawDiagnostic
	for rows.Next() {
		var record DeviceRawDiagnostic
		var deviceID sql.NullInt64
		var rawDeviceTS sql.NullInt64
		var timestampStr string
		if err := rows.Scan(&deviceID, &record.EmployeeID, &record.EmployeeName, &rawDeviceTS, &timestampStr, &record.Action, &record.StatusCode); err != nil {
			return nil, err
		}

		if deviceID.Valid {
			record.DeviceID = int(deviceID.Int64)
		}

		parsedTimestamp, err := time.Parse(time.RFC3339, timestampStr)
		if err != nil {
			continue
		}
		record.ParsedTimestamp = parsedTimestamp
		record.ImportedAt = parsedTimestamp

		if rawDeviceTS.Valid {
			record.RawDeviceTimestamp = rawDeviceTS.Int64
		} else if derived, err := rawDeviceTimestampFromTime(parsedTimestamp); err == nil {
			record.RawDeviceTimestamp = int64(derived)
		}
		record.Origin = "legacy_final"

		records = append(records, record)
	}

	return records, rows.Err()
}

func GetRecentLegacyUnassignedDeviceRecords(limit int) ([]DeviceRawDiagnostic, error) {
	if limit <= 0 {
		limit = 25
	}

	rows, err := DB.Query(`
		SELECT employee_id, employee_name, timestamp, action, status_code
		FROM records
		WHERE source = 'device'
		  AND (device_id IS NULL OR raw_device_timestamp IS NULL)
		ORDER BY timestamp DESC, id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []DeviceRawDiagnostic
	for rows.Next() {
		var record DeviceRawDiagnostic
		var timestampStr string
		if err := rows.Scan(&record.EmployeeID, &record.EmployeeName, &timestampStr, &record.Action, &record.StatusCode); err != nil {
			return nil, err
		}

		parsedTimestamp, err := time.Parse(time.RFC3339, timestampStr)
		if err != nil {
			continue
		}

		record.ParsedTimestamp = parsedTimestamp
		record.ImportedAt = parsedTimestamp
		record.DeviceID = 0
		record.Origin = "legacy_final"
		if derived, err := rawDeviceTimestampFromTime(parsedTimestamp); err == nil {
			record.RawDeviceTimestamp = int64(derived)
		}

		records = append(records, record)
	}

	return records, rows.Err()
}

func GetRecentDeviceRawRecords(limit int) ([]DeviceRawDiagnostic, error) {
	if limit <= 0 {
		limit = 25
	}

	backfillDeviceRawRecords()

	rows, err := DB.Query(`
		SELECT device_id, employee_id, employee_name, raw_device_timestamp, parsed_timestamp, action, status_code, imported_at
		FROM device_raw_records
		ORDER BY parsed_timestamp DESC, imported_at DESC, id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []DeviceRawDiagnostic
	for rows.Next() {
		var record DeviceRawDiagnostic
		var parsedTimestampStr string
		var importedAtStr string
		if err := rows.Scan(&record.DeviceID, &record.EmployeeID, &record.EmployeeName, &record.RawDeviceTimestamp, &parsedTimestampStr, &record.Action, &record.StatusCode, &importedAtStr); err != nil {
			return nil, err
		}

		if parsedTimestamp, err := time.Parse(time.RFC3339, parsedTimestampStr); err == nil {
			record.ParsedTimestamp = parsedTimestamp
		}
		if importedAt, err := time.Parse(time.RFC3339, importedAtStr); err == nil {
			record.ImportedAt = importedAt
		}
		record.Origin = "raw"

		records = append(records, record)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(records) == 0 {
		return getRecentDeviceRawRecordsFromFinalRecords(limit)
	}

	return records, nil
}

// scanRecords è un helper interno per unificare la scansione dei record dal dataset SQLite
func scanRecords(rows *sql.Rows) ([]Record, error) {
	var records []Record
	for rows.Next() {
		var r Record
		var timestampStr string
		err := rows.Scan(&r.ID, &r.EmployeeID, &r.EmployeeName, &timestampStr, &r.Action, &r.StatusCode, &r.Source, &r.Latitude, &r.Longitude)
		if err != nil {
			return nil, err
		}

		// Riconvertiamo a time.Time per i client REST
		t, parseErr := time.Parse(time.RFC3339, timestampStr)
		if parseErr == nil {
			r.Timestamp = t
		}

		records = append(records, r)
	}
	return records, nil
}

// GetRecords legge i record SQLite con supporto a range filtri (query API) e filter opzionale per employee
func GetRecords(startDate, endDate, employeeID string) ([]Record, error) {
	query := `SELECT id, employee_id, employee_name, timestamp, action, status_code, source, latitude, longitude FROM records WHERE 1=1`
	var args []interface{}

	if startDate != "" {
		// startDate expected as YYYY-MM-DD
		query += " AND date(timestamp) >= date(?)"
		args = append(args, startDate)
	}
	if endDate != "" && endDate != "null" {
		query += " AND date(timestamp) <= date(?)"
		args = append(args, endDate)
	}
	if employeeID != "" && employeeID != "null" {
		rawIDs := strings.Split(employeeID, ",")
		validIDs := make([]string, 0, len(rawIDs))
		for _, rawID := range rawIDs {
			trimmedID := strings.TrimSpace(rawID)
			if trimmedID != "" {
				validIDs = append(validIDs, trimmedID)
			}
		}

		if len(validIDs) == 1 {
			query += " AND employee_id = ?"
			args = append(args, validIDs[0])
		} else if len(validIDs) > 1 {
			placeholders := make([]string, 0, len(validIDs))
			for _, id := range validIDs {
				placeholders = append(placeholders, "?")
				args = append(args, id)
			}
			query += " AND employee_id IN (" + strings.Join(placeholders, ",") + ")"
		}
	}
	query += " ORDER BY timestamp ASC"

	rows, err := DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanRecords(rows)
}

// GetEmployeeRecords legge solo gli ultimi record di uno specifico dipendente
func GetEmployeeRecords(employeeID int, limit int) ([]Record, error) {
	query := `SELECT id, employee_id, employee_name, timestamp, action, status_code, source, latitude, longitude FROM records WHERE employee_id = ? ORDER BY timestamp DESC LIMIT ?`

	rows, err := DB.Query(query, employeeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanRecords(rows)
}

// GetEmployeeMonthlyRecords restituisce tutti i record di un dipendente per un dato mese
func GetEmployeeMonthlyRecords(employeeID int, year int, month int) ([]Record, error) {
	startDate := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.Local).Format("2006-01-02")
	endDate := time.Date(year, time.Month(month)+1, 0, 23, 59, 59, 0, time.Local).Format("2006-01-02")

	query := `SELECT id, employee_id, employee_name, timestamp, action, status_code, source, latitude, longitude
		FROM records
		WHERE employee_id = ? AND date(timestamp) >= date(?) AND date(timestamp) <= date(?)
		ORDER BY timestamp ASC`

	rows, err := DB.Query(query, employeeID, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanRecords(rows)
}

// GetEmployeeRangeRecords restituisce tutti i record di un dipendente in un range di date
func GetEmployeeRangeRecords(employeeID int, startDate string, endDate string) ([]Record, error) {
	query := `SELECT id, employee_id, employee_name, timestamp, action, status_code, source, latitude, longitude
		FROM records
		WHERE employee_id = ? AND date(timestamp) >= date(?) AND date(timestamp) <= date(?)
		ORDER BY timestamp ASC`

	rows, err := DB.Query(query, employeeID, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanRecords(rows)
}

// InsertPendingValidation crea una nuova richiesta di validazione per marcatura web
func InsertPendingValidation(employeeID int, employeeName string, timestamp time.Time, action string, statusCode int, lat, lon *float64) error {
	query := `INSERT INTO pending_validations (employee_id, employee_name, timestamp, action, status_code, latitude, longitude, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', ?)`
	_, err := DB.Exec(query, employeeID, employeeName, timestamp.Format(time.RFC3339), action, statusCode, lat, lon, time.Now().Format(time.RFC3339))
	return err
}

// GetPendingValidations restituisce le richieste di validazione, filtrate opzionalmente per status
func GetPendingValidations(status string) ([]PendingValidation, error) {
	query := `SELECT id, employee_id, employee_name, timestamp, action, status_code, latitude, longitude, status, created_at, reviewed_by, reviewed_at
		FROM pending_validations`
	var args []interface{}

	if status != "" {
		query += " WHERE status = ?"
		args = append(args, status)
	}
	query += " ORDER BY created_at DESC"

	rows, err := DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var validations []PendingValidation
	for rows.Next() {
		var v PendingValidation
		var timestampStr, createdAtStr string
		var reviewedAtStr sql.NullString
		var reviewedBy sql.NullInt64

		err := rows.Scan(&v.ID, &v.EmployeeID, &v.EmployeeName, &timestampStr, &v.Action, &v.StatusCode,
			&v.Latitude, &v.Longitude, &v.Status, &createdAtStr, &reviewedBy, &reviewedAtStr)
		if err != nil {
			return nil, err
		}

		if t, e := time.Parse(time.RFC3339, timestampStr); e == nil {
			v.Timestamp = t
		}
		if t, e := time.Parse(time.RFC3339, createdAtStr); e == nil {
			v.CreatedAt = t
		}
		if reviewedBy.Valid {
			val := int(reviewedBy.Int64)
			v.ReviewedBy = &val
		}
		if reviewedAtStr.Valid {
			if t, e := time.Parse(time.RFC3339, reviewedAtStr.String); e == nil {
				v.ReviewedAt = &t
			}
		}

		validations = append(validations, v)
	}
	return validations, nil
}

// GetRecordByID recupera un singolo record tramite il suo ID
func GetRecordByID(id int) (Record, error) {
	query := `SELECT id, employee_id, employee_name, timestamp, action, status_code, source, latitude, longitude FROM records WHERE id = ?`
	var r Record
	var timestampStr string
	err := DB.QueryRow(query, id).Scan(&r.ID, &r.EmployeeID, &r.EmployeeName, &timestampStr, &r.Action, &r.StatusCode, &r.Source, &r.Latitude, &r.Longitude)
	if err != nil {
		return r, err
	}

	t, parseErr := time.Parse(time.RFC3339, timestampStr)
	if parseErr == nil {
		r.Timestamp = t
	}
	return r, nil
}

// UpdateManualRecord aggiorna i dati di un record manuale esistente
func UpdateManualRecord(id int, timestamp time.Time, action string, statusCode int) error {
	query := `UPDATE records SET timestamp = ?, action = ?, status_code = ? WHERE id = ? AND source = 'manual_web'`
	res, err := DB.Exec(query, timestamp.Format(time.RFC3339), action, statusCode, id)
	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("nessun record aggiornato (potrebbe non esistere o non essere manual_web)")
	}
	return nil
}

// actionGroup restituisce il gruppo semantico di un'azione per il controllo duplicati.
// Ogni tipo di marcatura ha il proprio gruppo, così "Entrata" non blocca "Inizio Pausa".
func actionGroup(action string) string {
	normalized := strings.TrimSpace(strings.ToLower(action))
	switch {
	case normalized == "in" || normalized == "entrata":
		return "entrata"
	case normalized == "out" || normalized == "uscita":
		return "uscita"
	case normalized == "i_pausa" || normalized == "inizio_pausa" || normalized == "i_break":
		return "inizio_pausa"
	case normalized == "f_pausa" || normalized == "fine_pausa" || normalized == "f_break":
		return "fine_pausa"
	case normalized == "inizio_trasferta" || strings.HasPrefix(normalized, "u_trasf") || strings.HasPrefix(normalized, "u_trasfer") || strings.HasPrefix(normalized, "u_transfer"):
		return "inizio_trasferta"
	case normalized == "ritorno_trasferta" || strings.HasPrefix(normalized, "r_trasf") || strings.HasPrefix(normalized, "r_trasfer") || strings.HasPrefix(normalized, "r_transfer"):
		return "ritorno_trasferta"
	default:
		return normalized
	}
}

// GetRecordHasDeviceEquivalent verifica se per quel dipendente esiste gia una marcatura device
// della stessa tipologia nello stesso minuto. Questo evita duplicati veri ma consente di
// recuperare manualmente marcature mancanti della stessa categoria in orari diversi.
func GetRecordHasDeviceEquivalent(employeeID int, timestamp time.Time, action string) bool {
	targetGroup := actionGroup(action)

	query := `SELECT action
		FROM records
		WHERE employee_id = ?
		  AND source = 'device'
		  AND strftime('%Y-%m-%d %H:%M', timestamp) = strftime('%Y-%m-%d %H:%M', ?)`
	rows, err := DB.Query(query, employeeID, timestamp.Format(time.RFC3339))
	if err != nil {
		return false
	}
	defer rows.Close()

	for rows.Next() {
		var dbAction string
		if err := rows.Scan(&dbAction); err == nil {
			if actionGroup(dbAction) == targetGroup {
				return true
			}
		}
	}
	return false
}

// ApproveValidation approva una richiesta pendente e copia il record nella tabella records
func ApproveValidation(validationID int, adminID int) error {
	// Recupera la validazione pendente
	var v PendingValidation
	var timestampStr string
	err := DB.QueryRow(`SELECT id, employee_id, employee_name, timestamp, action, status_code, latitude, longitude, status
		FROM pending_validations WHERE id = ?`, validationID).Scan(
		&v.ID, &v.EmployeeID, &v.EmployeeName, &timestampStr, &v.Action, &v.StatusCode, &v.Latitude, &v.Longitude, &v.Status)
	if err != nil {
		return fmt.Errorf("validazione non trovata: %w", err)
	}
	if v.Status != "pending" {
		return fmt.Errorf("la validazione è già stata gestita (stato: %s)", v.Status)
	}

	if t, e := time.Parse(time.RFC3339, timestampStr); e == nil {
		v.Timestamp = t
	}

	// Inserisci il record nella tabella records
	inserted, err := InsertRecord(v.EmployeeID, v.EmployeeName, v.Timestamp, v.Action, v.StatusCode, "web", v.Latitude, v.Longitude)
	if errors.Is(err, ErrDuplicateRecord) {
		return fmt.Errorf("esiste gia una marcatura web/manuale con gli stessi dati")
	}
	if err != nil {
		return fmt.Errorf("errore inserimento record approvato: %w", err)
	}
	if !inserted {
		return fmt.Errorf("esiste gia una marcatura web/manuale con gli stessi dati")
	}

	// Aggiorna lo stato della validazione
	now := time.Now().Format(time.RFC3339)
	_, err = DB.Exec(`UPDATE pending_validations SET status = 'approved', reviewed_by = ?, reviewed_at = ? WHERE id = ?`,
		adminID, now, validationID)
	return err
}

// RejectValidation rifiuta una richiesta pendente
func RejectValidation(validationID int, adminID int) error {
	var status string
	err := DB.QueryRow(`SELECT status FROM pending_validations WHERE id = ?`, validationID).Scan(&status)
	if err != nil {
		return fmt.Errorf("validazione non trovata: %w", err)
	}
	if status != "pending" {
		return fmt.Errorf("la validazione è già stata gestita (stato: %s)", status)
	}

	now := time.Now().Format(time.RFC3339)
	_, err = DB.Exec(`UPDATE pending_validations SET status = 'rejected', reviewed_by = ?, reviewed_at = ? WHERE id = ?`,
		adminID, now, validationID)
	return err
}

// CustomHoliday rappresenta una data considerata festiva a livello aziendale
type CustomHoliday struct {
	ID          int    `json:"id"`
	Date        string `json:"date"` // Formato YYYY-MM-DD
	Description string `json:"description"`
}

// GetCustomHolidays restituisce l'elenco di tutte le festività personalizzate
func GetCustomHolidays() ([]CustomHoliday, error) {
	query := `SELECT id, date, description FROM custom_holidays ORDER BY date ASC`
	rows, err := DB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var holidays []CustomHoliday
	for rows.Next() {
		var h CustomHoliday
		var desc sql.NullString
		err := rows.Scan(&h.ID, &h.Date, &desc)
		if err != nil {
			return nil, err
		}
		if desc.Valid {
			h.Description = desc.String
		}
		holidays = append(holidays, h)
	}
	return holidays, nil
}

// AddCustomHoliday aggiunge una nuova festività personalizzata
func AddCustomHoliday(date string, description string) error {
	query := `INSERT INTO custom_holidays (date, description) VALUES (?, ?)`
	_, err := DB.Exec(query, date, description)
	return err
}

// DeleteCustomHoliday elimina una festività personalizzata
func DeleteCustomHoliday(id int) error {
	query := `DELETE FROM custom_holidays WHERE id = ?`
	_, err := DB.Exec(query, id)
	return err
}
