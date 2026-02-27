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

	rows, err := db.Query("SELECT id, name, pin FROM employees ORDER BY id ASC")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	fmt.Println("Dipendenti:")
	for rows.Next() {
		var id int
		var name, pin string
		rows.Scan(&id, &name, &pin)
		fmt.Printf("  ID: %d | Name: %s | PIN: %s\n", id, name, pin)
	}
}
