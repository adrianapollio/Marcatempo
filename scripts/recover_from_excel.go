package main

import (
	"archive/zip"
	"database/sql"
	"encoding/xml"
	"io/ioutil"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Excel XML structures for parsing .xlsx files
type SstXML struct {
	XMLName xml.Name `xml:"sst"`
	Si      []struct {
		T string `xml:"t"`
	} `xml:"si"`
}

type WorksheetXML struct {
	XMLName   xml.Name `xml:"worksheet"`
	SheetData struct {
		Row []struct {
			C []struct {
				R  string `xml:"r,attr"`
				T  string `xml:"t,attr"`
				V  string `xml:"v"`
				Is struct {
					T string `xml:"t"`
				} `xml:"is"`
			} `xml:"c"`
		} `xml:"row"`
	} `xml:"sheetData"`
}

// excelDateToTime converte un numero seriale Excel in un timestamp.
// I timestamp Excel sono il numero di giorni dal 30/12/1899.
func excelDateToTime(excelDateStr string, loc *time.Location) (time.Time, error) {
	f, err := strconv.ParseFloat(excelDateStr, 64)
	if err != nil {
		return time.Time{}, err
	}

	wholeDays, fractionalDay := math.Modf(f)
	baseDateUTC := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(wholeDays))

	totalNanos := int64(math.Round(fractionalDay * float64(24*time.Hour)))
	dayNanos := int64(24 * time.Hour)
	if totalNanos >= dayNanos {
		baseDateUTC = baseDateUTC.AddDate(0, 0, 1)
		totalNanos -= dayNanos
	}

	hour := int(totalNanos / int64(time.Hour))
	totalNanos %= int64(time.Hour)
	minute := int(totalNanos / int64(time.Minute))
	totalNanos %= int64(time.Minute)
	second := int(totalNanos / int64(time.Second))
	nanosecond := int(totalNanos % int64(time.Second))

	return time.Date(baseDateUTC.Year(), baseDateUTC.Month(), baseDateUTC.Day(), hour, minute, second, nanosecond, loc), nil
}

// readExcelRows legge tutte le righe da un file .xlsx e restituisce una slice di slice di stringhe
func readExcelRows(fileName string) [][]string {
	r, err := zip.OpenReader(fileName)
	if err != nil {
		log.Fatalf("Impossibile aprire il file Excel %s: %v", fileName, err)
	}
	defer r.Close()

	var sharedStrings SstXML
	var worksheet WorksheetXML

	for _, f := range r.File {
		name := strings.ToLower(f.Name)
		if name == "xl/sharedstrings.xml" {
			rc, err := f.Open()
			if err == nil {
				data, _ := ioutil.ReadAll(rc)
				xml.Unmarshal(data, &sharedStrings)
				rc.Close()
			}
		}
		if name == "xl/worksheets/sheet1.xml" {
			rc, err := f.Open()
			if err == nil {
				data, _ := ioutil.ReadAll(rc)
				xml.Unmarshal(data, &worksheet)
				rc.Close()
			}
		}
	}

	var parsedRows [][]string
	for _, row := range worksheet.SheetData.Row {
		var rowData []string
		for _, col := range row.C {
			val := col.V
			if col.T == "s" {
				idx, _ := strconv.Atoi(val)
				if idx >= 0 && idx < len(sharedStrings.Si) {
					val = sharedStrings.Si[idx].T
				}
			} else if col.T == "inlineStr" {
				val = col.Is.T
			}
			rowData = append(rowData, val)
		}
		if len(rowData) > 0 {
			parsedRows = append(parsedRows, rowData)
		}
	}
	return parsedRows
}

// mapAction normalizza il nome dell'azione dal formato Anviz al formato interno del DB
func mapAction(rawAction string) (action string, statusCode int) {
	upper := strings.ToUpper(strings.TrimSpace(rawAction))
	switch {
	case upper == "ENTRATA" || upper == "IN":
		return "In", 0
	case upper == "USCITA" || upper == "OUT":
		return "Out", 1
	case strings.HasPrefix(upper, "I_PAUSA") || strings.HasPrefix(upper, "INIZIO PAUSA") || upper == "I_BREAK":
		return "I_pausa", 2
	case strings.HasPrefix(upper, "F_PAUSA") || strings.HasPrefix(upper, "FINE PAUSA") || upper == "F_BREAK":
		return "F_pausa", 3
	case strings.HasPrefix(upper, "U_TRASFER") || strings.HasPrefix(upper, "USCITA TRASF") || strings.HasPrefix(upper, "INIZIO TRASF"):
		return "U_trasf", 4
	case strings.HasPrefix(upper, "R_TRASFER") || strings.HasPrefix(upper, "RITORNO TRASF") || strings.HasPrefix(upper, "RIENTRO TRASF"):
		return "R_trasf", 5
	default:
		log.Printf("  [WARN] Azione sconosciuta: '%s' -> default 'In'", rawAction)
		return "In", 0
	}
}

