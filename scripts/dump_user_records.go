package main

import (
	"database/sql"
	"fmt"
	"log"
	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "data/attendance.db")
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	defer db.Close()

	ids := []int{40, 41, 45, 51, 61, 64}
	for _, id := range ids {
		fmt.Printf("--- ID %d ---\n", id)
		rows, err := db.Query("SELECT timestamp, action, source, status_code FROM records WHERE employee_id = ? AND date(timestamp) = '2026-03-16' ORDER BY timestamp", id)
		if err != nil {
			log.Printf("Error querying ID %d: %v", id, err)
			continue
		}
		for rows.Next() {
			var ts, act, src string
			var sc int
			rows.Scan(&ts, &act, &src, &sc)
			fmt.Printf("%s | %-10s | %-10s | %d\n", ts, act, src, sc)
		}
		rows.Close()
	}
}
