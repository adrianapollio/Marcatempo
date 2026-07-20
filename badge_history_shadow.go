package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	badgeHistoryModeOff    = "off"
	badgeHistoryModeShadow = "shadow"
	badgeHistoryModeActive = "active"
)

type badgeHistoryPerson struct {
	ID          int64
	PersonKey   string
	DisplayName string
	IsActive    bool
}

type badgeHistoryAssignment struct {
	PersonID        int64
	PersonKey       string
	AnvizEmployeeID int
	ValidFromUTC    int64
	ValidToUTC      *int64
}

type badgeHistoryPresentation struct {
	AnvizEmployeeID int
	ValidFromUTC    int64
	Open            bool
}

type badgeHistoryResolver struct {
	AssignmentsByEmployee map[int][]badgeHistoryAssignment
	AssignmentsByPerson   map[int64][]badgeHistoryAssignment
	People                map[int64]badgeHistoryPerson
	PersonIDByKey         map[string]int64
	AssignmentCount       int
}

type badgeHistoryShadowReport struct {
	Total           int
	Matched         int
	Mismatched      int
	Unmanaged       int
	IntervalMissing int
	Ambiguous       int
	InvalidTime     int
	MismatchSamples []string
}

func runBadgeHistoryStartupShadowAudit() error {
	mode := badgeHistoryMode()
	if mode == "" || mode == badgeHistoryModeOff {
		return nil
	}
	if mode != badgeHistoryModeShadow && mode != badgeHistoryModeActive {
		return fmt.Errorf("BADGE_HISTORY_MODE=%q non valido: valori ammessi off, shadow, active", mode)
	}

	ctx, err := loadEmployeeCanonicalContext()
	if err != nil {
		return fmt.Errorf("caricamento alias: %w", err)
	}
	resolver, err := loadBadgeHistoryResolverUsing(DB)
	if err != nil {
		return fmt.Errorf("caricamento storico badge: %w", err)
	}

	logPrefix := "BADGE_HISTORY_SHADOW"
	if mode == badgeHistoryModeActive {
		logPrefix = "BADGE_HISTORY_ACTIVE"
	}
	log.Printf("[%s] storico caricato: persone=%d assegnazioni=%d", logPrefix, len(resolver.People), resolver.AssignmentCount)

	checks := []struct {
		label string
		query string
	}{
		{
			label: "records",
			query: `SELECT id, employee_id, timestamp FROM records ORDER BY id`,
		},
		{
			label: "device_raw_records",
			query: `SELECT id, employee_id, parsed_timestamp FROM device_raw_records ORDER BY id`,
		},
	}

	for _, check := range checks {
		report, err := auditBadgeHistoryRowsUsing(DB, check.query, ctx.DirectAliasToCanonical, resolver)
		if err != nil {
			return fmt.Errorf("audit %s: %w", check.label, err)
		}
		log.Printf(
			"[%s] tabella=%s totali=%d coerenti=%d differenze=%d non_gestiti=%d intervallo_assente=%d ambigui=%d timestamp_invalidi=%d",
			logPrefix,
			check.label,
			report.Total,
			report.Matched,
			report.Mismatched,
			report.Unmanaged,
			report.IntervalMissing,
			report.Ambiguous,
			report.InvalidTime,
		)
		for _, sample := range report.MismatchSamples {
			log.Printf("[%s] [DIFF] tabella=%s %s", logPrefix, check.label, sample)
		}
	}

	return nil
}

func badgeHistoryMode() string {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("BADGE_HISTORY_MODE")))
	if mode == "" {
		return badgeHistoryModeOff
	}
	return mode
}

func badgeHistoryActive() bool {
	return badgeHistoryMode() == badgeHistoryModeActive
}

