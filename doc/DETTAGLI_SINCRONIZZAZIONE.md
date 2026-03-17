# Dettagli Tecnici Sincronizzazione Anviz

Questo documento spiega il funzionamento interno del worker che scarica i dati dai dispositivi hardware Anviz.

## 1. Architettura del Worker
Il componente `anviz_sync.go` opera come un processo in background (Goroutine) che si avvia insieme all'applicazione principale.

*   **Frequenza**: Il sistema effettua un polling (interrogazione) ogni **30 minuti**.
*   **Protocollo**: Utilizza il protocollo proprietario Anviz su TCP (porta standard 5010).

## 2. Flusso di Operazione
Per ogni ciclo di sincronizzazione, il worker esegue i seguenti passaggi:

1.  **Connessione e Login (0x38)**: Tenta di autenticarsi con il dispositivo usando una password (default vuota).
2.  **Sincronizzazione Staff (0x72)**: Scarica i profili dei dipendenti per assicurarsi che i nomi siano aggiornati nel database locale.
3.  **Scaricamento Presenze (0x40)**:
    *   **Modalità "All Records" (0x01)**: Il sistema richiede l'intero archivio presenze del dispositivo a ogni sincronizzazione.
    *   **Gestione Paginazione**: Se ci sono molti record (più di 25), il sistema richiede le "pagine successive" (0x00) finché non ha esaurito i dati disponibili.
4.  **Nessun Reset Puntatore (0x4E)**: Il worker non invia più `CLEAR NEW RECORDS`. In questo modo la sincronizzazione non dipende dallo stato interno del puntatore "new records" del device ed evita perdite di marcature in caso di firmware o rete instabili.

## 3. Riconoscimento Nuovi Record e Sicurezza
*   **Deduplicazione Raw**: Ogni record hardware viene prima salvato in `device_raw_records` con unicità su `(device_id, employee_id, raw_device_timestamp, status_code)`.
*   **Deduplicazione Finale**: La tabella `records` separa le regole di unicità tra sorgente hardware e sorgente web/manuale. Per i record device la chiave è `(device_id, employee_id, raw_device_timestamp, status_code)`; per i record web/manuali la chiave è `(employee_id, timestamp, action)`.
*   **Protezione**: Se un record è già stato ingestito, viene conteggiato come duplicato e saltato senza creare nuovi inserimenti.
*   **Timezone**: I dati vengono interpretati direttamente in `Europe/Rome`.

## 4. Possibili Problemi e Soluzioni
*   **Hardware Offline**: Se il dispositivo è spento o la rete è giù, il worker registra un errore nel log e riprova dopo 30 minuti. Nessun dato viene perso perché rimangono nella memoria interna del dispositivo finché non vengono scaricati con successo.
*   **Interruzioni di Corrente**: In rari casi di "freeze" del dispositivo, i record potrebbero non essere segnati come scaricati. La clausola di deduplicazione sopra descritta garantisce che al riavvio non ci siano doppi inserimenti.

## 5. Note sulla Normalizzazione
Ogni dato scaricato viene immediatamente convertito nei nomi canonici (`In`, `Out`, ecc.) prima del salvataggio, assicurando che il portale mostri sempre informazioni leggibili e coerenti.
