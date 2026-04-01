package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Nessuna API Key statica - rimosso segreto hardcoded

// ClockData è la struttura della request in arrivo dal frontend (Web/Browser)
type ClockData struct {
	EmployeeID int      `json:"employeeId"`
	PIN        string   `json:"pin,omitempty"` // Aggiunto per autenticazione
	Action     string   `json:"action"`        // "in" o "out"
	Latitude   *float64 `json:"latitude"`
	Longitude  *float64 `json:"longitude"`
}

// ManualClockData è la struttura per l'inserimento manuale da admin
type ManualClockData struct {
	AdminID    int    `json:"adminId"`
	EmployeeID int    `json:"employeeId"`
	Date       string `json:"date"` // YYYY-MM-DD
	Time       string `json:"time"` // HH:MM
	Action     string `json:"action"`
}

// LoginData è la struttura per la richiesta di Login (Web)
type LoginData struct {
	EmployeeID int    `json:"employeeId"`
	PIN        string `json:"pin"`
}

// SystemLoginData è la struttura per la richiesta di Login di Sistema
type SystemLoginData struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type SystemChangePasswordData struct {
	Username    string `json:"username"`
	NewPassword string `json:"newPassword"`
}

type SystemDeviceDiagnosticsResponse struct {
	LegacyUnassignedRecords int                       `json:"legacy_unassigned_records"`
	ConfiguredDevicesCount  int                       `json:"configured_devices_count"`
	ActiveDevicesCount      int                       `json:"active_devices_count"`
	SyncEnabled             bool                      `json:"sync_enabled"`
	Devices                 []SystemDeviceDiagnostics `json:"devices"`
	RecentRaw               []DeviceRawDiagnostic     `json:"recent_raw"`
	RecentLegacyUnassigned  []DeviceRawDiagnostic     `json:"recent_legacy_unassigned"`
}

type SystemDeviceDiagnostics struct {
	DeviceID             int        `json:"device_id"`
	IP                   string     `json:"ip"`
	Configured           bool       `json:"configured"`
	Active               bool       `json:"active"`
	RawCount             int        `json:"raw_count"`
	FinalCount           int        `json:"final_count"`
	LatestRawTimestamp   *time.Time `json:"latest_raw_timestamp,omitempty"`
	LatestFinalTimestamp *time.Time `json:"latest_final_timestamp,omitempty"`
}

// Device info for synchronization
type AnvizDevice struct {
	IP string
	ID uint32
}

var (
	configuredDevices []AnvizDevice
	activeDevices     []AnvizDevice
)

func anvizSyncEnabled() bool {
	value := strings.TrimSpace(os.Getenv("ANVIZ_SYNC_ENABLED"))
	if value == "" {
		return true
	}

	return value != "0" && !strings.EqualFold(value, "false")
}

func parseAnvizDeviceEntry(entry string) (AnvizDevice, error) {
	parts := strings.SplitN(strings.TrimSpace(entry), "@", 2)
	if len(parts) != 2 {
		parts = strings.SplitN(strings.TrimSpace(entry), "=", 2)
	}
	if len(parts) != 2 {
		return AnvizDevice{}, fmt.Errorf("formato non valido")
	}

	idValue := strings.TrimSpace(parts[0])
	ipValue := strings.TrimSpace(parts[1])
	if idValue == "" || ipValue == "" {
		return AnvizDevice{}, fmt.Errorf("id o ip mancanti")
	}

	parsedID, err := strconv.ParseUint(idValue, 10, 32)
	if err != nil {
		return AnvizDevice{}, fmt.Errorf("device id non valido: %w", err)
	}

	return AnvizDevice{
		IP: ipValue,
		ID: uint32(parsedID),
	}, nil
}

func loadConfiguredAnvizDevices() []AnvizDevice {
	raw := strings.TrimSpace(os.Getenv("ANVIZ_DEVICES"))
	if raw == "" {
		log.Println("ANVIZ_DEVICES non impostato: nessun device configurato")
		return nil
	}

	entries := strings.Split(raw, ",")
	devices := make([]AnvizDevice, 0, len(entries))
	seen := make(map[uint32]struct{})

	for _, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}

		device, err := parseAnvizDeviceEntry(trimmed)
		if err != nil {
			log.Printf("Configurazione ANVIZ_DEVICES ignorata per entry %q: %v", trimmed, err)
			continue
		}

		if _, exists := seen[device.ID]; exists {
			log.Printf("Configurazione ANVIZ_DEVICES duplicata per device id=%d, entry %q ignorata", device.ID, trimmed)
			continue
		}

		seen[device.ID] = struct{}{}
		devices = append(devices, device)
	}

	if len(devices) == 0 {
		log.Println("ANVIZ_DEVICES impostato ma nessun device valido configurato")
	}

	return devices
}