func loadBadgeHistoryResolverUsing(queryer sqlQueryer) (badgeHistoryResolver, error) {
	resolver := badgeHistoryResolver{
		AssignmentsByEmployee: make(map[int][]badgeHistoryAssignment),
		AssignmentsByPerson:   make(map[int64][]badgeHistoryAssignment),
		People:                make(map[int64]badgeHistoryPerson),
		PersonIDByKey:         make(map[string]int64),
	}

	rows, err := queryer.Query(`
		SELECT h.person_id,
		       p.person_key,
		       p.display_name,
		       p.is_active,
		       h.anviz_employee_id,
		       h.valid_from_utc,
		       h.valid_to_utc
		FROM person_badge_history h
		JOIN people p ON p.id = h.person_id
		WHERE h.voided_at_utc IS NULL
		ORDER BY h.anviz_employee_id, h.valid_from_utc, h.id
	`)
	if err != nil {
		return resolver, err
	}
	defer rows.Close()

	for rows.Next() {
		var assignment badgeHistoryAssignment
		var displayName string
		var isActive int
		var validTo sql.NullInt64
		if err := rows.Scan(
			&assignment.PersonID,
			&assignment.PersonKey,
			&displayName,
			&isActive,
			&assignment.AnvizEmployeeID,
			&assignment.ValidFromUTC,
			&validTo,
		); err != nil {
			return resolver, err
		}
		if validTo.Valid {
			value := validTo.Int64
			assignment.ValidToUTC = &value
		}
		if strings.TrimSpace(assignment.PersonKey) == "" || strings.TrimSpace(displayName) == "" {
			return resolver, fmt.Errorf("persona %d senza chiave o nome", assignment.PersonID)
		}
		if assignment.ValidToUTC != nil && *assignment.ValidToUTC <= assignment.ValidFromUTC {
			return resolver, fmt.Errorf("intervallo non valido per assegnazione employee_id=%d", assignment.AnvizEmployeeID)
		}

		resolver.AssignmentsByEmployee[assignment.AnvizEmployeeID] = append(
			resolver.AssignmentsByEmployee[assignment.AnvizEmployeeID],
			assignment,
		)
		resolver.AssignmentsByPerson[assignment.PersonID] = append(
			resolver.AssignmentsByPerson[assignment.PersonID],
			assignment,
		)
		person := badgeHistoryPerson{
			ID:          assignment.PersonID,
			PersonKey:   assignment.PersonKey,
			DisplayName: strings.TrimSpace(displayName),
			IsActive:    isActive == 1,
		}
		resolver.People[assignment.PersonID] = person
		resolver.PersonIDByKey[assignment.PersonKey] = assignment.PersonID
		resolver.AssignmentCount++
	}
	if err := rows.Err(); err != nil {
		return resolver, err
	}

	for employeeID := range resolver.AssignmentsByEmployee {
		sort.Slice(resolver.AssignmentsByEmployee[employeeID], func(i, j int) bool {
			return resolver.AssignmentsByEmployee[employeeID][i].ValidFromUTC < resolver.AssignmentsByEmployee[employeeID][j].ValidFromUTC
		})
	}
	for personID := range resolver.AssignmentsByPerson {
		sort.Slice(resolver.AssignmentsByPerson[personID], func(i, j int) bool {
			return resolver.AssignmentsByPerson[personID][i].ValidFromUTC < resolver.AssignmentsByPerson[personID][j].ValidFromUTC
		})
		if err := validateBadgeHistoryIntervals("person_id", personID, resolver.AssignmentsByPerson[personID]); err != nil {
			return resolver, err
		}
	}
	for employeeID := range resolver.AssignmentsByEmployee {
		if err := validateBadgeHistoryIntervals("anviz_employee_id", int64(employeeID), resolver.AssignmentsByEmployee[employeeID]); err != nil {
			return resolver, err
		}
	}
	if badgeHistoryActive() && resolver.AssignmentCount == 0 {
		return resolver, fmt.Errorf("storico badge vuoto in modalita active")
	}

	return resolver, nil
}

func validateBadgeHistoryIntervals(label string, id int64, assignments []badgeHistoryAssignment) error {
	for i := 1; i < len(assignments); i++ {
		previous := assignments[i-1]
		current := assignments[i]
		if previous.ValidToUTC == nil || current.ValidFromUTC < *previous.ValidToUTC {
			return fmt.Errorf("intervalli sovrapposti per %s=%d", label, id)
		}
	}
	return nil
}

