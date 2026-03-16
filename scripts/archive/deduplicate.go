package main

import (
	"database/sql"
	"log"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	dbPath := "data/attendance.db"
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Query("SELECT id, employee_id, timestamp, action FROM records")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	type record struct {
		id         int
		empID      int
		t          time.Time
		action     string
	}

	seen := make(map[string]int) // key: empID_time_action -> id
	var toDelete []int

	for rows.Next() {
		var r record
		var tStr string
		if err := rows.Scan(&r.id, &r.empID, &tStr, &r.action); err != nil {
			log.Fatal(err)
		}

		// Try to parse in different formats
		t, err := time.Parse(time.RFC3339, tStr)
		if err != nil {
			t, err = time.Parse("2006-01-02T15:04:05Z07:00", tStr)
		}
		if err != nil {
			log.Printf("Non riesco a parsare %s: %v", tStr, err)
			continue
		}
		r.t = t.UTC()

		key := string(r.empID) + "_" + r.t.Format(time.RFC3339) + "_" + r.action
		if existingID, ok := seen[key]; ok {
			// Duplicate found. Keep the one with smaller ID (usually the original)
			toDelete = append(toDelete, r.id)
			_ = existingID
		} else {
			seen[key] = r.id
		}
	}

	for _, id := range toDelete {
		_, err := db.Exec("DELETE FROM records WHERE id = ?", id)
		if err != nil {
			log.Printf("Errore delete %d: %v", id, err)
		}
	}

	log.Printf("Deduplicazione completata. Eliminati %d record duplicati.", len(toDelete))
}
