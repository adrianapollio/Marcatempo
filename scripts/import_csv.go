package main

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

// Configura i percorsi dei due CSV forniti
var csvFiles = []string{
	`timbrature_elaborate_v3.csv`,
	`timbrature_ordinate_v2 (1).csv`, // Attenzione: usare path assoluti se lanciato da altre directory
}

func main() {
	log.Println("Avvio script importazione CSV in attendance.db...")

	db, err := sql.Open("sqlite", "attendance.db")
	if err != nil {
		log.Fatalf("Errore apertura DB: %v", err)
	}
	defer db.Close()

	// La query di inserimento è idempotente grazie all'UNIQUE INDEX in records: employee_id, timestamp
	insertQuery := `INSERT OR IGNORE INTO records (employee_id, employee_name, timestamp, action, status_code, source, latitude, longitude) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	stmt, err := db.Prepare(insertQuery)
	if err != nil {
		log.Fatalf("Errore query prepare: %v", err)
	}
	defer stmt.Close()

	totalProcessed := 0
	totalInserted := 0 // SQLite driver non ritorna easily RowsAffected con Ignore insert, lo stimiamo

	for _, fileName := range csvFiles {
		log.Printf("Elaborazione file: %s", fileName)

		file, err := os.Open(fileName)
		if err != nil {
			log.Printf("!!! Impossibile aprire %s: %v", fileName, err)
			continue
		}

		reader := csv.NewReader(file)
		reader.Comma = ';'
		reader.LazyQuotes = true
		reader.FieldsPerRecord = -1 // Permetti record sfasati/con un numero vario di colonne (i due file sono diversi tra loro)

		records, err := reader.ReadAll()
		file.Close()

		if err != nil {
			log.Printf("Errore lettura file %s: %v", fileName, err)
			continue
		}

		if len(records) <= 1 {
			log.Printf("File %s non contiene record.", fileName)
			continue
		}

		// Raccogli intestazioni per capire quale file stiamo leggendo
		header := records[0]
		isExtendedFile := len(header) >= 9 // E' il file v2 che possiede Inizio Pausa, Fine Pausa, etc...

		for i, row := range records {
			if i == 0 {
				continue // Skip the header
			}

			// Le righe devono avere almeno ID, Name, Data, ingresso (min. 4 indici)
			if len(row) < 4 {
				continue
			}

			userIdStr := strings.TrimSpace(row[0])
			userName := strings.TrimSpace(row[1])
			dateStr := strings.TrimSpace(row[2])

			userId, err := strconv.Atoi(userIdStr)
			if err != nil {
				continue // Forse linea vuota
			}

			// Format DD/MM/YYYY into YYYY-MM-DD
			dateParts := strings.Split(dateStr, "/")
			if len(dateParts) != 3 {
				continue
			}
			year := dateParts[2]
			month := fmt.Sprintf("%02s", dateParts[1])
			day := fmt.Sprintf("%02s", dateParts[0])
			isoDate := fmt.Sprintf("%s-%s-%s", year, month, day)

			var processingValues = make([]struct {
				val    string
				action string
				scode  int
			}, 0)

			// Tutti e due i file hanno questo:
			// row[3] = Ingresso
			if len(row) > 3 && strings.TrimSpace(row[3]) != "" {
				processingValues = append(processingValues, struct {
					val    string
					action string
					scode  int
				}{strings.TrimSpace(row[3]), "In", 0})
			}

			if isExtendedFile {
				// E' timbrature_ordinate_v2
				// UserId;UserName;Data;Ingresso;Inizio Pausa;Fine Pausa;Uscita Trasferta;Rientro Trasferta;Uscita
				if len(row) > 4 && strings.TrimSpace(row[4]) != "" { // Inizio Pausa
					processingValues = append(processingValues, struct {
						val    string
						action string
						scode  int
					}{strings.TrimSpace(row[4]), "I_pausa", 0})
				}
				if len(row) > 5 && strings.TrimSpace(row[5]) != "" { // Fine Pausa
					processingValues = append(processingValues, struct {
						val    string
						action string
						scode  int
					}{strings.TrimSpace(row[5]), "F_pausa", 1})
				}
				if len(row) > 6 && strings.TrimSpace(row[6]) != "" { // Uscita Trasferta
					processingValues = append(processingValues, struct {
						val    string
						action string
						scode  int
					}{strings.TrimSpace(row[6]), "U_trasf", 1})
				}
				if len(row) > 7 && strings.TrimSpace(row[7]) != "" { // Rientro Trasferta
					processingValues = append(processingValues, struct {
						val    string
						action string
						scode  int
					}{strings.TrimSpace(row[7]), "R_trasf", 0})
				}
				if len(row) > 8 && strings.TrimSpace(row[8]) != "" { // Uscita
					processingValues = append(processingValues, struct {
						val    string
						action string
						scode  int
					}{strings.TrimSpace(row[8]), "Out", 1})
				}
			} else {
				// E' timbrature_elaborate_v3
				// UserId;UserName;Data;Ingresso;Uscita
				if len(row) > 4 && strings.TrimSpace(row[4]) != "" {
					processingValues = append(processingValues, struct {
						val    string
						action string
						scode  int
					}{strings.TrimSpace(row[4]), "Out", 1})
				}
			}

			for _, pval := range processingValues {
				rawTimeField := pval.val

				// Fix edge cases dove c'è "07:32 / 17:02" nello stesso blocco
				timeSplits := strings.Split(rawTimeField, "/")
				for _, t := range timeSplits {
					t = strings.TrimSpace(t)
					if t == "" {
						continue
					}
					
					// E' possibile che vi siano orari separati da ; invece che /  es. "16:33;16:34" - anche se sono collegate in excel
					// Se capitano due valori facciamo finta che l'azione resti la stessa
					subSplits := strings.Split(t, ";")
					for _, ts := range subSplits {
						ts = strings.TrimSpace(ts)
						if ts == "" {
							continue
						}

						// Fallback if seconds are missing:
						if len(ts) == 5 { // "HH:MM"
							ts = ts + ":00"
						}

						dateTimeStr := fmt.Sprintf("%s %s", isoDate, ts)
						timestamp, err := time.ParseInLocation("2006-01-02 15:04:05", dateTimeStr, time.Local)
						if err != nil {
							log.Printf("Errore parser date/time: %s sulla riga ID %d: %v", dateTimeStr, userId, err)
							continue
						}

						// Save
						_, err = stmt.Exec(userId, userName, timestamp.Format(time.RFC3339), pval.action, pval.scode, "manual_csv", nil, nil)
						if err != nil {
							log.Printf("Errore insert record (ID: %d %s %v): %v", userId, userName, timestamp, err)
						} else {
							totalInserted++
						}
					}
				}
			}
			totalProcessed++
		}
	}

	log.Printf("Analisi conclusa. Processate %d righe complessive e inserite (tentative unique) %d stampe di orari.", totalProcessed, totalInserted)
}
