package main

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
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
	Latitude     *float64  `json:"latitude"`
	Longitude    *float64  `json:"longitude"`
}

type Employee struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	PIN     string `json:"-"` // Non esportare il PIN nel JSON per sicurezza
	IsAdmin bool   `json:"is_admin"`
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

var DB *sql.DB

var ErrDuplicatePendingValidation = errors.New("esiste gia una marcatura web recente in attesa o registrata")

const webDuplicateWindow = time.Minute

// InitDB apre la connessione ed esegue l'inizializzazione della tabella
func InitDB() {
	var err error
	DB, err = sql.Open("sqlite", "attendance.db")
	if err != nil {
		log.Fatalf("Impossibile aprire il database SQLite: %v", err)
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
		device_ip TEXT,
		raw_timestamp_secs INTEGER,
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

	// Migrazione: metadati grezzi per la deduplica delle marcature device
	_, _ = DB.Exec("ALTER TABLE records ADD COLUMN device_id INTEGER")
	_, _ = DB.Exec("ALTER TABLE records ADD COLUMN device_ip TEXT")
	_, _ = DB.Exec("ALTER TABLE records ADD COLUMN raw_timestamp_secs INTEGER")

	// Helper for testing: se non c'è nessun admin, imposta un admin (potrai gestire manualmente)
	// (Decommentare per configurare automaticamente un admin di test sulla base dell'ID)
	// _, _ = DB.Exec("UPDATE employees SET is_admin = 1 WHERE id = 15")

	// Rimuove la vecchia unique globale che mischiava device e web.
	_, _ = DB.Exec("DROP INDEX IF EXISTS idx_emp_time")
	_, _ = DB.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_records_device_unique ON records(device_id, employee_id, raw_timestamp_secs, status_code) WHERE source = 'device' AND device_id IS NOT NULL AND raw_timestamp_secs IS NOT NULL")
	_, _ = DB.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_records_web_unique ON records(employee_id, timestamp, action) WHERE source = 'web'")
	_, _ = DB.Exec("CREATE INDEX IF NOT EXISTS idx_records_web_lookup ON records(source, employee_id, action, status_code, timestamp)")
	_, _ = DB.Exec("CREATE INDEX IF NOT EXISTS idx_pending_validations_lookup ON pending_validations(status, employee_id, action, status_code, timestamp)")

	log.Println("Database SQLite inizializzato con successo, tabelle verificata.")
}

func hasLegacyDeviceDuplicate(employeeID int, timestamp time.Time, statusCode int) (bool, error) {
	var exists int
	err := DB.QueryRow(`
		SELECT 1
		FROM records
		WHERE source = 'device' AND raw_timestamp_secs IS NULL AND employee_id = ? AND timestamp = ? AND status_code = ?
		LIMIT 1`, employeeID, timestamp.Format(time.RFC3339), statusCode).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func hasDeviceDuplicate(employeeID int, timestamp time.Time, statusCode int, deviceID uint32, rawTimestampSecs uint32) (bool, error) {
	var exists int
	err := DB.QueryRow(`
		SELECT 1
		FROM records
		WHERE source = 'device' AND (
			(device_id = ? AND employee_id = ? AND raw_timestamp_secs = ? AND status_code = ?)
			OR
			(raw_timestamp_secs IS NULL AND employee_id = ? AND timestamp = ? AND status_code = ?)
		)
		LIMIT 1`,
		int(deviceID), employeeID, int64(rawTimestampSecs), statusCode,
		employeeID, timestamp.Format(time.RFC3339), statusCode,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func hasRecentWebRecord(employeeID int, timestamp time.Time, action string, statusCode int) (bool, error) {
	var exists int
	lowerBound := timestamp.Add(-webDuplicateWindow).Format(time.RFC3339)
	upperBound := timestamp.Add(webDuplicateWindow).Format(time.RFC3339)
	err := DB.QueryRow(`
		SELECT 1
		FROM records
		WHERE source = 'web' AND employee_id = ? AND action = ? AND status_code = ?
			AND datetime(timestamp) BETWEEN datetime(?) AND datetime(?)
		LIMIT 1`, employeeID, action, statusCode, lowerBound, upperBound).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func hasRecentPendingValidation(employeeID int, timestamp time.Time, action string, statusCode int) (bool, error) {
	var exists int
	lowerBound := timestamp.Add(-webDuplicateWindow).Format(time.RFC3339)
	upperBound := timestamp.Add(webDuplicateWindow).Format(time.RFC3339)
	err := DB.QueryRow(`
		SELECT 1
		FROM pending_validations
		WHERE status = 'pending' AND employee_id = ? AND action = ? AND status_code = ?
			AND datetime(timestamp) BETWEEN datetime(?) AND datetime(?)
		LIMIT 1`, employeeID, action, statusCode, lowerBound, upperBound).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// InsertRecord salva un dato di presenza generato dal flusso web.
func InsertRecord(employeeID int, employeeName string, timestamp time.Time, action string, statusCode int, source string, lat, lon *float64) error {
	if source == "device" {
		duplicate, err := hasLegacyDeviceDuplicate(employeeID, timestamp, statusCode)
		if err != nil {
			return err
		}
		if duplicate {
			return nil
		}
	}

	if source == "web" {
		duplicate, err := hasRecentWebRecord(employeeID, timestamp, action, statusCode)
		if err != nil {
			return err
		}
		if duplicate {
			return nil
		}
	}

	query := `INSERT INTO records (employee_id, employee_name, timestamp, action, status_code, source, latitude, longitude) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := DB.Exec(query, employeeID, employeeName, timestamp.Format(time.RFC3339), action, statusCode, source, lat, lon)
	return err
}

func InsertDeviceRecord(employeeID int, employeeName string, timestamp time.Time, action string, statusCode int, deviceID uint32, deviceIP string, rawTimestampSecs uint32) error {
	duplicate, err := hasDeviceDuplicate(employeeID, timestamp, statusCode, deviceID, rawTimestampSecs)
	if err != nil {
		return err
	}
	if duplicate {
		return nil
	}

	query := `INSERT INTO records (employee_id, employee_name, timestamp, action, status_code, source, device_id, device_ip, raw_timestamp_secs, latitude, longitude) VALUES (?, ?, ?, ?, ?, 'device', ?, ?, ?, NULL, NULL)`
	_, err = DB.Exec(query, employeeID, employeeName, timestamp.Format(time.RFC3339), action, statusCode, int(deviceID), deviceIP, int64(rawTimestampSecs))
	return err
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

func businessLocation() *time.Location {
	loc, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		return time.Local
	}
	return loc
}

func buildDayRange(dateStr string) (time.Time, time.Time, error) {
	loc := businessLocation()
	start, err := time.ParseInLocation("2006-01-02", dateStr, loc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return start, start.AddDate(0, 0, 1), nil
}

func buildMonthRange(year int, month int) (time.Time, time.Time) {
	loc := businessLocation()
	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, loc)
	return start, start.AddDate(0, 1, 0)
}

func parseRecordTimestamp(timestampStr string) time.Time {
	timestamp, err := time.Parse(time.RFC3339, timestampStr)
	if err != nil {
		return time.Time{}
	}
	return timestamp
}

func isAdminEmployee(employeeID int) error {
	if employeeID <= 0 {
		return fmt.Errorf("admin non valido")
	}
	if !IsEmployeeAdmin(employeeID) {
		return fmt.Errorf("l'utente %d non ha privilegi admin", employeeID)
	}
	return nil
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

// GetRecords legge i record SQLite con supporto a range filtri (query API) e filter opzionale per employee
func GetRecords(startDate, endDate, employeeID string) ([]Record, error) {
	query := `SELECT id, employee_id, employee_name, timestamp, action, status_code, source, latitude, longitude FROM records WHERE 1=1`
	var args []interface{}

	if startDate != "" {
		startTime, _, err := buildDayRange(startDate)
		if err != nil {
			return nil, err
		}
		query += " AND datetime(timestamp) >= datetime(?)"
		args = append(args, startTime.Format(time.RFC3339))
	}
	if endDate != "" && endDate != "null" {
		_, endExclusive, err := buildDayRange(endDate)
		if err != nil {
			return nil, err
		}
		query += " AND datetime(timestamp) < datetime(?)"
		args = append(args, endExclusive.Format(time.RFC3339))
	}
	if employeeID != "" && employeeID != "null" {
		query += " AND employee_id = ?"
		args = append(args, employeeID)
	}
	query += " ORDER BY timestamp ASC"

	rows, err := DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []Record
	for rows.Next() {
		var r Record
		var timestampStr string
		err := rows.Scan(&r.ID, &r.EmployeeID, &r.EmployeeName, &timestampStr, &r.Action, &r.StatusCode, &r.Source, &r.Latitude, &r.Longitude)
		if err != nil {
			return nil, err
		}

		// Riconvertiamo a time.Time per i client REST
		r.Timestamp = parseRecordTimestamp(timestampStr)

		records = append(records, r)
	}
	return records, nil
}

// GetEmployeeRecords legge solo gli ultimi record di uno specifico dipendente
func GetEmployeeRecords(employeeID int, limit int) ([]Record, error) {
	query := `SELECT id, employee_id, employee_name, timestamp, action, status_code, source, latitude, longitude FROM records WHERE employee_id = ? ORDER BY timestamp DESC LIMIT ?`

	rows, err := DB.Query(query, employeeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []Record
	for rows.Next() {
		var r Record
		var timestampStr string
		err := rows.Scan(&r.ID, &r.EmployeeID, &r.EmployeeName, &timestampStr, &r.Action, &r.StatusCode, &r.Source, &r.Latitude, &r.Longitude)
		if err != nil {
			return nil, err
		}

		r.Timestamp = parseRecordTimestamp(timestampStr)

		records = append(records, r)
	}
	return records, nil
}

// GetEmployeeMonthlyRecords restituisce tutti i record di un dipendente per un dato mese
func GetEmployeeMonthlyRecords(employeeID int, year int, month int) ([]Record, error) {
	startDate, endDate := buildMonthRange(year, month)

	query := `SELECT id, employee_id, employee_name, timestamp, action, status_code, source, latitude, longitude
		FROM records
		WHERE employee_id = ? AND datetime(timestamp) >= datetime(?) AND datetime(timestamp) < datetime(?)
		ORDER BY timestamp ASC`

	rows, err := DB.Query(query, employeeID, startDate.Format(time.RFC3339), endDate.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []Record
	for rows.Next() {
		var r Record
		var timestampStr string
		err := rows.Scan(&r.ID, &r.EmployeeID, &r.EmployeeName, &timestampStr, &r.Action, &r.StatusCode, &r.Source, &r.Latitude, &r.Longitude)
		if err != nil {
			return nil, err
		}

		r.Timestamp = parseRecordTimestamp(timestampStr)

		records = append(records, r)
	}
	return records, nil
}

// GetEmployeeRangeRecords restituisce tutti i record di un dipendente in un range di date
func GetEmployeeRangeRecords(employeeID int, startDate string, endDate string) ([]Record, error) {
	startTime, _, err := buildDayRange(startDate)
	if err != nil {
		return nil, err
	}
	_, endExclusive, err := buildDayRange(endDate)
	if err != nil {
		return nil, err
	}

	query := `SELECT id, employee_id, employee_name, timestamp, action, status_code, source, latitude, longitude
		FROM records
		WHERE employee_id = ? AND datetime(timestamp) >= datetime(?) AND datetime(timestamp) < datetime(?)
		ORDER BY timestamp ASC`

	rows, err := DB.Query(query, employeeID, startTime.Format(time.RFC3339), endExclusive.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []Record
	for rows.Next() {
		var r Record
		var timestampStr string
		err := rows.Scan(&r.ID, &r.EmployeeID, &r.EmployeeName, &timestampStr, &r.Action, &r.StatusCode, &r.Source, &r.Latitude, &r.Longitude)
		if err != nil {
			return nil, err
		}

		r.Timestamp = parseRecordTimestamp(timestampStr)

		records = append(records, r)
	}
	return records, nil
}

// InsertPendingValidation crea una nuova richiesta di validazione per marcatura web
func InsertPendingValidation(employeeID int, employeeName string, timestamp time.Time, action string, statusCode int, lat, lon *float64) error {
	pendingDuplicate, err := hasRecentPendingValidation(employeeID, timestamp, action, statusCode)
	if err != nil {
		return err
	}
	if pendingDuplicate {
		return ErrDuplicatePendingValidation
	}

	recordDuplicate, err := hasRecentWebRecord(employeeID, timestamp, action, statusCode)
	if err != nil {
		return err
	}
	if recordDuplicate {
		return ErrDuplicatePendingValidation
	}

	query := `INSERT INTO pending_validations (employee_id, employee_name, timestamp, action, status_code, latitude, longitude, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', ?)`
	_, err = DB.Exec(query, employeeID, employeeName, timestamp.Format(time.RFC3339), action, statusCode, lat, lon, time.Now().Format(time.RFC3339))
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

// ApproveValidation approva una richiesta pendente e copia il record nella tabella records
func ApproveValidation(validationID int, adminID int) error {
	if err := isAdminEmployee(adminID); err != nil {
		return err
	}

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

	v.Timestamp = parseRecordTimestamp(timestampStr)

	// Inserisci il record nella tabella records
	err = InsertRecord(v.EmployeeID, v.EmployeeName, v.Timestamp, v.Action, v.StatusCode, "web", v.Latitude, v.Longitude)
	if err != nil {
		return fmt.Errorf("errore inserimento record approvato: %w", err)
	}

	// Aggiorna lo stato della validazione
	now := time.Now().Format(time.RFC3339)
	_, err = DB.Exec(`UPDATE pending_validations SET status = 'approved', reviewed_by = ?, reviewed_at = ? WHERE id = ?`,
		adminID, now, validationID)
	return err
}

// RejectValidation rifiuta una richiesta pendente
func RejectValidation(validationID int, adminID int) error {
	if err := isAdminEmployee(adminID); err != nil {
		return err
	}

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
