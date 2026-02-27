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

	rows, err := db.Query("SELECT id, name, pin FROM employees WHERE id = 39")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	if rows.Next() {
		var id int
		var name, pin string
		if err := rows.Scan(&id, &name, &pin); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Dipendente %d: Nome='%s' PIN='%s'\n", id, name, pin)
	} else {
		fmt.Println("Nessun dipendente trovato con ID 39 nel DB!")
	}
}
