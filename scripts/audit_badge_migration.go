package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

type idSet map[int]struct{}

type databaseAudit struct {
	IntegrityResult       string
	ForeignKeyErrors      int
	TableCounts           map[string]int64
	EmployeeIDs           idSet
	RecordEmployeeIDs     idSet
	RawRecordEmployeeIDs  idSet
	EnabledAliasCount     int
	AliasEndpointIDs      idSet
	DanglingAliasCount    int
	AliasCycleCount       int
	FirstRecordTimestamp  string
	LatestRecordTimestamp string
}

func main() {
	var dbPath string
	var label string
	var details bool

	flag.StringVar(&dbPath, "db", "", "percorso del database attendance.db")
	flag.StringVar(&label, "label", "deploy", "etichetta del deploy, per esempio Napoli o Ferrara")
	flag.BoolVar(&details, "details", false, "mostra gli ID problematici (mai PIN o password)")
	flag.Parse()

	if strings.TrimSpace(dbPath) == "" {
		log.Fatal("parametro obbligatorio: -db /percorso/attendance.db")
	}

	report, err := auditDatabase(dbPath)
	if err != nil {
		log.Fatalf("audit database fallito: %v", err)
	}
	printReport(label, dbPath, report, details)
}

func auditDatabase(path string) (databaseAudit, error) {
	report := databaseAudit{
		TableCounts:          make(map[string]int64),
		EmployeeIDs:          make(idSet),
		RecordEmployeeIDs:    make(idSet),
		RawRecordEmployeeIDs: make(idSet),
		AliasEndpointIDs:     make(idSet),
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return report, err
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return report, err
	}
	if info.IsDir() {
		return report, fmt.Errorf("il percorso DB e una directory: %s", absPath)
	}

	dsn := "file:" + filepath.ToSlash(absPath) + "?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return report, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		return report, err
	}
	if err := db.QueryRow("PRAGMA quick_check").Scan(&report.IntegrityResult); err != nil {
		return report, fmt.Errorf("PRAGMA quick_check: %w", err)
	}

	rows, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		return report, fmt.Errorf("PRAGMA foreign_key_check: %w", err)
	}
	for rows.Next() {
		report.ForeignKeyErrors++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return report, err
	}
	rows.Close()

	tables := []string{
		"records",
		"device_raw_records",
		"employees",
		"employee_aliases",
		"pending_validations",
	}
	for _, table := range tables {
		exists, err := tableExists(db, table)
		if err != nil {
			return report, err
		}
		if !exists {
			report.TableCounts[table] = -1
			continue
		}

		var count int64
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			return report, fmt.Errorf("conteggio %s: %w", table, err)
		}
		report.TableCounts[table] = count
	}

	if report.TableCounts["employees"] >= 0 {
		ids, err := loadIntegerColumn(db, "SELECT id FROM employees")
		if err != nil {
			return report, err
		}
		report.EmployeeIDs = ids
	}
	if report.TableCounts["records"] >= 0 {
		ids, err := loadIntegerColumn(db, "SELECT DISTINCT employee_id FROM records")
		if err != nil {
			return report, err
		}
		report.RecordEmployeeIDs = ids
		if err := db.QueryRow(`
			SELECT COALESCE(MIN(timestamp), ''), COALESCE(MAX(timestamp), '')
			FROM records
		`).Scan(&report.FirstRecordTimestamp, &report.LatestRecordTimestamp); err != nil {
			return report, err
		}
	}
	if report.TableCounts["device_raw_records"] >= 0 {
		ids, err := loadIntegerColumn(db, "SELECT DISTINCT employee_id FROM device_raw_records")
		if err != nil {
			return report, err
		}
		report.RawRecordEmployeeIDs = ids
	}

	if report.TableCounts["employee_aliases"] >= 0 {
		aliasRows, err := db.Query(`
			SELECT alias_employee_id, canonical_employee_id
			FROM employee_aliases
			WHERE enabled = 1
		`)
		if err != nil {
			return report, err
		}

		aliases := make(map[int]int)
		for aliasRows.Next() {
			var aliasID int
			var canonicalID int
			if err := aliasRows.Scan(&aliasID, &canonicalID); err != nil {
				aliasRows.Close()
				return report, err
			}
			aliases[aliasID] = canonicalID
			report.AliasEndpointIDs[aliasID] = struct{}{}
			report.AliasEndpointIDs[canonicalID] = struct{}{}
		}
		if err := aliasRows.Err(); err != nil {
			aliasRows.Close()
			return report, err
		}
		aliasRows.Close()

		report.EnabledAliasCount = len(aliases)
		for id := range report.AliasEndpointIDs {
			if _, ok := report.EmployeeIDs[id]; !ok {
				report.DanglingAliasCount++
			}
		}
		report.AliasCycleCount = countAliasCycles(aliases)
	}

	return report, nil
}

