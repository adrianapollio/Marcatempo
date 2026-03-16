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

	fmt.Println("Eseguo REINDEX...")
	_, err = db.Exec("REINDEX")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("REINDEX completato con successo.")
}
