package main

import (
	"archive/zip"
	"database/sql"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

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

func main() {
	fileName := "20260316_R.xlsx"
	if _, err := os.Stat(fileName); os.IsNotExist(err) {
		log.Fatalf("File non trovato: %s", fileName)
	}

	db, err := sql.Open("sqlite", "data/attendance.db")
	if err != nil {
		log.Fatalf("Impossibile aprire il DB: %v", err)
	}
	defer db.Close()

	r, err := zip.OpenReader(fileName)
	if err != nil {
		log.Fatalf("Error opening zip: %v", err)
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

	location, _ := time.LoadLocation("Europe/Rome")
	
	fmt.Printf("Analisi file %s (%d righe)...\n", fileName, len(worksheet.SheetData.Row))
	
	// Analisi riga per riga
	for i, row := range worksheet.SheetData.Row {
		if i == 0 { continue } // Header
		
		var empID int
		var empName, actionRaw, dateRaw string
		
		for _, col := range row.C {
			val := col.V
			if col.T == "s" {
				idx, _ := strconv.Atoi(val)
				if idx >= 0 && idx < len(sharedStrings.Si) {
					val = sharedStrings.Si[idx].T
				}
			}
			
			colName := strings.ToUpper(col.R)
			if strings.HasPrefix(colName, "A") { // ID
				empID, _ = strconv.Atoi(val)
			} else if strings.HasPrefix(colName, "B") { // Nome
				empName = val
			} else if strings.HasPrefix(colName, "C") { // Data/Ora
				dateRaw = val
			} else if strings.HasPrefix(colName, "D") { // Stato
				actionRaw = val
			}
		}
		
		if empID == 0 { continue }
		
		var ts time.Time
		if _, err := strconv.ParseFloat(dateRaw, 64); err == nil {
			ts, _ = excelDateToTime(dateRaw, location)
		} else {
			ts, _ = time.ParseInLocation("2006-01-02 15:04:05", dateRaw, location)
		}
		
		if ts.IsZero() { continue }
		
		// Verifica nel DB
		// Cerchiamo un record per lo stesso dipendente nello stesso minuto (tolleranza 1m per arrotondamenti Excel)
		startTime := ts.Add(-30 * time.Second).Format(time.RFC3339)
		endTime := ts.Add(30 * time.Second).Format(time.RFC3339)
		
		var dbCount int
		err = db.QueryRow("SELECT COUNT(*) FROM records WHERE employee_id = ? AND timestamp BETWEEN ? AND ?", empID, startTime, endTime).Scan(&dbCount)
		
		if dbCount == 0 {
			fmt.Printf("MANCANTE: ID %d (%s) - %s - %s\n", empID, empName, ts.Format("2006-01-02 15:04:05"), actionRaw)
		}
	}
}
