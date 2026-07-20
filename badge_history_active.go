package main

import (
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

func parseCommaSeparatedValues(raw string) []string {
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	seen := make(map[string]struct{})
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}

func (resolver badgeHistoryResolver) latestPersonForEmployeeIDAt(employeeID int, timestamp time.Time) (int64, bool) {
	assignments := resolver.AssignmentsByEmployee[employeeID]
	unixTime := timestamp.Unix()
	var personID int64
	var latestFrom int64
	found := false
	for _, assignment := range assignments {
		if assignment.ValidFromUTC > unixTime {
			continue
		}
		if !found || assignment.ValidFromUTC > latestFrom {
			personID = assignment.PersonID
			latestFrom = assignment.ValidFromUTC
			found = true
		}
	}
	return personID, found
}

func applyBadgeHistoryActiveResolution(records []Record, resolver badgeHistoryResolver) error {
	now := time.Now()
	for i := range records {
		record := &records[i]
		if record.Timestamp.IsZero() {
			return fmt.Errorf("record %d: timestamp non valido", record.ID)
		}
		rawEmployeeID := record.EmployeeID
		record.AnvizEmployeeID = rawEmployeeID

		assignment, _, status := resolver.resolveAt(rawEmployeeID, record.Timestamp)
		switch status {
		case "unmanaged":
			continue
		case "resolved":
			person, ok := resolver.People[assignment.PersonID]
			if !ok {
				return fmt.Errorf("persona %d non caricata per record %d", assignment.PersonID, record.ID)
			}
			personID := person.ID
			record.PersonID = &personID
			record.PersonKey = person.PersonKey
			if person.DisplayName != "" {
				record.EmployeeName = person.DisplayName
			}
			if presentation, found := resolver.presentationAt(person.ID, now); found {
				record.EmployeeID = presentation.AnvizEmployeeID
			}
		default:
			return fmt.Errorf("record %d: storico badge %s per employee_id=%d timestamp=%s", record.ID, status, rawEmployeeID, record.Timestamp.Format(time.RFC3339))
		}
	}
	return nil
}

func getBadgeHistoryActiveRecords(startDate, endDate, employeeIDFilter, personKeyFilter string) ([]Record, error) {
	resolver, err := loadBadgeHistoryResolverUsing(DB)
	if err != nil {
		return nil, fmt.Errorf("caricamento resolver badge active: %w", err)
	}
	requestedPeople := make(map[int64]struct{})
	requestedLegacyIDs := make(map[int]struct{})
	for _, key := range parseCommaSeparatedValues(personKeyFilter) {
		if personID, ok := resolver.PersonIDByKey[key]; ok {
			requestedPeople[personID] = struct{}{}
		}
	}
	for _, rawID := range parseCommaSeparatedValues(employeeIDFilter) {
		employeeID, parseErr := strconv.Atoi(rawID)
		if parseErr != nil || employeeID <= 0 {
			continue
		}
		if personID, ok := resolver.latestPersonForEmployeeIDAt(employeeID, time.Now()); ok {
			requestedPeople[personID] = struct{}{}
		} else {
			requestedLegacyIDs[employeeID] = struct{}{}
		}
	}
	hasRequestedFilter := strings.TrimSpace(personKeyFilter) != "" || strings.TrimSpace(employeeIDFilter) != ""
	if hasRequestedFilter && len(requestedPeople) == 0 && len(requestedLegacyIDs) == 0 {
		return []Record{}, nil
	}

	query := `SELECT id, employee_id, employee_name, timestamp, action, status_code, source, latitude, longitude FROM records WHERE 1=1`
	args := make([]interface{}, 0, 8)
	if hasRequestedFilter {
		candidateIDSet := make(map[int]struct{})
		for personID := range requestedPeople {
			for _, assignment := range resolver.AssignmentsByPerson[personID] {
				candidateIDSet[assignment.AnvizEmployeeID] = struct{}{}
			}
		}
		for employeeID := range requestedLegacyIDs {
			candidateIDSet[employeeID] = struct{}{}
		}
		candidateIDs := make([]int, 0, len(candidateIDSet))
		for employeeID := range candidateIDSet {
			candidateIDs = append(candidateIDs, employeeID)
		}
		sort.Ints(candidateIDs)
		placeholders := strings.TrimRight(strings.Repeat("?,", len(candidateIDs)), ",")
		query += " AND employee_id IN (" + placeholders + ")"
		for _, employeeID := range candidateIDs {
			args = append(args, employeeID)
		}
	}
	if startDate != "" {
		query += " AND date(timestamp) >= date(?)"
		args = append(args, startDate)
	}
	if endDate != "" && endDate != "null" {
		query += " AND date(timestamp) <= date(?)"
		args = append(args, endDate)
	}
	query += " ORDER BY timestamp ASC, id ASC"

	rows, err := DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	records, err := scanRecords(rows)
	closeErr := rows.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err := applyBadgeHistoryActiveResolution(records, resolver); err != nil {
		return nil, err
	}

	if len(requestedPeople) == 0 && len(requestedLegacyIDs) == 0 {
		return records, nil
	}

	filtered := make([]Record, 0, len(records))
	for _, record := range records {
		if record.PersonID != nil {
			if _, ok := requestedPeople[*record.PersonID]; ok {
				filtered = append(filtered, record)
			}
			continue
		}
		if _, ok := requestedLegacyIDs[record.AnvizEmployeeID]; ok {
			filtered = append(filtered, record)
		}
	}
	return filtered, nil
}

func getBadgeHistoryActiveEmployees() ([]Employee, error) {
	resolver, err := loadBadgeHistoryResolverUsing(DB)
	if err != nil {
		return nil, fmt.Errorf("caricamento resolver badge active: %w", err)
	}

	now := time.Now()
	employees := make([]Employee, 0, len(resolver.People))
	managedIDs := make(map[int]struct{})
	for employeeID := range resolver.AssignmentsByEmployee {
		managedIDs[employeeID] = struct{}{}
	}
	for _, person := range resolver.People {
		personID := person.ID
		employee := Employee{
			Name:      person.DisplayName,
			PersonID:  &personID,
			PersonKey: person.PersonKey,
			IsActive:  person.IsActive,
		}
		if presentation, found := resolver.presentationAt(person.ID, now); found {
			employee.ID = presentation.AnvizEmployeeID
			var isAdmin int
			if err := DB.QueryRow(`SELECT is_admin FROM employees WHERE id = ?`, employee.ID).Scan(&isAdmin); err != nil && err != sql.ErrNoRows {
				return nil, err
			}
			employee.IsAdmin = isAdmin == 1
		}
		employees = append(employees, employee)
	}

	rows, err := DB.Query(`SELECT id, name, is_admin FROM employees ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var employee Employee
		var isAdmin int
		if err := rows.Scan(&employee.ID, &employee.Name, &isAdmin); err != nil {
			return nil, err
		}
		if _, managed := managedIDs[employee.ID]; managed {
			continue
		}
		employee.IsAdmin = isAdmin == 1
		employee.IsActive = true
		employees = append(employees, employee)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(employees, func(i, j int) bool {
		left := strings.ToLower(strings.TrimSpace(employees[i].Name))
		right := strings.ToLower(strings.TrimSpace(employees[j].Name))
		if left == right {
			return employees[i].PersonKey < employees[j].PersonKey
		}
		return left < right
	})
	return employees, nil
}

func resolveBadgeForPersonAt(personKey string, timestamp time.Time) (int, string, error) {
	resolver, err := loadBadgeHistoryResolverUsing(DB)
	if err != nil {
		return 0, "", err
	}
	personID, ok := resolver.PersonIDByKey[strings.TrimSpace(personKey)]
	if !ok {
		return 0, "", fmt.Errorf("persona %q non trovata", personKey)
	}
	presentation, found := resolver.presentationAt(personID, timestamp)
	if !found {
		return 0, "", fmt.Errorf("nessun badge assegnato a %q alla data %s", personKey, timestamp.Format(time.RFC3339))
	}
	return presentation.AnvizEmployeeID, resolver.People[personID].DisplayName, nil
}

func loadBadgeHistoryActiveTimelineBeforeUsing(queryer sqlQueryer, employeeID int, timestamp time.Time) ([]deviceTimelineRecord, bool, error) {
	resolver, err := loadBadgeHistoryResolverUsing(queryer)
	if err != nil {
		return nil, true, err
	}
	assignment, _, status := resolver.resolveAt(employeeID, timestamp)
	if status == "unmanaged" {
		return nil, false, nil
	}
	if status != "resolved" {
		return nil, true, fmt.Errorf("storico badge %s per employee_id=%d timestamp=%s", status, employeeID, timestamp.Format(time.RFC3339))
	}

	ids := make([]int, 0, len(resolver.AssignmentsByPerson[assignment.PersonID]))
	seen := make(map[int]struct{})
	for _, item := range resolver.AssignmentsByPerson[assignment.PersonID] {
		if _, exists := seen[item.AnvizEmployeeID]; exists {
			continue
		}
		seen[item.AnvizEmployeeID] = struct{}{}
		ids = append(ids, item.AnvizEmployeeID)
	}
	if len(ids) == 0 {
		return nil, true, nil
	}

	loc := anvizEpochLocation()
	localTS := timestamp.In(loc)
	dayStart := time.Date(localTS.Year(), localTS.Month(), localTS.Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.AddDate(0, 0, 1)
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	query := `SELECT employee_id, timestamp, action, status_code, source FROM records WHERE employee_id IN (` + placeholders + `) AND timestamp >= ? AND timestamp < ? AND timestamp < ? ORDER BY timestamp ASC, id ASC`
	args := make([]interface{}, 0, len(ids)+3)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, dayStart.Format(time.RFC3339), dayEnd.Format(time.RFC3339), timestamp.Format(time.RFC3339))

	rows, err := queryer.Query(query, args...)
	if err != nil {
		return nil, true, err
	}
	defer rows.Close()
	var timeline []deviceTimelineRecord
	for rows.Next() {
		var rawID int
		var timestampRaw string
		var item deviceTimelineRecord
		if err := rows.Scan(&rawID, &timestampRaw, &item.Action, &item.StatusCode, &item.Source); err != nil {
			return nil, true, err
		}
		parsed, err := time.Parse(time.RFC3339, timestampRaw)
		if err != nil {
			return nil, true, fmt.Errorf("timestamp record non valido: %q", timestampRaw)
		}
		rowAssignment, _, rowStatus := resolver.resolveAt(rawID, parsed)
		if rowStatus != "resolved" {
			return nil, true, fmt.Errorf("storico badge %s per record employee_id=%d timestamp=%s", rowStatus, rawID, parsed.Format(time.RFC3339))
		}
		if rowAssignment.PersonID != assignment.PersonID {
			continue
		}
		item.Timestamp = parsed
		timeline = append(timeline, item)
	}
	return timeline, true, rows.Err()
}

func getBadgeHistoryActiveDeviceEquivalent(employeeID int, timestamp time.Time, targetGroup string) (bool, bool, error) {
	resolver, err := loadBadgeHistoryResolverUsing(DB)
	if err != nil {
		return false, true, err
	}
	assignment, _, status := resolver.resolveAt(employeeID, timestamp)
	if status == "unmanaged" {
		return false, false, nil
	}
	if status != "resolved" {
		return false, true, fmt.Errorf("storico badge %s", status)
	}

	ids := make([]int, 0, len(resolver.AssignmentsByPerson[assignment.PersonID]))
	seen := make(map[int]struct{})
	for _, item := range resolver.AssignmentsByPerson[assignment.PersonID] {
		if _, exists := seen[item.AnvizEmployeeID]; exists {
			continue
		}
		seen[item.AnvizEmployeeID] = struct{}{}
		ids = append(ids, item.AnvizEmployeeID)
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	query := `SELECT employee_id, timestamp, action FROM records WHERE employee_id IN (` + placeholders + `) AND source = 'device' AND strftime('%Y-%m-%d %H:%M', timestamp) = strftime('%Y-%m-%d %H:%M', ?)`
	args := make([]interface{}, 0, len(ids)+1)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, timestamp.Format(time.RFC3339))
	rows, err := DB.Query(query, args...)
	if err != nil {
		return false, true, err
	}
	defer rows.Close()
	for rows.Next() {
		var rawID int
		var timestampRaw string
		var dbAction string
		if err := rows.Scan(&rawID, &timestampRaw, &dbAction); err != nil {
			return false, true, err
		}
		parsed, err := time.Parse(time.RFC3339, timestampRaw)
		if err != nil {
			return false, true, err
		}
		rowAssignment, _, rowStatus := resolver.resolveAt(rawID, parsed)
		if rowStatus != "resolved" {
			return false, true, fmt.Errorf("storico badge %s per record employee_id=%d", rowStatus, rawID)
		}
		if rowAssignment.PersonID == assignment.PersonID && actionGroup(dbAction) == targetGroup {
			return true, true, nil
		}
	}
	return false, true, rows.Err()
}