func selectActiveAnvizDevices(devices []AnvizDevice) []AnvizDevice {
	if len(devices) == 0 {
		return nil
	}

	configured := strings.TrimSpace(os.Getenv("ANVIZ_ACTIVE_DEVICE_IDS"))
	if configured == "" {
		selected := make([]AnvizDevice, len(devices))
		copy(selected, devices)
		return selected
	}

	selectedIDs := make(map[uint32]struct{})
	for _, token := range strings.Split(configured, ",") {
		value := strings.TrimSpace(token)
		if value == "" {
			continue
		}
		parsed, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			log.Printf("Configurazione ANVIZ_ACTIVE_DEVICE_IDS ignorata per token %q: %v", value, err)
			continue
		}
		selectedIDs[uint32(parsed)] = struct{}{}
	}

	filtered := make([]AnvizDevice, 0, len(devices))
	for _, device := range devices {
		if _, ok := selectedIDs[device.ID]; ok {
			filtered = append(filtered, device)
		}
	}

	if len(filtered) == 0 {
		log.Printf("ANVIZ_ACTIVE_DEVICE_IDS=%q non corrisponde a nessun device configurato: nessun device attivo", configured)
		return nil
	}

	return filtered
}

func initAnvizDeviceConfig() {
	configuredDevices = loadConfiguredAnvizDevices()
	activeDevices = selectActiveAnvizDevices(configuredDevices)

	if len(configuredDevices) == 0 {
		log.Println("Configurazione device Anviz assente: la sincronizzazione restera inattiva fino a configurazione completata")
		return
	}

	if len(activeDevices) == 0 {
		log.Printf("Configurazione device Anviz caricata (%d device), ma nessun device e attivo per la sync", len(configuredDevices))
		return
	}

	log.Printf("Configurazione device Anviz caricata: configurati=%d attivi=%d", len(configuredDevices), len(activeDevices))
}

func configuredAnvizDevices() []AnvizDevice {
	result := make([]AnvizDevice, len(configuredDevices))
	copy(result, configuredDevices)
	return result
}

func activeAnvizDevices() []AnvizDevice {
	result := make([]AnvizDevice, len(activeDevices))
	copy(result, activeDevices)
	return result
}

// handleSyncNow triggers a manual synchronization with all configured Anviz devices
func handleSyncNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	if _, ok := requireAdminSession(w, r); !ok {
		return
	}

	log.Println("Manuale: Richiesta sincronizzazione Anviz avviata dall'admin...")

	devices := activeAnvizDevices()
	if len(devices) == 0 {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "warning",
			"message": "Nessun device Anviz attivo configurato",
			"results": map[string]string{},
			"success": false,
		})
		return
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	results := make(map[string]string)

	for _, d := range devices {
		wg.Add(1)
		go func(ip string, id uint32) {
			defer wg.Done()
			stats, err := syncFromDevice(ip, id, true)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				results[ip] = "ERRORE: " + err.Error()
			} else {
				results[ip] = fmt.Sprintf("OK (ricevuti=%d, nuovi=%d, duplicati=%d, errori=%d)", stats.Received, stats.Inserted, stats.Duplicates, stats.Errors)
			}
		}(d.IP, d.ID)
	}
	wg.Wait()

	// Build a summary message
	summary := "Esito sincronizzazione:\n"
	successCount := 0
	for ip, status := range results {
		summary += fmt.Sprintf("- %s: %s\n", ip, status)
		if strings.HasPrefix(status, "OK") {
			successCount++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": summary,
		"results": results,
		"success": successCount == len(devices),
	})
}

// handleClock gestire le TIMBRATURE via Web (Frontend)
func handleClock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	var data ClockData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Errore decodifica JSON", http.StatusBadRequest)
		return
	}

	// Simple check, in un caso reale il PIN viaggia criptato o in token JWT
	if !VerifyPIN(data.EmployeeID, data.PIN) {
		http.Error(w, "Credenziali o PIN errati", http.StatusUnauthorized)
		return
	}

	// Per le timbrature da web, mappiamo "in"->0, "out"->1
	statusCode := 0
	if data.Action == "out" {
		statusCode = 1
	}

	employeeName := GetEmployeeName(data.EmployeeID)
	if employeeName == "" {
		employeeName = fmt.Sprintf("Utente %d", data.EmployeeID)
	}

	err := InsertPendingValidation(data.EmployeeID, employeeName, time.Now(), data.Action, statusCode, data.Latitude, data.Longitude)
	if err != nil {
		log.Printf("Errore creazione richiesta validazione (Web): %v", err)
		http.Error(w, "Errore salvataggio nel database", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "pending",
		"message": "Richiesta di validazione inviata. In attesa di approvazione admin.",
	})
}

