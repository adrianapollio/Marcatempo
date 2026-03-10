# 🛡️ Piano di Implementazione: Integrazione tsnet (Tailscale)

Data: 4 Marzo 2026  
Stato: Proposta Tecnica  
Obiettivo: Permettere al server Marcatempo di raggiungere orologi Anviz in sedi remote senza VPN a livello OS.

---

## 1. Architettura Proposta

L'integrazione di `tsnet` trasforma l'applicazione Marcatempo in un nodo della rete Tailscale (Tailnet). L'applicazione non avrà bisogno di configurazioni di rete particolari sul server ospite; gestirà internamente l'autenticazione e il rooting del traffico verso gli IP privati della Tailnet.

### Componenti Necessari:
*   **Tailscale Auth Key:** Una chiave (preferibilmente di tipo *Reusable* e *Ephemeral*) generata dalla console Tailscale.
*   **Libreria `tailscale.com/tsnet`:** Integrata nel binario Go.

---

## 2. Roadmap di Implementazione

### Fase A: Preparazione (Console Tailscale)
1.  Accedere a [tailscale.com](https://login.tailscale.com/).
2.  Generare una **Auth Key** in `Settings > Keys`.
3.  (Opzionale) Configurare un **ACL** per restringere l'accesso del nodo "marcatempo" solo agli orologi remoti.

### Fase B: Modifiche al Codice Go

#### 1. Inizializzazione della Rete (`main.go`)
Dobbiamo creare un'istanza di `tsnet.Server` all'avvio:
```go
s := &tsnet.Server{
    Hostname: "marcatempo-server",
    AuthKey:  os.Getenv("TS_AUTHKEY"), // Meglio gestirla via variabile d'ambiente
}
defer s.Close()
```

#### 2. Modifica del Dialing (`anviz_sync.go`)
La funzione `syncFromDevice` deve smettere di usare `net.DialTimeout` standard e iniziare a usare il `Dial` fornito dal server Tailscale.

**Cambiamento concettuale:**
*   **Prima:** `net.DialTimeout("tcp", ip+":5010", 5*time.Second)`
*   **Dopo:** `s.Dial(ctx, "tcp", ip+":5010")`

#### 3. Gestione dei Worker
Passeremo l'istanza di `tsnet.Server` (o una funzione di Dialing) alla funzione `SyncAnvizWorker` in modo che possa aprire tunnel sicuri.

---

## 3. Gestione delle Sedi Remote

Per raggiungere l'orologio fisico nella città remota, esistono due opzioni:
1.  **Subnet Router:** Se nella sede remota c'è un PC/Router con Tailscale che espone la LAN locale (es. `192.168.1.0/24`). L'app contatterà direttamente l'IP locale.
2.  **Tailscale su PC ponte:** Se l'orologio è collegato a un PC che ha Tailscale installato, l'app contatterà l'IP Tailscale di quel PC (e servirà un piccolo port-forward locale su quel PC).

---

## 4. Vantaggi e Rischi

### ✅ Vantaggi
*   **Auto-contenimento:** L'eseguibile `marcatempo.exe` funziona ovunque ci sia internet, collegandosi alla sede remota in automatico.
*   **Sicurezza:** Tutto il traffico tra le città è crittografato end-to-end da WireGuard (il protocollo sotto Tailscale).
*   **Niente IP Pubblici:** Non serve aprire porte sul router della sede remota.

### ⚠️ Rischi/Impegni
*   **Dimensione Binario:** L'inclusione di `tsnet` aggiunge circa 15-20MB all'eseguibile.
*   **Dipendenza Esterna:** Il sistema dipende dalla disponibilità del piano di controllo Tailscale per stabilire le connessioni iniziali.

---

## 5. Prossimi Passi

Se approvato:
1.  Aggiunta della dipendenza nel `go.mod`.
2.  Refactoring di `anviz_sync.go` per accettare un'interfaccia di `Dialer`.
3.  Implementazione del setup del server in `main.go`.
