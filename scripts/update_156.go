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

	newName := "Utente Test 156"
	empID := 156

	// Update employees table
	_, err = db.Exec("UPDATE employees SET name = ? WHERE id = ?", newName, empID)
	if err != nil {
		fmt.Println("Error updating employees:", err)
	}

	// Update pending_validations table
	_, err = db.Exec("UPDATE pending_validations SET employee_name = ? WHERE employee_id = ?", newName, empID)
	if err != nil {
		fmt.Println("Error updating pending_validations:", err)
	}

	// Update records table
	_, err = db.Exec("UPDATE records SET employee_name = ? WHERE employee_id = ?", newName, empID)
	if err != nil {
		fmt.Println("Error updating records:", err)
	}

	fmt.Println("Updated name for employee 156 successfully.")
}
