package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "./data/attendance.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM records").Scan(&count)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Totale Timbrature nel DB: %d\n", count)
	
	// Stampo le ultimissime
	rows, err := db.Query("SELECT employee_id, timestamp, action FROM records ORDER BY id DESC LIMIT 5")
	if err == nil {
		defer rows.Close()
		fmt.Println("Ultime Timbrature:")
		for rows.Next() {
			var empID int
			var ts string
			var act string
			rows.Scan(&empID, &ts, &act)
			fmt.Printf("  - Emp: %d | Time: %s | Action: %s\n", empID, ts, act)
		}
	}
}
