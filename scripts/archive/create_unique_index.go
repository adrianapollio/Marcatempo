package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "modernc.org/sqlite"
)

func main() {
	dbPath := "data/attendance.db"
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	fmt.Println("Creazione indice UNIQUE idx_emp_time...")
	_, err = db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_emp_time ON records(employee_id, timestamp)")
	if err != nil {
		log.Fatalf("Errore creazione indice: %v. Probabilmente ci sono dei duplicati residui.", err)
	}
	fmt.Println("Indice creato con successo.")
}
