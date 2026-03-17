package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// BackupConfig contiene i parametri di configurazione per il backup periodico
type BackupConfig struct {
	BackupDir string // Directory dove salvare i backup
	Retention int    // Numero di backup da mantenere
	Hour      int    // Ora del giorno in cui eseguire il backup (0-23)
	Minute    int    // Minuto dell'ora in cui eseguire il backup (0-59)
}

// getBackupConfig legge la configurazione dai env vars o usa i default
func getBackupConfig() BackupConfig {
	dbPath := resolveDBPath()

	backupDir := os.Getenv("BACKUP_DIR")
	if backupDir == "" {
		// Default: cartella backups accanto al DB
		backupDir = filepath.Join(filepath.Dir(dbPath), "backups")
	}

	retention := 7
	if val := os.Getenv("BACKUP_RETENTION"); val != "" {
		if n, err := fmt.Sscanf(val, "%d", &retention); n != 1 || err != nil {
			retention = 7
		}
	}

	return BackupConfig{
		BackupDir: backupDir,
		Retention: retention,
		Hour:      0,  // Mezzanotte
		Minute:    0,
	}
}

// PerformBackup esegue un backup atomico del database usando VACUUM INTO.
// Restituisce il path del file di backup creato.
func PerformBackup() (string, error) {
	config := getBackupConfig()

	// Crea la directory di backup se non esiste
	if err := os.MkdirAll(config.BackupDir, 0755); err != nil {
		return "", fmt.Errorf("impossibile creare directory backup %s: %w", config.BackupDir, err)
	}

	// Genera il nome file con timestamp
	timestamp := time.Now().Format("20060102_150405")
	backupFile := filepath.Join(config.BackupDir, fmt.Sprintf("attendance_backup_%s.db", timestamp))

	// Usa VACUUM INTO per creare un backup atomico e consistente
	// Questo è sicuro con WAL mode e non blocca lettori/scrittori concorrenti
	_, err := DB.Exec(fmt.Sprintf(`VACUUM INTO '%s'`, backupFile))
	if err != nil {
		return "", fmt.Errorf("errore VACUUM INTO: %w", err)
	}

	log.Printf("[BACKUP] Backup completato: %s", backupFile)

	// Pulizia dei backup vecchi
	if err := rotateBackups(config); err != nil {
		log.Printf("[BACKUP] Attenzione: errore durante la rotazione dei backup: %v", err)
	}

	return backupFile, nil
}

// rotateBackups mantiene solo gli ultimi N backup, eliminando i più vecchi
func rotateBackups(config BackupConfig) error {
	entries, err := os.ReadDir(config.BackupDir)
	if err != nil {
		return err
	}

	// Filtra solo i file di backup
	var backupFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "attendance_backup_") && strings.HasSuffix(entry.Name(), ".db") {
			backupFiles = append(backupFiles, entry.Name())
		}
	}

	// Ordina in ordine alfabetico (il timestamp nel nome garantisce l'ordine cronologico)
	sort.Strings(backupFiles)

	// Se ci sono più backup del consentito, elimina i più vecchi
	if len(backupFiles) > config.Retention {
		toDelete := backupFiles[:len(backupFiles)-config.Retention]
		for _, f := range toDelete {
			path := filepath.Join(config.BackupDir, f)
			if err := os.Remove(path); err != nil {
				log.Printf("[BACKUP] Errore eliminazione backup vecchio %s: %v", f, err)
			} else {
				log.Printf("[BACKUP] Eliminato backup vecchio: %s", f)
			}
		}
	}

	return nil
}

// StartBackupScheduler avvia il scheduler di backup giornaliero in background.
// Esegue un backup immediato all'avvio, poi ogni giorno a mezzanotte.
func StartBackupScheduler() {
	config := getBackupConfig()
	log.Printf("[BACKUP] Scheduler avviato — backup giornaliero alle %02d:%02d, retention: %d copie, dir: %s",
		config.Hour, config.Minute, config.Retention, config.BackupDir)

	// Backup immediato all'avvio del server
	if _, err := PerformBackup(); err != nil {
		log.Printf("[BACKUP] Errore durante il backup iniziale: %v", err)
	}

	go func() {
		for {
			now := time.Now()
			// Calcola il prossimo orario di backup
			next := time.Date(now.Year(), now.Month(), now.Day(), config.Hour, config.Minute, 0, 0, now.Location())
			if !next.After(now) {
				// Se l'orario di oggi è già passato, schedula per domani
				next = next.Add(24 * time.Hour)
			}

			waitDuration := time.Until(next)
			log.Printf("[BACKUP] Prossimo backup schedulato: %s (tra %s)", next.Format("2006-01-02 15:04"), waitDuration.Round(time.Minute))

			time.Sleep(waitDuration)

			if _, err := PerformBackup(); err != nil {
				log.Printf("[BACKUP] Errore durante il backup schedulato: %v", err)
			}
		}
	}()
}

// GetBackupList restituisce la lista dei backup disponibili con dimensione e data
type BackupInfo struct {
	Filename  string    `json:"filename"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

func GetBackupList() ([]BackupInfo, error) {
	config := getBackupConfig()

	entries, err := os.ReadDir(config.BackupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []BackupInfo{}, nil
		}
		return nil, err
	}

	var backups []BackupInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "attendance_backup_") || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		backups = append(backups, BackupInfo{
			Filename:  entry.Name(),
			Size:      info.Size(),
			CreatedAt: info.ModTime(),
		})
	}

	// Ordina dal più recente al più vecchio
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].CreatedAt.After(backups[j].CreatedAt)
	})

	return backups, nil
}