func tableExists(db *sql.DB, name string) (bool, error) {
	var count int
	err := db.QueryRow(`
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table' AND name = ?
	`, name).Scan(&count)
	return count > 0, err
}

func loadIntegerColumn(db *sql.DB, query string) (idSet, error) {
	result := make(idSet)
	rows, err := db.Query(query)
	if err != nil {
		return result, err
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return result, err
		}
		result[id] = struct{}{}
	}
	return result, rows.Err()
}

func countAliasCycles(aliases map[int]int) int {
	cyclic := make(idSet)
	for origin := range aliases {
		visited := make(map[int]int)
		current := origin
		step := 0
		for {
			if firstStep, ok := visited[current]; ok {
				for id, seenAt := range visited {
					if seenAt >= firstStep {
						cyclic[id] = struct{}{}
					}
				}
				break
			}
			visited[current] = step
			step++
			next, ok := aliases[current]
			if !ok {
				break
			}
			current = next
		}
	}
	return len(cyclic)
}

func printReport(label, dbPath string, report databaseAudit, details bool) {
	fmt.Printf("Audit badge - %s\n", label)
	fmt.Printf("Database: %s\n", dbPath)
	fmt.Printf("Integrita SQLite: %s\n", report.IntegrityResult)
	fmt.Printf("Errori foreign key: %d\n", report.ForeignKeyErrors)

	tableNames := make([]string, 0, len(report.TableCounts))
	for table := range report.TableCounts {
		tableNames = append(tableNames, table)
	}
	sort.Strings(tableNames)
	for _, table := range tableNames {
		count := report.TableCounts[table]
		if count < 0 {
			fmt.Printf("%-24s assente\n", table+":")
		} else {
			fmt.Printf("%-24s %d\n", table+":", count)
		}
	}

	fmt.Printf("ID distinti in employees: %d\n", len(report.EmployeeIDs))
	fmt.Printf("ID distinti in records: %d\n", len(report.RecordEmployeeIDs))
	fmt.Printf("ID distinti in raw: %d\n", len(report.RawRecordEmployeeIDs))
	fmt.Printf("Alias abilitati: %d\n", report.EnabledAliasCount)
	fmt.Printf("Endpoint alias senza employee: %d\n", report.DanglingAliasCount)
	fmt.Printf("ID coinvolti in cicli alias: %d\n", report.AliasCycleCount)
	if report.FirstRecordTimestamp != "" || report.LatestRecordTimestamp != "" {
		fmt.Printf("Periodo records: %s -> %s\n", report.FirstRecordTimestamp, report.LatestRecordTimestamp)
	}

	recordsWithoutEmployee := difference(report.RecordEmployeeIDs, report.EmployeeIDs)
	rawWithoutEmployee := difference(report.RawRecordEmployeeIDs, report.EmployeeIDs)
	fmt.Printf("ID records senza employee: %d\n", len(recordsWithoutEmployee))
	fmt.Printf("ID raw senza employee: %d\n", len(rawWithoutEmployee))

	if details {
		printIDs("ID records senza employee", recordsWithoutEmployee)
		printIDs("ID raw senza employee", rawWithoutEmployee)
	}
}

func difference(left, right idSet) []int {
	result := make([]int, 0)
	for id := range left {
		if _, ok := right[id]; !ok {
			result = append(result, id)
		}
	}
	sort.Ints(result)
	return result
}

func printIDs(label string, ids []int) {
	if len(ids) == 0 {
		return
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.Itoa(id))
	}
	fmt.Printf("%s: %s\n", label, strings.Join(parts, ","))
}
