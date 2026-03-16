package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Println("Uso: go run insert_device_record.go <EmployeeID> <Timestamp> <Action>")
		fmt.Println("Esempio: go run insert_device_record.go 15 \"2026-03-13 08:30:00\" \"In\"")
		fmt.Println("Azioni valide: In, Out, I_pausa, F_pausa, U_trasf, R_trasf, I_break, F_break")
		return
	}

	empID := os.Args[1]
	tsStr := os.Args[2]
	action := os.Args[3]

	// Mappa delle azioni in codici di stato Anviz (coerente con anviz_sync.go)
	statusMap := map[string]int{
		"In":      0,
		"Out":     1,
		"I_pausa": 2,
		"F_pausa": 3,
		"U_trasf": 4,
		"R_trasf": 5,
		"I_break": 6,
		"F_break": 7,
	}

	statusCode, ok := statusMap[action]
	if !ok {
		log.Fatalf("Azione non valida: %s. Azioni supportate: In, Out, I_pausa, F_pausa, U_trasf, R_trasf, I_break, F_break", action)
	}

	// Parsing del timestamp
	ts, err := time.ParseInLocation("2006-01-02 15:04:05", tsStr, time.Local)
	if err != nil {
		log.Fatalf("Formato timestamp non valido: %v. Usa \"YYYY-MM-DD HH:MM:SS\"", err)
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		// Prova diverse posizioni comuni per Marcatempo
		// Priorità a data/ per allineamento con docker-compose
		paths := []string{"data/attendance.db", "attendance.db", "../attendance.db"}
		for _, p := range paths {
			if _, err := os.Stat(p); err == nil {
				dbPath = p
				break
			}
		}
	}
	if dbPath == "" {
		dbPath = "attendance.db" // Fallback
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("Impossibile aprire il DB (%s): %v", dbPath, err)
	}
	defer db.Close()

	// Recupera il nome dell'impiegato
	var empName string
	err = db.QueryRow("SELECT name FROM employees WHERE id = ?", empID).Scan(&empName)
	if err != nil {
		log.Fatalf("Errore nel recupero del dipendente ID %s: %v", empID, err)
	}

	// Inserimento del record con source = 'device'
	query := `INSERT OR IGNORE INTO records (employee_id, employee_name, timestamp, action, status_code, source) VALUES (?, ?, ?, ?, ?, 'device')`
	_, err = db.Exec(query, empID, empName, ts.Format(time.RFC3339), action, statusCode)
	if err != nil {
		log.Fatalf("Errore durante l'inserimento: %v", err)
	}

	fmt.Printf("Marcatura inserita con successo per %s (ID: %s):\n", empName, empID)
	fmt.Printf("  Timestamp: %s\n", ts.Format("2006-01-02 15:04:05"))
	fmt.Printf("  Azione:    %s (Source: device)\n", action)
}