func main() {
	// Determina il file Excel da leggere
	fileName := ""
	if len(os.Args) > 1 {
		fileName = os.Args[1]
	}

	if fileName == "" {
		// Cerca file con pattern comuni se non specificato
		files, _ := ioutil.ReadDir(".")
		for _, f := range files {
			name := f.Name()
			if strings.HasSuffix(strings.ToLower(name), ".xlsx") && 
				(strings.Contains(name, "_R") || strings.Contains(name, "export")) {
				fileName = name
				break
			}
		}
	}

	if fileName == "" {
		fileName = "20260316_R.xlsx" // Fallback finale
	}

	if _, err := os.Stat(fileName); os.IsNotExist(err) {
		log.Fatalf("File non trovato: %s", fileName)
	}

	// ... (Database and location setup remains same)
	dbPath := "attendance.db"
	if envPath := os.Getenv("DB_PATH"); envPath != "" {
		dbPath = envPath
	}
	for _, p := range []string{"data/attendance.db", dbPath, "../attendance.db"} {
		if _, err := os.Stat(p); err == nil {
			dbPath = p
			break
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("Impossibile aprire il DB (%s): %v", dbPath, err)
	}
	defer db.Close()

	location, _ := time.LoadLocation("Europe/Rome")
	if location == nil { location = time.Local }

	log.Printf("=== Importazione timbrature da %s ===", fileName)
	rows := readExcelRows(fileName)
	if len(rows) == 0 { log.Fatal("Nessuna riga trovata.") }

	// Rilevamento dinamico delle colonne
	idxID, idxName, idxDate, idxAction := -1, -1, -1, -1
	header := rows[0]
	for i, col := range header {
		c := strings.ToUpper(col)
		if strings.Contains(c, "ID") { idxID = i }
		if strings.Contains(c, "NOM") { idxName = i }
		if strings.Contains(c, "DAT") || strings.Contains(c, "ORA") { idxDate = i }
		if strings.Contains(c, "STATO") || strings.Contains(c, "AZION") || strings.Contains(c, "TIPO") { idxAction = i }
	}

	// Fallback se non trova gli header
	if idxID == -1 { idxID = 0 }
	if idxName == -1 { idxName = 1 }
	if idxDate == -1 { idxDate = 2 }
	if idxAction == -1 { idxAction = 3 }

	log.Printf("Mapping colonne: ID=%d, Nome=%d, Data=%d, Stato=%d", idxID, idxName, idxDate, idxAction)

	tx, _ := db.Begin()
	stmt, _ := tx.Prepare(`INSERT OR IGNORE INTO records (employee_id, employee_name, timestamp, action, status_code, source) VALUES (?, ?, ?, ?, ?, 'device')`)
	defer stmt.Close()

	inserted := 0
	for i, row := range rows {
		if i == 0 { continue } // Skip header
		if len(row) <= max(idxID, max(idxName, max(idxDate, idxAction))) { continue }

		empID, _ := strconv.Atoi(strings.TrimSpace(row[idxID]))
		if empID == 0 { continue }
		
		empName := strings.TrimSpace(row[idxName])
		dateRaw := strings.TrimSpace(row[idxDate])
		actionRaw := strings.TrimSpace(row[idxAction])

		var ts time.Time
		if f, err := strconv.ParseFloat(dateRaw, 64); err == nil && f > 40000 {
			ts, _ = excelDateToTime(dateRaw, location)
		} else {
			ts, _ = time.ParseInLocation("2006-01-02 15:04:05", dateRaw, location)
		}

		if ts.IsZero() { continue }

		action, sc := mapAction(actionRaw)
		_, err := stmt.Exec(empID, empName, ts.Format(time.RFC3339), action, sc)
		if err == nil { inserted++ }
	}
	tx.Commit()

	log.Printf("✓ Importazione completata. Record processati: %d", inserted)
}

func max(a, b int) int {
	if a > b { return a }
	return b
}
