package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "./attendance.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec("INSERT OR REPLACE INTO employees (id, name, pin) VALUES (39, 'Nome 39', '1751')")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Dipendente 39 inserito correttamente con PIN 1751")
}
