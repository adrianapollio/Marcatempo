package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const adminTokenTTL = 12 * time.Hour

type contextKey string

const adminIDContextKey contextKey = "adminID"

var adminTokenSecret []byte

// ClockData è la struttura della request in arrivo dal frontend (Web/Browser)
type ClockData struct {
	EmployeeID int      `json:"employeeId"`
	PIN        string   `json:"pin,omitempty"` // Aggiunto per autenticazione
	Action     string   `json:"action"`        // "in" o "out"
	Latitude   *float64 `json:"latitude"`
	Longitude  *float64 `json:"longitude"`
}

// LoginData è la struttura per la richiesta di Login (Web)
type LoginData struct {
	EmployeeID int    `json:"employeeId"`
	PIN        string `json:"pin"`
}

func normalizeClockAction(action string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "in":
		return "in", true
	case "out":
		return "out", true
	default:
		return "", false
	}
}

func initAdminTokenSecret() {
	secret := strings.TrimSpace(os.Getenv("ADMIN_TOKEN_SECRET"))
	if secret != "" {
		adminTokenSecret = []byte(secret)
		return
	}

	generated := make([]byte, 32)
	if _, err := rand.Read(generated); err != nil {
		log.Fatalf("Impossibile inizializzare il segreto dei token admin: %v", err)
	}
	adminTokenSecret = generated
	log.Println("ADMIN_TOKEN_SECRET non configurato: uso un segreto volatile generato all'avvio")
}

func issueAdminToken(adminID int) (string, error) {
	if adminID <= 0 {
		return "", fmt.Errorf("admin non valido")
	}

	expiresAt := time.Now().Add(adminTokenTTL).Unix()
	payload := fmt.Sprintf("%d:%d", adminID, expiresAt)
	mac := hmac.New(sha256.New, adminTokenSecret)
	if _, err := mac.Write([]byte(payload)); err != nil {
		return "", err
	}
	signature := mac.Sum(nil)

	encodedPayload := base64.RawURLEncoding.EncodeToString([]byte(payload))
	encodedSignature := base64.RawURLEncoding.EncodeToString(signature)
	return encodedPayload + "." + encodedSignature, nil
}

func parseAdminToken(token string) (int, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return 0, fmt.Errorf("token non valido")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return 0, fmt.Errorf("payload token non valido")
	}
	signatureBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, fmt.Errorf("firma token non valida")
	}

	mac := hmac.New(sha256.New, adminTokenSecret)
	if _, err := mac.Write(payloadBytes); err != nil {
		return 0, err
	}
	if !hmac.Equal(signatureBytes, mac.Sum(nil)) {
		return 0, fmt.Errorf("firma token non valida")
	}

	var adminID int
	var expiresAt int64
	if _, err := fmt.Sscanf(string(payloadBytes), "%d:%d", &adminID, &expiresAt); err != nil {
		return 0, fmt.Errorf("contenuto token non valido")
	}
	if adminID <= 0 {
		return 0, fmt.Errorf("admin non valido")
	}
	if time.Now().Unix() > expiresAt {
		return 0, fmt.Errorf("token admin scaduto")
	}
	if !IsEmployeeAdmin(adminID) {
		return 0, fmt.Errorf("admin non autorizzato")
	}

	return adminID, nil
}

func adminIDFromRequest(r *http.Request) (int, bool) {
	adminID, ok := r.Context().Value(adminIDContextKey).(int)
	return adminID, ok
}

