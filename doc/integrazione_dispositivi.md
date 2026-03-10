# 📋 Piano di Integrazione Altro Dispositivo Anviz

Data: 4 Marzo 2026  
Autore: Antigravity Assistant

Questo documento descrive i passaggi necessari per aggiungere uno o più nuovi terminali di timbratura Anviz al sistema Marcatempo esistente.

---

## 🏗️ Architettura Attuale

Il sistema utilizza dei "Worker" definiti in `anviz_sync.go` che vengono avviati in background (Goroutines). Ogni worker gestisce la connessione TCP (Porta 5010) verso un IP specifico.

I dati scaricati vengono salvati nel database SQLite centralizzato `attendance.db`.

---

## 🛠️ Procedura Operativa

### 1. Preparazione dell'Hardware
*   **IP Statico:** Assegnare un indirizzo IP statico al nuovo dispositivo (es. `192.168.1.247`).
*   **Porta:** Assicurarsi che la porta `5010/TCP` sia aperta e raggiungibile dal server.
*   **User ID:** Verificare che gli ID utente sul nuovo dispositivo corrispondano a quelli già censiti nel database (per evitare duplicazioni o nomi errati).
*   **Password:** Se possibile, rimuovere o lasciare vuota la password di comunicazione del dispositivo (il driver attuale usa password vuota per default).

### 2. Modifica del Codice Sorgente
Aprire il file `main.go` e individuare la funzione `main()`. Aggiungere una nuova chiamata alla funzione `SyncAnvizWorker` prima della configurazione degli endpoint HTTP.

```go
// main.go ~ riga 583
go SyncAnvizWorker("192.168.1.247", 3) // Sostituire IP e dare un ID incrementale
```

*   **Parametro 1 (IP):** L'indirizzo IP del nuovo orologio.
*   **Parametro 2 (DeviceID):** Un numero intero univoco che identifica l'orologio nel protocollo Anviz.

### 3. Ricompilazione e Riavvio
Essendo un'applicazione scritta in Go, è necessario ricompilare l'eseguibile per rendere effettive le modifiche.

1.  Fermare il servizio corrente (`Ctrl+C` nel terminale).
2.  Eseguire il build:
    ```powershell
    go build -o marcatempo.exe .
    ```
3.  Avviare il nuovo eseguibile:
    ```powershell
    ./marcatempo.exe
    ```

---

## ✅ Verifica del Funzionamento

*   **Log del Server:** All'avvio, il server dovrebbe mostrare:
    `TCP Worker: Tentativo di sincronizzazione con l'Anviz IP 192.168.1.247...`
*   **Dashboard Admin:** Accedere all'area Admin (/admin) e verificare che le nuove timbrature appaiano nella tabella dello storico.
*   **Sincronizzazione Dipendenti:** Controllare che eventuali nuovi badge o PIN configurati sul dispositivo vengano registrati correttamente nella tabella dipendenti.

---

## 🌐 Integrazione Sedi Remote (VPN)

Se l'orologio si trova in un'altra città o sede, esporre la porta 5010 su internet tramite port-forwarding è **altamente sconsigliato** (anche se l'audit di sicurezza ha già evidenziato altre criticità, non aggiungiamone di nuove).

L'approccio migliore "integrato nel progetto" per collegare sedi remote è utilizzare una **VPN Mesh (es. Tailscale)**, che può essere gestita in due modi:

### 1. VPN a Livello Sistema Operativo (Consigliata)
Si installa il client VPN (Tailscale, WireGuard o OpenVPN) sia sul computer dove gira il server che su un computer/router nella sede remota che faccia da "ponte" (Subnet Router) per raggiungere l'IP dell'Anviz.
*   **Pro:** Il codice Go rimane identico. Basta puntare all'IP privato della VPN.
*   **Contro:** Richiede installazioni separate sul server.

### 2. Integrazione nel Binario Go (`tsnet`)
È possibile integrare il client Tailscale direttamente dentro l'applicazione Marcatempo utilizzando la libreria `tsnet`.
*   **Pro:** L'applicazione "porta la sua VPN con sé". Non serve configurare nulla sul sistema operativo.
*   **Contro:** Richiede una modifica più profonda al modo in cui il server apre le connessioni TCP.

### Scenario Tecnico con `tsnet`
Se decidessi di integrare `tsnet` nel progetto Go:
1.  Si aggiunge il pacchetto `tailscale.com/tsnet`.
2.  All'avvio (`main.go`), l'app esegue il login a Tailscale.
3.  Per connettersi all'orologio remoto, userebbe `s.Dial(ctx, "tcp", "192.168.1.247:5010")` invece del dial standard.
4.  L'IP remoto deve essere accessibile nel "Tailnet" dell'azienda.

---

## 🚀 Miglioramenti Futuri (Opzionale)

Se il numero di dispositivi aumenta frequentemente, si consiglia di:
1.  Creare una tabella `devices` nel database.
2.  Modificare `main.go` affinché legga gli IP dal database all'avvio invece di averli cablati nel codice.
3.  Implementare un'interfaccia web per aggiungere/rimuovere IP senza dover fermare il server.
