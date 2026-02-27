package main

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "attendance.db")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	rows, err := db.Query("SELECT id, status FROM pending_validations")
	if err != nil {
		panic(err)
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		var status string
		if err := rows.Scan(&id, &status); err != nil {
			panic(err)
		}
		fmt.Printf("ID: %d - Status: %s\n", id, status)
	}
}
