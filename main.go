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
	Devices                 []SystemDeviceDiagnostics `json:"devices"`
	RecentRaw               []DeviceRawDiagnostic     `json:"recent_raw"`
}

type SystemDeviceDiagnostics struct {
	DeviceID             int        `json:"device_id"`
	IP                   string     `json:"ip"`
	Configured           bool       `json:"configured"`
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

var devices = []AnvizDevice{
	{"192.168.1.245", 1},
	{"192.168.1.246", 2},
}

// handleSyncNow triggers a manual synchronization with all configured Anviz devices
func handleSyncNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	// In a real app, verify admin privileges here
	log.Println("Manuale: Richiesta sincronizzazione Anviz avviata dall'admin...")

	var wg sync.WaitGroup
	var mu sync.Mutex
	results := make(map[string]string)

	for _, d := range devices {
		wg.Add(1)
		go func(ip string, id uint32) {
			defer wg.Done()
			stats, err := syncFromDevice(ip, id)
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

	var data SystemLoginData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Errore payload JSON", http.StatusBadRequest)
		return
	}

	if VerifySystemAdmin(data.Username, data.Password) {
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

	var data SystemChangePasswordData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Errore payload JSON", http.StatusBadRequest)
		return
	}

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

	// Verifica Super Admin (semplificato per ora richiedendo un header o assumendo che la rotta sia protetta a monte)
	// In produzione si userebbe un token JWT o sessione
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

	byDevice := make(map[int]SystemDeviceDiagnostics)
	for _, device := range devices {
		byDevice[int(device.ID)] = SystemDeviceDiagnostics{
			DeviceID:   int(device.ID),
			IP:         device.IP,
			Configured: true,
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
		Devices:                 responseDevices,
		RecentRaw:               recentRaw,
	})
}

// handleEmployees restituisce la lista di tutti i dipendenti (per i filtri dell'admin)
func handleEmployees(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
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

	// Utilizziamo un semplice header Auth per la demo al posto di un JWT lungo
	empIDStr := r.Header.Get("X-Employee-ID")
	pin := r.Header.Get("X-Employee-PIN")

	empID, err := strconv.Atoi(empIDStr)
	if err != nil || !VerifyPIN(empID, pin) {
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

	empIDStr := r.Header.Get("X-Employee-ID")
	pin := r.Header.Get("X-Employee-PIN")

	empID, err := strconv.Atoi(empIDStr)
	if err != nil || !VerifyPIN(empID, pin) {
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

	empIDStr := r.Header.Get("X-Employee-ID")
	pin := r.Header.Get("X-Employee-PIN")

	empID, err := strconv.Atoi(empIDStr)
	if err != nil || !VerifyPIN(empID, pin) {
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

	var data ValidationAction
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Errore payload JSON", http.StatusBadRequest)
		return
	}

	if err := ApproveValidation(data.ID, data.AdminID); err != nil {
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

	var data ValidationAction
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Errore payload JSON", http.StatusBadRequest)
		return
	}

	if err := RejectValidation(data.ID, data.AdminID); err != nil {
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

	var data ManualClockData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		log.Printf("MANUAL CLOCK DECODE ERR: %v", err)
		http.Error(w, "Errore payload JSON", http.StatusBadRequest)
		return
	}
	
	log.Printf("MANUAL CLOCK REQ: %+v", data)

	// Verifica se l'adminId è valido
	if data.AdminID != 0 && !IsEmployeeAdmin(data.AdminID) {
		log.Printf("MANUAL CLOCK UNAUTH: AdminID %d non è admin", data.AdminID)
		http.Error(w, "Privilegi insufficienti", http.StatusUnauthorized)
		return
	}

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
	if GetRecordHasDeviceEquivalent(data.EmployeeID, data.Date, data.Action) {
		log.Printf("MANUAL CLOCK BLOCKED: Dipendente %d ha già una marcatura dispositivo di questo tipo il %s", data.EmployeeID, data.Date)
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

	var data EditManualClockData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		log.Printf("EDIT MANUAL CLOCK DECODE ERR: %v", err)
		http.Error(w, "Errore payload JSON", http.StatusBadRequest)
		return
	}

	log.Printf("EDIT MANUAL CLOCK REQ: %+v", data)

	// Verifica se l'adminId è valido
	if data.AdminID != 0 && !IsEmployeeAdmin(data.AdminID) {
		log.Printf("EDIT MANUAL CLOCK UNAUTH: AdminID %d non è admin", data.AdminID)
		http.Error(w, "Privilegi insufficienti", http.StatusUnauthorized)
		return
	}

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
	if GetRecordHasDeviceEquivalent(record.EmployeeID, data.Date, data.Action) {
		log.Printf("EDIT MANUAL CLOCK BLOCKED: Dipendente %d ha già una marcatura dispositivo il %s", record.EmployeeID, data.Date)
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
		var data struct {
			AdminID     int    `json:"adminId"`
			Date        string `json:"date"`        // YYYY-MM-DD
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			http.Error(w, "Errore payload JSON", http.StatusBadRequest)
			return
		}

		if data.AdminID != 0 && !IsEmployeeAdmin(data.AdminID) {
			http.Error(w, "Privilegi insufficienti", http.StatusUnauthorized)
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
		adminIDStr := r.URL.Query().Get("adminId")
		idStr := r.URL.Query().Get("id")

		adminID, _ := strconv.Atoi(adminIDStr)
		id, _ := strconv.Atoi(idStr)

		if adminID != 0 && !IsEmployeeAdmin(adminID) {
			http.Error(w, "Privilegi insufficienti", http.StatusUnauthorized)
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

	var data struct {
		AdminID int `json:"adminId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Errore payload JSON", http.StatusBadRequest)
		return
	}

	if data.AdminID != 0 && !IsEmployeeAdmin(data.AdminID) {
		http.Error(w, "Privilegi insufficienti", http.StatusUnauthorized)
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

	// 2. Avvia il sistema di backup periodico del database
	StartBackupScheduler()

	// 3. Avvia Goroutine lavoratore in background per Anviz (uno per ogni IP)
	// NOTA: il DeviceID tipicamente di default è 1.	// Avvia i worker TCP per ciascun orologio fisico in Goroutine con DeviceID corretto
	for _, d := range devices {
		go SyncAnvizWorker(d.IP, d.ID)
	}

	// 4. Registrazione API Endpoints
	http.HandleFunc("/api/clock", handleClock)
	http.HandleFunc("/api/login", handleLogin)
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
