package main

import (
	"database/sql"
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

	// Helper for testing: se non c'è nessun admin, imposta un admin (potrai gestire manualmente)
	// (Decommentare per configurare automaticamente un admin di test sulla base dell'ID)
	// _, _ = DB.Exec("UPDATE employees SET is_admin = 1 WHERE id = 15")

	// Previene duplicati delle stesse timbrature
	_, _ = DB.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_emp_time ON records(employee_id, timestamp)")

	log.Println("Database SQLite inizializzato con successo, tabelle verificata.")
}

// InsertRecord salva un dato di presenza prelevato dal web app o dal raw TCP tcp
func InsertRecord(employeeID int, employeeName string, timestamp time.Time, action string, statusCode int, source string, lat, lon *float64) error {
	query := `INSERT OR IGNORE INTO records (employee_id, employee_name, timestamp, action, status_code, source, latitude, longitude) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	// Salviamo la data nel formato ISO8601 così è compresa da SQLite in query su intervalli (e.g. date())
	_, err := DB.Exec(query, employeeID, employeeName, timestamp.Format(time.RFC3339), action, statusCode, source, lat, lon)
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
		// startDate expected as YYYY-MM-DD
		query += " AND date(timestamp) >= date(?)"
		args = append(args, startDate)
	}
	if endDate != "" && endDate != "null" {
		query += " AND date(timestamp) <= date(?)"
		args = append(args, endDate)
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
		t, parseErr := time.Parse(time.RFC3339, timestampStr)
		if parseErr == nil {
			r.Timestamp = t
		}

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

		t, parseErr := time.Parse(time.RFC3339, timestampStr)
		if parseErr == nil {
			r.Timestamp = t
		}

		records = append(records, r)
	}
	return records, nil
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

	var records []Record
	for rows.Next() {
		var r Record
		var timestampStr string
		err := rows.Scan(&r.ID, &r.EmployeeID, &r.EmployeeName, &timestampStr, &r.Action, &r.StatusCode, &r.Source, &r.Latitude, &r.Longitude)
		if err != nil {
			return nil, err
		}

		t, parseErr := time.Parse(time.RFC3339, timestampStr)
		if parseErr == nil {
			r.Timestamp = t
		}

		records = append(records, r)
	}
	return records, nil
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

	var records []Record
	for rows.Next() {
		var r Record
		var timestampStr string
		err := rows.Scan(&r.ID, &r.EmployeeID, &r.EmployeeName, &timestampStr, &r.Action, &r.StatusCode, &r.Source, &r.Latitude, &r.Longitude)
		if err != nil {
			return nil, err
		}

		t, parseErr := time.Parse(time.RFC3339, timestampStr)
		if parseErr == nil {
			r.Timestamp = t
		}

		records = append(records, r)
	}
	return records, nil
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
