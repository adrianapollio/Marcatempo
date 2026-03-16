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
    *   **Modalità "New Records" (0x02)**: Il sistema richiede al dispositivo solo i record che non sono ancora stati scaricati.
    *   **Gestione Paginazione**: Se ci sono molti record (più di 25), il sistema richiede le "pagine successive" (0x00) finché non ha esaurito i dati dal buffer del dispositivo.
4.  **Reset Puntatore (0x4E)**: Una volta scaricati i dati con successo, invia il comando `CLEAR NEW RECORDS`. Questo dice al terminale: "Ho ricevuto tutto, la prossima volta dammi solo quello che succede da ora in poi".

## 3. Riconoscimento Nuovi Record e Sicurezza
*   **Deduplicazione**: Anche se il comando "New Records" fallisse e il dispositivo reinviasse dati vecchi, il database SQLite ha un **indice UNICO** sulla coppia `(employee_id, timestamp)`. 
*   **Protezione**: Il sistema usa una clausola `INSERT OR IGNORE`. Se un record esiste già, viene semplicemente saltato senza creare duplicati o errori.
*   **Timezone**: I dati vengono interpretati direttamente in `Europe/Rome`.

## 4. Possibili Problemi e Soluzioni
*   **Hardware Offline**: Se il dispositivo è spento o la rete è giù, il worker registra un errore nel log e riprova dopo 30 minuti. Nessun dato viene perso perché rimangono nella memoria interna del dispositivo finché non vengono scaricati con successo.
*   **Interruzioni di Corrente**: In rari casi di "freeze" del dispositivo, i record potrebbero non essere segnati come scaricati. La clausola di deduplicazione sopra descritta garantisce che al riavvio non ci siano doppi inserimenti.

## 5. Note sulla Normalizzazione
Ogni dato scaricato viene immediatamente convertito nei nomi canonici (`In`, `Out`, ecc.) prima del salvataggio, assicurando che il portale mostri sempre informazioni leggibili e coerenti.
