package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Uso: go run make_system_admin.go <Username> <Password>")
		return
	}
	username := os.Args[1]
	password := os.Args[2]

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "attendance.db"
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("Impossibile aprire il DB (%s): %v", dbPath, err)
	}
	defer db.Close()

	// Assicurati che la tabella esista (nel caso InitDB non sia stato ancora eseguito dal server)
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS system_admins (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL,
		created_at DATETIME NOT NULL
	)`)

	h := sha256.New()
	h.Write([]byte(password))
	hash := hex.EncodeToString(h.Sum(nil))

	query := `
		INSERT INTO system_admins (username, password_hash, created_at) VALUES (?, ?, ?)
		ON CONFLICT(username) DO UPDATE SET password_hash=excluded.password_hash
	`
	_, err = db.Exec(query, username, hash, time.Now().Format(time.RFC3339))
	if err != nil {
		log.Fatalf("Errore salvataggio admin: %v", err)
	}

	fmt.Printf("Amministratore di sistema '%s' creato/aggiornato con successo!\n", username)
}
