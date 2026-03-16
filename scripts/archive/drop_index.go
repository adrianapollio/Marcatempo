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

	fmt.Println("Dropping index idx_emp_time...")
	_, err = db.Exec("DROP INDEX IF EXISTS idx_emp_time")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Index dropped successfully.")
}
