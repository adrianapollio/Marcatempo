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

	empID := 39
	date := "2026-02-09"

	// 1. Mostra i record attuali
	fmt.Println("=== RECORD ATTUALI ===")
	rows, err := db.Query(`SELECT id, employee_id, employee_name, timestamp, action, status_code, source 
		FROM records WHERE employee_id = ? AND date(timestamp) = date(?) ORDER BY timestamp ASC`, empID, date)
	if err != nil {
		log.Fatal(err)
	}

	type rec struct {
		id         int
		action     string
		statusCode int
	}
	var inRec, outRec *rec

	for rows.Next() {
		var id, empId, sc int
		var name, ts, action, source string
		rows.Scan(&id, &empId, &name, &ts, &action, &sc, &source)
		fmt.Printf("  ID=%d | %s | action=%s | status_code=%d | source=%s\n", id, ts, action, sc, source)

		// Identifica i record di Entrata (In) e Uscita (Out) da invertire
		if action == "In" || action == "in" || action == "entrata" {
			inRec = &rec{id: id, action: action, statusCode: sc}
		}
		if action == "Out" || action == "out" || action == "uscita" {
			outRec = &rec{id: id, action: action, statusCode: sc}
		}
	}
	rows.Close()

	if inRec == nil || outRec == nil {
		fmt.Println("Non ho trovato sia Entrata che Uscita per questo giorno. Nessuna modifica.")
		return
	}

	fmt.Printf("\n=== SWAP: ID %d (attualmente %s) <-> ID %d (attualmente %s) ===\n", inRec.id, inRec.action, outRec.id, outRec.action)

	// 2. Scambia le azioni e i status_code
	// In -> Out (status_code 0 -> 1), Out -> In (status_code 1 -> 0)
	_, err = db.Exec(`UPDATE records SET action = ?, status_code = ? WHERE id = ?`, outRec.action, outRec.statusCode, inRec.id)
	if err != nil {
		log.Fatalf("Errore swap record %d: %v", inRec.id, err)
	}

	_, err = db.Exec(`UPDATE records SET action = ?, status_code = ? WHERE id = ?`, inRec.action, inRec.statusCode, outRec.id)
	if err != nil {
		log.Fatalf("Errore swap record %d: %v", outRec.id, err)
	}

	fmt.Println("Swap completato!")

	// 3. Verifica
	fmt.Println("\n=== RECORD DOPO LO SWAP ===")
	rows2, _ := db.Query(`SELECT id, employee_id, employee_name, timestamp, action, status_code, source 
		FROM records WHERE employee_id = ? AND date(timestamp) = date(?) ORDER BY timestamp ASC`, empID, date)
	for rows2.Next() {
		var id, empId, sc int
		var name, ts, action, source string
		rows2.Scan(&id, &empId, &name, &ts, &action, &sc, &source)
		fmt.Printf("  ID=%d | %s | action=%s | status_code=%d | source=%s\n", id, ts, action, sc, source)
	}
	rows2.Close()
}