// handleAttendances espone i dati salvati (web e device) per il gestionale esterno
func handleAttendances(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	if _, ok := requireAdminSession(w, r); !ok {
		return
	}

	// Parametri di filtro opzionali dal Gestionale Esterno o Admin Dashboard
	start := r.URL.Query().Get("start_date")
	end := r.URL.Query().Get("end_date")
	empID := r.URL.Query().Get("employee_id")

	records, err := GetRecords(start, end, empID)
	if err != nil {
		log.Printf("Errore lettura presenze da DB: %v", err)
		http.Error(w, "Errore estrazione dati SQLite", http.StatusInternalServerError)
		return
	}

	// Evita nil reference convertendo l'array in lista vuota se è nil
	if records == nil {
		records = []Record{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(records)
}

// handleLogin gestisce l'autenticazione dal frontend
func handleLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	var data LoginData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Errore payload JSON", http.StatusBadRequest)
		return
	}

	log.Printf("[DEBUG] Tentativo login ID: %d", data.EmployeeID)
	if VerifyPIN(data.EmployeeID, data.PIN) {
		if err := createEmployeeSession(w, data.EmployeeID); err != nil {
			log.Printf("[ERROR] Creazione sessione dipendente fallita: %v", err)
			http.Error(w, "Errore creazione sessione", http.StatusInternalServerError)
			return
		}

		name := GetEmployeeName(data.EmployeeID)
		isAdmin := IsEmployeeAdmin(data.EmployeeID)
		log.Printf("[DEBUG] Login riuscito: %s (Admin: %v)", name, isAdmin)
		response := map[string]interface{}{"success": true, "name": name, "isAdmin": isAdmin}
		json.NewEncoder(w).Encode(response)
	} else {
		log.Printf("[DEBUG] Login fallito per ID: %d (PIN errato o DB busy)", data.EmployeeID)
		http.Error(w, "PIN errato", http.StatusUnauthorized)
	}
}

// handleAdminLogin gestisce l'autenticazione per l'area admin
func handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	var data LoginData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Errore payload JSON", http.StatusBadRequest)
		return
	}

	if VerifyAdminPIN(data.EmployeeID, data.PIN) {
		if err := createEmployeeSession(w, data.EmployeeID); err != nil {
			log.Printf("[ERROR] Creazione sessione admin fallita: %v", err)
			http.Error(w, "Errore creazione sessione", http.StatusInternalServerError)
			return
		}

		name := GetEmployeeName(data.EmployeeID)
		// Ritorna esito positivo
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"name":    name,
		})
	} else {
		http.Error(w, "Credenziali non valide o privilegi insufficienti", http.StatusUnauthorized)
	}
}

// handleSystemLogin gestisce l'autenticazione per l'amministratore di sistema
func handleSystemLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	const fixedSystemAdminUsername = "admin"

	var data SystemLoginData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Errore payload JSON", http.StatusBadRequest)
		return
	}
	data.Password = strings.TrimSpace(data.Password)
	data.Username = fixedSystemAdminUsername
	if data.Password == "" {
		http.Error(w, "Password obbligatoria", http.StatusBadRequest)
		return
	}

	if VerifySystemAdmin(data.Username, data.Password) {
		if err := createSystemAdminSession(w, data.Username); err != nil {
			log.Printf("[ERROR] Creazione sessione system admin fallita: %v", err)
			http.Error(w, "Errore creazione sessione", http.StatusInternalServerError)
			return
		}

		log.Printf("[DEBUG] Login System Admin riuscito: %s", data.Username)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"name":    data.Username,
		})
	} else {
		log.Printf("[DEBUG] Login System Admin fallito per: %s", data.Username)
		http.Error(w, "Credenziali non valide", http.StatusUnauthorized)
	}
}