// AuthMiddleware controlla la presenza del Bearer token corretto
func AuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, "Non autorizzato - Bearer token mancante o errato", http.StatusUnauthorized)
			return
		}

		adminID, err := parseAdminToken(strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer ")))
		if err != nil {
			http.Error(w, "Non autorizzato - token admin non valido o scaduto", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), adminIDContextKey, adminID)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
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

	normalizedAction, ok := normalizeClockAction(data.Action)
	if !ok {
		http.Error(w, "Azione non valida: usa 'in' o 'out'", http.StatusBadRequest)
		return
	}
	data.Action = normalizedAction

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
		if errors.Is(err, ErrDuplicatePendingValidation) {
			http.Error(w, "Esiste gia una marcatura web recente uguale o una richiesta ancora in attesa", http.StatusConflict)
			return
		}
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

	if VerifyPIN(data.EmployeeID, data.PIN) {
		name := GetEmployeeName(data.EmployeeID)
		isAdmin := IsEmployeeAdmin(data.EmployeeID)
		response := map[string]interface{}{"success": true, "name": name, "isAdmin": isAdmin}
		// Se è admin, includi un token firmato per accesso alle API protette.
		if isAdmin {
			token, err := issueAdminToken(data.EmployeeID)
			if err != nil {
				log.Printf("Errore generazione token admin per %d: %v", data.EmployeeID, err)
				http.Error(w, "Errore generazione token admin", http.StatusInternalServerError)
				return
			}
			response["apiKey"] = token
			response["token"] = token
		}
		json.NewEncoder(w).Encode(response)
	} else {
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
		token, err := issueAdminToken(data.EmployeeID)
		if err != nil {
			log.Printf("Errore generazione token admin per %d: %v", data.EmployeeID, err)
			http.Error(w, "Errore generazione token admin", http.StatusInternalServerError)
			return
		}
		// Ritorna il token da utilizzare come Bearer token per le successive chiamate admin.
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"name":    name,
			"apiKey":  token,
			"token":   token,
		})
	} else {
		http.Error(w, "Credenziali non valide o privilegi insufficienti", http.StatusUnauthorized)
	}
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
	ID int `json:"id"`
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

	adminID, ok := adminIDFromRequest(r)
	if !ok {
		http.Error(w, "Admin non autenticato", http.StatusUnauthorized)
		return
	}

	if err := ApproveValidation(data.ID, adminID); err != nil {
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

	adminID, ok := adminIDFromRequest(r)
	if !ok {
		http.Error(w, "Admin non autenticato", http.StatusUnauthorized)
		return
	}

	if err := RejectValidation(data.ID, adminID); err != nil {
		log.Printf("Errore rifiuto validazione %d: %v", data.ID, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "rejected", "message": "Marcatura rifiutata"})
}

func main() {
	log.Println("Avvio Time & Attendance Microservice...")
	initAdminTokenSecret()

	// 1. Inizializzazione Database
	InitDB()

	// 2. Avvia Goroutine lavoratore in background per Anviz (uno per ogni IP)
	// NOTA: il DeviceID tipicamente di default è 1.	// Avvia i worker TCP per ciascun orologio fisico in Goroutine con DeviceID corretto
	go SyncAnvizWorker("192.168.1.245", 1) // L'orologio in cui c'è testuale la pwd 12345
	go SyncAnvizWorker("192.168.1.246", 2) // L'orologio 246 da cui è stata rimossa la pwd e ha ID 2

	// 3. Registrazione API Endpoints
	http.HandleFunc("/api/clock", handleClock)
	http.HandleFunc("/api/login", handleLogin)
	http.HandleFunc("/api/admin/login", handleAdminLogin)
	http.HandleFunc("/api/employee/attendances", handleEmployeeAttendances)
	http.HandleFunc("/api/employee/monthly-attendances", handleEmployeeMonthlyAttendances)
	http.HandleFunc("/api/employee/range-attendances", handleEmployeeRangeAttendances)
	http.HandleFunc("/api/attendances", AuthMiddleware(handleAttendances))
	http.HandleFunc("/api/employees", AuthMiddleware(handleEmployees))
	http.HandleFunc("/api/admin/pending-validations", AuthMiddleware(handlePendingValidations))
	http.HandleFunc("/api/admin/approve-validation", AuthMiddleware(handleApproveValidation))
	http.HandleFunc("/api/admin/reject-validation", AuthMiddleware(handleRejectValidation))

	// Servire dashboard admin
	http.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "admin.html")
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
