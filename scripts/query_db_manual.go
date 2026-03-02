package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "attendance.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT id, employee_id, employee_name, timestamp, action, status_code, source FROM records ORDER BY id DESC LIMIT 10`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	fmt.Println("Latest 10 records:")
	for rows.Next() {
		var id, empId, statusCode int
		var empName, timestamp, action, source string
		rows.Scan(&id, &empId, &empName, &timestamp, &action, &statusCode, &source)
		fmt.Printf("ID: %d | Emp: %d | Name: %s | Time: %s | Action: %s | Source: %s\n", id, empId, empName, timestamp, action, source)
	}
}