// handleSystemChangePassword permette al Super Admin di cambiare la propria password
func handleSystemChangePassword(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	session, ok := requireSystemSession(w, r)
	if !ok {
		return
	}

	var data SystemChangePasswordData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Errore payload JSON", http.StatusBadRequest)
		return
	}

	data.Username = session.Username

	if data.Username == "" || data.NewPassword == "" {
		http.Error(w, "Dati mancanti", http.StatusBadRequest)
		return
	}

	err := UpdateSystemAdminPassword(data.Username, data.NewPassword)
	if err != nil {
		log.Printf("[ERROR] Errore cambio password per %s: %v", data.Username, err)
		http.Error(w, "Errore durante l'aggiornamento della password", http.StatusInternalServerError)
		return
	}

	log.Printf("[INFO] Password aggiornata con successo per System Admin: %s", data.Username)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Password aggiornata con successo",
	})
}

// handleSystemEmployees restituisce tutti i dipendenti con lo stato admin (solo Super Admin)
func handleSystemEmployees(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	if _, ok := requireSystemSession(w, r); !ok {
		return
	}

	rows, err := DB.Query("SELECT id, name, is_admin FROM employees ORDER BY id ASC")
	if err != nil {
		http.Error(w, "Errore database", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type EmpStatus struct {
		ID      int    `json:"id"`
		Name    string `json:"name"`
		IsAdmin bool   `json:"isAdmin"`
	}
	var employees []EmpStatus
	for rows.Next() {
		var e EmpStatus
		var isAdmin int
		if err := rows.Scan(&e.ID, &e.Name, &isAdmin); err == nil {
			e.IsAdmin = isAdmin == 1
			employees = append(employees, e)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(employees)
}

// handleSystemToggleAdmin abilita/disabilita i privilegi di admin per un dipendente (solo Super Admin)
func handleSystemToggleAdmin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	if _, ok := requireSystemSession(w, r); !ok {
		return
	}

	var data struct {
		EmployeeID int  `json:"employeeId"`
		IsAdmin    bool `json:"isAdmin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Errore payload JSON", http.StatusBadRequest)
		return
	}

	status := 0
	if data.IsAdmin {
		status = 1
	}

	_, err := DB.Exec("UPDATE employees SET is_admin = ? WHERE id = ?", status, data.EmployeeID)
	if err != nil {
		http.Error(w, "Errore aggiornamento database", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleSystemDeviceDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	if _, ok := requireSystemSession(w, r); !ok {
		return
	}

	aggregates, err := GetDeviceAggregates()
	if err != nil {
		log.Printf("Errore lettura diagnostica device: %v", err)
		http.Error(w, "Errore estrazione diagnostica device", http.StatusInternalServerError)
		return
	}

	legacyCount, err := GetLegacyUnassignedDeviceRecordCount()
	if err != nil {
		log.Printf("Errore conteggio record legacy device: %v", err)
		http.Error(w, "Errore conteggio record legacy", http.StatusInternalServerError)
		return
	}

	recentRaw, err := GetRecentDeviceRawRecords(25)
	if err != nil {
		log.Printf("Errore lettura raw records device: %v", err)
		http.Error(w, "Errore estrazione raw records device", http.StatusInternalServerError)
		return
	}

	recentLegacyUnassigned, err := GetRecentLegacyUnassignedDeviceRecords(25)
	if err != nil {
		log.Printf("Errore lettura record device legacy senza raw: %v", err)
		http.Error(w, "Errore estrazione record device legacy", http.StatusInternalServerError)
		return
	}

	byDevice := make(map[int]SystemDeviceDiagnostics)
	activeDeviceSet := make(map[int]struct{})
	for _, device := range activeAnvizDevices() {
		activeDeviceSet[int(device.ID)] = struct{}{}
	}

	for _, device := range configuredAnvizDevices() {
		_, isActive := activeDeviceSet[int(device.ID)]
		byDevice[int(device.ID)] = SystemDeviceDiagnostics{
			DeviceID:   int(device.ID),
			IP:         device.IP,
			Configured: true,
			Active:     isActive,
		}
	}

	for _, aggregate := range aggregates {
		entry := byDevice[aggregate.DeviceID]
		entry.DeviceID = aggregate.DeviceID
		entry.RawCount = aggregate.RawCount
		entry.FinalCount = aggregate.FinalCount
		entry.LatestRawTimestamp = aggregate.LatestRawTimestamp
		entry.LatestFinalTimestamp = aggregate.LatestFinalTimestamp
		byDevice[aggregate.DeviceID] = entry
	}

	responseDevices := make([]SystemDeviceDiagnostics, 0, len(byDevice))
	for _, entry := range byDevice {
		responseDevices = append(responseDevices, entry)
	}

	sort.Slice(responseDevices, func(i, j int) bool {
		return responseDevices[i].DeviceID < responseDevices[j].DeviceID
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SystemDeviceDiagnosticsResponse{
		LegacyUnassignedRecords: legacyCount,
		ConfiguredDevicesCount:  len(configuredAnvizDevices()),
		ActiveDevicesCount:      len(activeAnvizDevices()),
		SyncEnabled:             anvizSyncEnabled(),
		Devices:                 responseDevices,
		RecentRaw:               recentRaw,
		RecentLegacyUnassigned:  recentLegacyUnassigned,
	})
}

func handleSystemBootstrapStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	status, err := GetSystemAdminBootstrapStatus()
	if err != nil {
		log.Printf("Errore lettura stato bootstrap system admin: %v", err)
		http.Error(w, "Errore lettura stato bootstrap", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

// handleEmployees restituisce la lista di tutti i dipendenti (per i filtri dell'admin)
func handleEmployees(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	if _, ok := requireAdminSession(w, r); !ok {
		return
	}

	employees, err := GetAllEmployees()
	if err != nil {
		log.Printf("Errore lettura dipendenti: %v", err)
		http.Error(w, "Errore estrazione dati SQLite", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(employees)
}

// handleEmployeeAttendances legge lo storico presenze per un solo dipendente (Dashboard Web)
func handleEmployeeAttendances(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	empID, ok := authenticatedEmployeeID(r)
	if !ok {
		http.Error(w, "Non Autenticato", http.StatusUnauthorized)
		return
	}

	limit := 10 // Paginazione base
	records, err := GetEmployeeRecords(empID, limit)
	if err != nil {
		http.Error(w, "Errore lettura records", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(records)
}

// handleEmployeeMonthlyAttendances restituisce tutti i record di un dipendente per un dato mese
func handleEmployeeMonthlyAttendances(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	empID, ok := authenticatedEmployeeID(r)
	if !ok {
		http.Error(w, "Non Autenticato", http.StatusUnauthorized)
		return
	}

	monthParam := r.URL.Query().Get("month") // formato: "2026-02"
	if monthParam == "" {
		// Default al mese corrente
		now := time.Now()
		monthParam = now.Format("2006-01")
	}

	parsed, err := time.Parse("2006-01", monthParam)
	if err != nil {
		http.Error(w, "Formato mese non valido. Usa YYYY-MM", http.StatusBadRequest)
		return
	}

	records, err := GetEmployeeMonthlyRecords(empID, parsed.Year(), int(parsed.Month()))
	if err != nil {
		http.Error(w, "Errore lettura records mensili", http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []Record{}
	}

	json.NewEncoder(w).Encode(records)
}

// handleEmployeeRangeAttendances restituisce tutti i record di un dipendente in un range di date
func handleEmployeeRangeAttendances(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	empID, ok := authenticatedEmployeeID(r)
	if !ok {
		http.Error(w, "Non Autenticato", http.StatusUnauthorized)
		return
	}

	startDate := r.URL.Query().Get("start_date")
	endDate := r.URL.Query().Get("end_date")

	if startDate == "" || endDate == "" {
		http.Error(w, "start_date e end_date sono richiesti", http.StatusBadRequest)
		return
	}

	records, err := GetEmployeeRangeRecords(empID, startDate, endDate)
	if err != nil {
		http.Error(w, "Errore lettura records range", http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []Record{}
	}

	json.NewEncoder(w).Encode(records)
}

// handlePendingValidations restituisce le richieste di validazione pendenti (o tutte se status non specificato)
func handlePendingValidations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	if _, ok := requireAdminSession(w, r); !ok {
		return
	}

	status := r.URL.Query().Get("status") // "pending", "approved", "rejected", o vuoto per tutte
	validations, err := GetPendingValidations(status)
	if err != nil {
		log.Printf("Errore lettura validazioni: %v", err)
		http.Error(w, "Errore estrazione dati", http.StatusInternalServerError)
		return
	}

	if validations == nil {
		validations = []PendingValidation{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(validations)
}

// ValidationAction è la struttura per approvare/rifiutare una validazione
type ValidationAction struct {
	ID      int `json:"id"`
	AdminID int `json:"adminId"`
}

// handleApproveValidation approva una richiesta di validazione
func handleApproveValidation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	session, ok := requireAdminSession(w, r)
	if !ok {
		return
	}

	var data ValidationAction
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Errore payload JSON", http.StatusBadRequest)
		return
	}

	if err := ApproveValidation(data.ID, sessionActorAdminID(session)); err != nil {
		log.Printf("Errore approvazione validazione %d: %v", data.ID, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "approved", "message": "Marcatura approvata e registrata"})
}

// handleRejectValidation rifiuta una richiesta di validazione
func handleRejectValidation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	session, ok := requireAdminSession(w, r)
	if !ok {
		return
	}

	var data ValidationAction
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Errore payload JSON", http.StatusBadRequest)
		return
	}

	if err := RejectValidation(data.ID, sessionActorAdminID(session)); err != nil {
		log.Printf("Errore rifiuto validazione %d: %v", data.ID, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "rejected", "message": "Marcatura rifiutata"})
}

// handleAdminManualClock gestisce l'inserimento manuale di una marcatura da parte dell'admin
func handleAdminManualClock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	if _, ok := requireAdminSession(w, r); !ok {
		return
	}

	var data ManualClockData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		log.Printf("MANUAL CLOCK DECODE ERR: %v", err)
		http.Error(w, "Errore payload JSON", http.StatusBadRequest)
		return
	}

	log.Printf("MANUAL CLOCK REQ: %+v", data)

	// Parse date and time
	dateTimeStr := fmt.Sprintf("%s %s:00", data.Date, data.Time)
	timestamp, err := time.ParseInLocation("2006-01-02 15:04:05", dateTimeStr, time.Local)
	if err != nil {
		log.Printf("MANUAL CLOCK TIME PARSE ERR: %v", err)
		http.Error(w, "Formato data/ora non valido", http.StatusBadRequest)
		return
	}

	statusCode := 0
	if data.Action == "Out" || data.Action == "out" || data.Action == "F_pausa" || data.Action == "R_trasf" || data.Action == "uscita" || data.Action == "fine_pausa" || data.Action == "ritorno_trasferta" {
		statusCode = 1
	}

	// Controllo duplicato dal dispositivo terminale
	if GetRecordHasDeviceEquivalent(data.EmployeeID, timestamp, data.Action) {
		log.Printf("MANUAL CLOCK BLOCKED: Dipendente %d ha già una marcatura dispositivo equivalente a %s", data.EmployeeID, timestamp.Format(time.RFC3339))
		http.Error(w, "Esiste già una marcatura proveniente dal dispositivo per questa azione nella data indicata", http.StatusBadRequest)
		return
	}

	employeeName := GetEmployeeName(data.EmployeeID)
	if employeeName == "" {
		employeeName = fmt.Sprintf("Utente %d", data.EmployeeID)
	}

	log.Printf("MANUAL CLOCK: going to InsertRecord(empId=%d, name=%s, ts=%v, action=%s, sc=%d)", data.EmployeeID, employeeName, timestamp, data.Action, statusCode)

	inserted, err := InsertRecord(data.EmployeeID, employeeName, timestamp, data.Action, statusCode, "manual_web", nil, nil)
	if err == ErrDuplicateRecord {
		http.Error(w, "Esiste gia una marcatura web/manuale con gli stessi dati", http.StatusConflict)
		return
	}
	if err != nil {
		log.Printf("Errore inserimento marcatura manuale: %v", err)
		http.Error(w, "Errore salvataggio nel database", http.StatusInternalServerError)
		return
	}
	if !inserted {
		http.Error(w, "Esiste gia una marcatura web/manuale con gli stessi dati", http.StatusConflict)
		return
	}

	log.Println("MANUAL CLOCK SUCCESS")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Marcatura manuale inserita con successo",
	})
}

// EditManualClockData struttura per aggiornare una marcatura esistente
type EditManualClockData struct {
	AdminID int    `json:"adminId"`
	ID      int    `json:"id"`
	Date    string `json:"date"`
	Time    string `json:"time"`
	Action  string `json:"action"`
}

// handleEditAdminManualClock modifica una marcatura ESCLUSIVAMENTE manual_web
func handleEditAdminManualClock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	if _, ok := requireAdminSession(w, r); !ok {
		return
	}

	var data EditManualClockData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		log.Printf("EDIT MANUAL CLOCK DECODE ERR: %v", err)
		http.Error(w, "Errore payload JSON", http.StatusBadRequest)
		return
	}

	log.Printf("EDIT MANUAL CLOCK REQ: %+v", data)

	record, err := GetRecordByID(data.ID)
	if err != nil {
		log.Printf("EDIT MANUAL CLOCK NOT FOUND: ID %d", data.ID)
		http.Error(w, "Record non trovato", http.StatusNotFound)
		return
	}

	if record.Source != "manual_web" {
		log.Printf("EDIT MANUAL CLOCK FORBIDDEN SOURCE: ID %d source %s", data.ID, record.Source)
		http.Error(w, "Solo le marcature inserite manualmente (dal web) possono essere modificate", http.StatusForbidden)
		return
	}

	// Parse date and time
	dateTimeStr := fmt.Sprintf("%s %s:00", data.Date, data.Time)
	timestamp, err := time.ParseInLocation("2006-01-02 15:04:05", dateTimeStr, time.Local)
	if err != nil {
		log.Printf("EDIT MANUAL CLOCK TIME PARSE ERR: %v", err)
		http.Error(w, "Formato data/ora non valido", http.StatusBadRequest)
		return
	}

	statusCode := 0
	if data.Action == "Out" || data.Action == "out" || data.Action == "F_pausa" || data.Action == "R_trasf" || data.Action == "uscita" || data.Action == "fine_pausa" || data.Action == "ritorno_trasferta" {
		statusCode = 1
	}

	// Controllo duplicato dal dispositivo terminale (per il giorno impostato)
	// Essendo un edit, se il record fosse manual_web non c'è rischio di collisione con se stesso,
	// ma la GetRecordHasDeviceEquivalent verifica i log con source = 'device' e questo va bene.
	if GetRecordHasDeviceEquivalent(record.EmployeeID, timestamp, data.Action) {
		log.Printf("EDIT MANUAL CLOCK BLOCKED: Dipendente %d ha già una marcatura dispositivo equivalente a %s", record.EmployeeID, timestamp.Format(time.RFC3339))
		http.Error(w, "Esiste già una marcatura proveniente dal dispositivo per questa azione nella data indicata", http.StatusBadRequest)
		return
	}

	err = UpdateManualRecord(data.ID, timestamp, data.Action, statusCode)
	if err != nil {
		log.Printf("Errore aggiornamento marcatura manuale ID %d: %v", data.ID, err)
		http.Error(w, "Errore aggiornamento nel database", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Marcatura manuale modificata con successo",
	})
}

// handleCustomHolidays gestisce le festività aziendali personalizzate (GET, POST, DELETE)
func handleCustomHolidays(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		holidays, err := GetCustomHolidays()
		if err != nil {
			log.Printf("Errore lettura festività personalizzate: %v", err)
			http.Error(w, "Errore estrazione dati", http.StatusInternalServerError)
			return
		}
		if holidays == nil {
			holidays = []CustomHoliday{}
		}
		json.NewEncoder(w).Encode(holidays)

	case http.MethodPost:
		if _, ok := requireAdminSession(w, r); !ok {
			return
		}

		var data struct {
			AdminID     int    `json:"adminId"`
			Date        string `json:"date"` // YYYY-MM-DD
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			http.Error(w, "Errore payload JSON", http.StatusBadRequest)
			return
		}

		if data.Date == "" {
			http.Error(w, "La data è obbligatoria", http.StatusBadRequest)
			return
		}

		if err := AddCustomHoliday(data.Date, data.Description); err != nil {
			log.Printf("Errore inserimento festività personalizzata: %v", err)
			http.Error(w, "Errore salvataggio nel database", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"status": "success", "message": "Festività aggiunta"})

	case http.MethodDelete:
		idStr := r.URL.Query().Get("id")
		id, _ := strconv.Atoi(idStr)

		if _, ok := requireAdminSession(w, r); !ok {
			return
		}

		if err := DeleteCustomHoliday(id); err != nil {
			log.Printf("Errore eliminazione festività personalizzata: %v", err)
			http.Error(w, "Errore eliminazione dal database", http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(map[string]string{"status": "success", "message": "Festività eliminata"})

	default:
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
	}
}

// handleAdminBackup esegue un backup manuale del database (solo admin)
func handleAdminBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	if _, ok := requireAdminSession(w, r); !ok {
		return
	}

	backupFile, err := PerformBackup()
	if err != nil {
		log.Printf("Errore backup manuale: %v", err)
		http.Error(w, "Errore durante il backup: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": "Backup completato con successo",
		"file":    filepath.Base(backupFile),
	})
}

// handleAdminBackupList restituisce la lista dei backup disponibili
func handleAdminBackupList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	if _, ok := requireAdminSession(w, r); !ok {
		return
	}

	backups, err := GetBackupList()
	if err != nil {
		log.Printf("Errore lettura lista backup: %v", err)
		http.Error(w, "Errore estrazione dati", http.StatusInternalServerError)
		return
	}

	if backups == nil {
		backups = []BackupInfo{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(backups)
}

// handleAdminBackupDownload permette di scaricare un file di backup specifico
func handleAdminBackupDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	if _, ok := requireAdminSession(w, r); !ok {
		return
	}

	filename := r.URL.Query().Get("file")
	if filename == "" {
		http.Error(w, "Parametro 'file' richiesto", http.StatusBadRequest)
		return
	}

	// Sicurezza: previeni path traversal
	cleanName := filepath.Base(filename)
	if cleanName != filename || cleanName == "." || cleanName == ".." {
		http.Error(w, "Nome file non valido", http.StatusBadRequest)
		return
	}

	config := getBackupConfig()
	backupPath := filepath.Join(config.BackupDir, cleanName)

	// Verifica che il file esista
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		http.Error(w, "Backup non trovato", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", cleanName))
	w.Header().Set("Content-Type", "application/x-sqlite3")
	http.ServeFile(w, r, backupPath)
}

func main() {
	log.Println("Avvio Time & Attendance Microservice...")

	// 1. Inizializzazione Database
	InitDB()

	// 1b. Carica la configurazione device della sede dal runtime locale
	initAnvizDeviceConfig()

	// 2. Avvia il sistema di backup periodico del database
	StartBackupScheduler()

	// 3. Avvia Goroutine lavoratore in background per Anviz (uno per ogni IP)
	// NOTA: il DeviceID tipicamente di default è 1.	// Avvia i worker TCP per ciascun orologio fisico in Goroutine con DeviceID corretto
	syncEnabled := anvizSyncEnabled()

	if syncEnabled {
		devices := activeAnvizDevices()
		if len(devices) == 0 {
			log.Println("Sync Anviz abilitata ma nessun device attivo configurato: nessun worker avviato")
		}
		for _, d := range devices {
			go SyncAnvizWorker(d.IP, d.ID)
		}
	} else {
		log.Println("Anviz background sync disabilitato da ANVIZ_SYNC_ENABLED")
	}

	// 4. Registrazione API Endpoints
	http.HandleFunc("/api/clock", handleClock)
	http.HandleFunc("/api/login", handleLogin)
	http.HandleFunc("/api/auth/logout", handleLogout)
	http.HandleFunc("/api/auth/session", handleSessionInfo)
	http.HandleFunc("/api/admin/login", handleAdminLogin)
	http.HandleFunc("/api/employee/attendances", handleEmployeeAttendances)
	http.HandleFunc("/api/employee/monthly-attendances", handleEmployeeMonthlyAttendances)
	http.HandleFunc("/api/employee/range-attendances", handleEmployeeRangeAttendances)
	http.HandleFunc("/api/attendances", handleAttendances)
	http.HandleFunc("/api/employees", handleEmployees)
	http.HandleFunc("/api/admin/pending-validations", handlePendingValidations)
	http.HandleFunc("/api/admin/approve-validation", handleApproveValidation)
	http.HandleFunc("/api/admin/reject-validation", handleRejectValidation)
	http.HandleFunc("/api/admin/manual-clock", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			handleAdminManualClock(w, r)
		} else if r.Method == http.MethodPut {
			handleEditAdminManualClock(w, r)
		} else {
			http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		}
	})
	http.HandleFunc("/api/custom-holidays", handleCustomHolidays)
	http.HandleFunc("/api/admin/sync-now", handleSyncNow)
	http.HandleFunc("/api/admin/backup", handleAdminBackup)
	http.HandleFunc("/api/admin/backups", handleAdminBackupList)
	http.HandleFunc("/api/admin/backup/download", handleAdminBackupDownload)
	http.HandleFunc("/api/system/login", handleSystemLogin)
	http.HandleFunc("/api/system/employees", handleSystemEmployees)
	http.HandleFunc("/api/system/toggle-admin", handleSystemToggleAdmin)
	http.HandleFunc("/api/system/change-password", handleSystemChangePassword)
	http.HandleFunc("/api/system/device-diagnostics", handleSystemDeviceDiagnostics)
	http.HandleFunc("/api/system/bootstrap-status", handleSystemBootstrapStatus)

	// Servire dashboard admin
	http.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "admin.html")
	})

	// Servire dashboard system admin
	http.HandleFunc("/system-admin", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "system_admin.html")
	})

	// Servire i file CSS
	http.HandleFunc("/admin.css", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "admin.css")
	})
	http.HandleFunc("/index.css", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "index.css")
	})
	http.HandleFunc("/system_admin.css", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "system_admin.css")
	})

	// Servire il frontend index.html
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "index.html")
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Server in ascolto sulla porta %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}
