package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type sessionRole string

const (
	sessionCookieName   = "marcatempo_session"
	sessionLifetime     = 12 * time.Hour
	sessionRoleEmployee sessionRole = "employee"
	sessionRoleAdmin    sessionRole = "admin"
	sessionRoleSystem   sessionRole = "system_admin"
)

type sessionData struct {
	Token      string
	Role       sessionRole
	EmployeeID int
	Username   string
	ExpiresAt  time.Time
}

type sessionStore struct {
	mu       sync.RWMutex
	sessions map[string]sessionData
}

var authSessions = &sessionStore{sessions: make(map[string]sessionData)}

func newSessionToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func cookieShouldBeSecure() bool {
	value := strings.TrimSpace(os.Getenv("COOKIE_SECURE"))
	return value == "1" || strings.EqualFold(value, "true")
}

func (store *sessionStore) create(role sessionRole, employeeID int, username string) (sessionData, error) {
	token, err := newSessionToken()
	if err != nil {
		return sessionData{}, err
	}

	session := sessionData{
		Token:      token,
		Role:       role,
		EmployeeID: employeeID,
		Username:   username,
		ExpiresAt:  time.Now().Add(sessionLifetime),
	}

	store.mu.Lock()
	store.sessions[token] = session
	store.mu.Unlock()

	return session, nil
}

func (store *sessionStore) get(token string) (sessionData, bool) {
	store.mu.RLock()
	session, ok := store.sessions[token]
	store.mu.RUnlock()
	if !ok {
		return sessionData{}, false
	}

	if time.Now().After(session.ExpiresAt) {
		store.delete(token)
		return sessionData{}, false
	}

	return session, true
}

func (store *sessionStore) delete(token string) {
	store.mu.Lock()
	delete(store.sessions, token)
	store.mu.Unlock()
}

func writeSessionCookie(w http.ResponseWriter, session sessionData) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    session.Token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   cookieShouldBeSecure(),
		Expires:  session.ExpiresAt,
		MaxAge:   int(sessionLifetime.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   cookieShouldBeSecure(),
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

func createEmployeeSession(w http.ResponseWriter, employeeID int) error {
	role := sessionRoleEmployee
	if IsEmployeeAdmin(employeeID) {
		role = sessionRoleAdmin
	}

	session, err := authSessions.create(role, employeeID, "")
	if err != nil {
		return err
	}

	writeSessionCookie(w, session)
	return nil
}

func createSystemAdminSession(w http.ResponseWriter, username string) error {
	session, err := authSessions.create(sessionRoleSystem, 0, username)
	if err != nil {
		return err
	}

	writeSessionCookie(w, session)
	return nil
}

func getRequestSession(r *http.Request) (sessionData, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return sessionData{}, false
	}

	return authSessions.get(cookie.Value)
}

func clearRequestSession(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && strings.TrimSpace(cookie.Value) != "" {
		authSessions.delete(cookie.Value)
	}
	clearSessionCookie(w)
}

func unauthorizedJSON(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"message": message,
	})
}

func requireRoles(w http.ResponseWriter, r *http.Request, allowed ...sessionRole) (sessionData, bool) {
	session, ok := getRequestSession(r)
	if !ok {
		unauthorizedJSON(w, "Autenticazione richiesta", http.StatusUnauthorized)
		return sessionData{}, false
	}

	for _, role := range allowed {
		if session.Role == role {
			return session, true
		}
	}

	unauthorizedJSON(w, "Privilegi insufficienti", http.StatusForbidden)
	return sessionData{}, false
}

func requireAdminSession(w http.ResponseWriter, r *http.Request) (sessionData, bool) {
	session, ok := requireRoles(w, r, sessionRoleAdmin, sessionRoleSystem)
	if !ok {
		return sessionData{}, false
	}

	if session.Role == sessionRoleAdmin && !IsEmployeeAdmin(session.EmployeeID) {
		clearRequestSession(w, r)
		unauthorizedJSON(w, "Privilegi admin non piu validi", http.StatusUnauthorized)
		return sessionData{}, false
	}

	return session, true
}

func requireSystemSession(w http.ResponseWriter, r *http.Request) (sessionData, bool) {
	return requireRoles(w, r, sessionRoleSystem)
}

func sessionActorAdminID(session sessionData) int {
	if session.Role == sessionRoleSystem {
		return 0
	}
	return session.EmployeeID
}

func authenticatedEmployeeID(r *http.Request) (int, bool) {
	if session, ok := getRequestSession(r); ok {
		switch session.Role {
		case sessionRoleEmployee, sessionRoleAdmin:
			return session.EmployeeID, true
		}
	}

	empIDStr := r.Header.Get("X-Employee-ID")
	pin := r.Header.Get("X-Employee-PIN")
	if strings.TrimSpace(empIDStr) == "" || strings.TrimSpace(pin) == "" {
		return 0, false
	}

	empID, err := strconv.Atoi(empIDStr)
	if err != nil || !VerifyPIN(empID, pin) {
		return 0, false
	}

	return empID, true
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	clearRequestSession(w, r)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

func handleSessionInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
		return
	}

	session, ok := getRequestSession(r)
	if !ok {
		unauthorizedJSON(w, "Sessione non valida", http.StatusUnauthorized)
		return
	}

	response := map[string]interface{}{
		"authenticated": true,
		"role":          session.Role,
		"employeeId":    session.EmployeeID,
		"username":      session.Username,
		"isAdmin":       session.Role == sessionRoleAdmin,
		"isSystemAdmin": session.Role == sessionRoleSystem,
	}

	if session.Role == sessionRoleSystem {
		response["name"] = session.Username
	} else {
		response["name"] = GetEmployeeName(session.EmployeeID)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}