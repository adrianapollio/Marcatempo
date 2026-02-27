package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "modernc.org/sqlite"
)

// Questo script svuota la tabella records per forzare un re-download completo
// dai dispositivi Anviz con il codice corretto (timezone + status code).
// Al prossimo avvio di marcatempo.exe, il sync scaricherà tutti i record
// con i dati corretti.

func main() {
	db, err := sql.Open("sqlite", "./attendance.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Conta i record attuali
	var count int
	db.QueryRow("SELECT COUNT(*) FROM records").Scan(&count)
	fmt.Printf("Record attuali nel DB: %d\n", count)

	// Aggiunge la colonna status_code se non esiste già (migrazione)
	_, _ = db.Exec("ALTER TABLE records ADD COLUMN status_code INTEGER NOT NULL DEFAULT 0")

	// Svuota la tabella records (mantiene employees intatti)
	_, err = db.Exec("DELETE FROM records")
	if err != nil {
		log.Fatalf("Errore durante la cancellazione: %v", err)
	}

	// Reset autoincrement
	_, _ = db.Exec("DELETE FROM sqlite_sequence WHERE name='records'")

	// Vacuum per recuperare spazio
	_, _ = db.Exec("VACUUM")

	fmt.Println("✓ Tabella records svuotata con successo.")
	fmt.Println("✓ La tabella employees è stata mantenuta.")
	fmt.Println("")
	fmt.Println("Al prossimo avvio di marcatempo.exe, verrà eseguito un download")
	fmt.Println("completo di tutti i record da entrambi i dispositivi Anviz")
	fmt.Println("con timezone (Europe/Rome) e status code (0-7) corretti.")
}
