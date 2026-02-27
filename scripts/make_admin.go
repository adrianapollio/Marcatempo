package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Uso: go run make_admin.go <EmployeeID>")
		return
	}
	empID := os.Args[1]

	// Apre il database attendance.db nella cartella corrente (Marcatempo)
	db, err := sql.Open("sqlite", "attendance.db")
	if err != nil {
		log.Fatalf("Impossibile aprire il DB: %v", err)
	}
	defer db.Close()

	// Prova ad aggiungere la colonna nel caso in cui il server non fosse 
	// ancora stato riavviato e la migrazione non fosse partita
	_, _ = db.Exec("ALTER TABLE employees ADD COLUMN is_admin INTEGER NOT NULL DEFAULT 0")

	res, err := db.Exec("UPDATE employees SET is_admin = 1 WHERE id = ?", empID)
	if err != nil {
		log.Fatalf("Errore aggiornamento: %v", err)
	}

	rows, _ := res.RowsAffected()
	if rows > 0 {
		fmt.Printf("Dipendente %s impostato come Amministratore con successo!\n", empID)
	} else {
		fmt.Printf("Nessun dipendente trovato con ID %s\n", empID)
	}
}