func (resolver badgeHistoryResolver) presentationAt(personID int64, timestamp time.Time) (badgeHistoryPresentation, bool) {
	assignments := resolver.AssignmentsByPerson[personID]
	unixTime := timestamp.Unix()
	var result badgeHistoryPresentation
	found := false
	for _, assignment := range assignments {
		if unixTime < assignment.ValidFromUTC {
			continue
		}
		if assignment.ValidToUTC != nil && unixTime >= *assignment.ValidToUTC {
			continue
		}
		candidate := badgeHistoryPresentation{
			AnvizEmployeeID: assignment.AnvizEmployeeID,
			ValidFromUTC:    assignment.ValidFromUTC,
			Open:            assignment.ValidToUTC == nil,
		}
		if !found || candidate.ValidFromUTC > result.ValidFromUTC {
			result = candidate
			found = true
		}
	}
	return result, found
}

func (resolver badgeHistoryResolver) resolveAt(employeeID int, timestamp time.Time) (badgeHistoryAssignment, int, string) {
	assignments, managed := resolver.AssignmentsByEmployee[employeeID]
	if !managed {
		return badgeHistoryAssignment{}, 0, "unmanaged"
	}

	unixTime := timestamp.Unix()
	matches := make([]badgeHistoryAssignment, 0, 1)
	for _, assignment := range assignments {
		if unixTime < assignment.ValidFromUTC {
			continue
		}
		if assignment.ValidToUTC != nil && unixTime >= *assignment.ValidToUTC {
			continue
		}
		matches = append(matches, assignment)
	}

	if len(matches) == 0 {
		return badgeHistoryAssignment{}, 0, "interval_missing"
	}
	if len(matches) > 1 {
		return badgeHistoryAssignment{}, 0, "ambiguous"
	}

	assignment := matches[0]
	presentation, ok := resolver.presentationAt(assignment.PersonID, time.Now())
	if !ok || presentation.AnvizEmployeeID <= 0 {
		return assignment, assignment.AnvizEmployeeID, "resolved"
	}
	return assignment, presentation.AnvizEmployeeID, "resolved"
}

func auditBadgeHistoryRowsUsing(queryer sqlQueryer, query string, aliases map[int]int, resolver badgeHistoryResolver) (badgeHistoryShadowReport, error) {
	var report badgeHistoryShadowReport
	rows, err := queryer.Query(query)
	if err != nil {
		return report, err
	}
	defer rows.Close()

	for rows.Next() {
		var rowID int64
		var employeeID int
		var timestampRaw string
		if err := rows.Scan(&rowID, &employeeID, &timestampRaw); err != nil {
			return report, err
		}
		report.Total++

		timestamp, err := time.Parse(time.RFC3339, timestampRaw)
		if err != nil {
			report.InvalidTime++
			continue
		}

		assignment, temporalEmployeeID, status := resolver.resolveAt(employeeID, timestamp)
		switch status {
		case "unmanaged":
			report.Unmanaged++
			continue
		case "interval_missing":
			report.IntervalMissing++
			continue
		case "ambiguous":
			report.Ambiguous++
			continue
		}

		legacyEmployeeID := resolveCanonicalEmployeeID(employeeID, aliases)
		if legacyEmployeeID == temporalEmployeeID {
			report.Matched++
			continue
		}

		report.Mismatched++
		if len(report.MismatchSamples) < 10 {
			report.MismatchSamples = append(report.MismatchSamples, fmt.Sprintf(
				"record_id=%d origine=%d alias=%d storico=%d persona=%s timestamp=%s",
				rowID,
				employeeID,
				legacyEmployeeID,
				temporalEmployeeID,
				assignment.PersonKey,
				timestamp.Format(time.RFC3339),
			))
		}
	}
	if err := rows.Err(); err != nil {
		return report, err
	}

	return report, nil
}
