# Guida al Deploy di Marcatempo

Questo documento descrive in modo dettagliato le procedure per sviluppare, aggiornare e installare l'applicazione Marcatempo su diverse postazioni, utilizzando Docker.

---

## 🚀 1. Sviluppo e Test in Locale
Questa è la fase in cui modifichi il codice sul tuo PC (ad esempio i file `admin.html`, `index.html`, o i `.go`) e vuoi vedere subito il risultato.

### Cosa fare:
Se hai modificato un qualsiasi file che fa parte dell'applicazione e vuoi testarlo al volo, il modo più rapido è forzare la ricostruzione (`--build`) dell'immagine usando docker-compose.

1. Apri un terminale (PowerShell o Git Bash) nella cartella `Marcatempo`.
2. Lancia il comando per ricostruire l'immagine e riavviare il container:
   ```bash
   docker-compose up -d --build marcatempo
   ```
3. Il container verrà aggiornato e potrai visualizzare le tue modifiche navigando su [http://localhost:8080](http://localhost:8080) dal tuo browser.

*(Se modifichi il file di configurazione `nginx.conf` o i certificati, dovrai riavviare anche nginx con `docker-compose up -d --build nginx`)*.

---

## ☁️ 2. Pubblicazione di un Aggiornamento (Dal PC di sviluppo a Docker Hub)
Hai finito le modifiche sul tuo PC di sviluppo, le hai testate localmente e funzionano. Ora devi renderle disponibili per il server di produzione su cui si collegheranno effettivamente i dipendenti.

### Cosa fare:
Devi compilare la nuova versione dell'applicazione e caricarla nel tuo "magazzino" online su Docker Hub.

1. Ricrea l'immagine Docker specificando il nome finale `zibola/marcatempo:latest`:
   ```bash
   docker build -t zibola/marcatempo:latest .
   ```
2. Carica l'immagine aggiornata sul Docker Hub. Dal tuo PC esegui:
   ```bash
   docker push zibola/marcatempo:latest
   ```
   *(Nota: Se il terminale dice "denied" o simili, assicurati di esserti autenticato eseguendo prima `docker login` e inserendo nome utente `zibola` e tua password).*

---

## 🔄 3. Aggiornamento del Server di Produzione (La macchina "reale")
Sul computer/server che ospita l'applicazione e a cui si collegano tutti i giorni i dipendenti (quello con IP fisso HTTPS, ecc.). 
Qui i dati (*timbrature, database*) **NON andranno persi** perché salvati al sicuro in un volume montato (`./data`).

### Cosa fare:
Vuoi applicare le novità che hai appena caricato.

1. Accedi alla console/terminale del server di produzione.
2. Spostati nella cartella in cui si trova il file `docker-compose.yml`.
3. Scarica dal Docker Hub l'immagine più recente che hai caricato in precedenza:
   ```bash
   docker-compose pull marcatempo
   ```
4. Di' a Docker di riapplicare ed eseguire il container con l'immagine fresca usando:
   ```bash
   docker-compose up -d
   ```
L'applicazione si spegnerà e si riaccenderà nel giro di secondi (a meno che non modifichi anche nginx, l'IP resterà fermo su `host mode`).

---

## 🏗️ 4. Installazione da Zero (Nuovo Server o Nuova Sede)
Se in futuro vuoi prendere un nuovo computer vuoto e usarlo per ospitare un'istanza separata (o rimpiazzare il server attuale), non hai bisogno di rimetterti a programmare. Ti basta recuperare le configurazioni essenziali e scaricare l'app.

### Requisiti sul nuovo computer:
- Docker installato
- (Opzionale, ma consigliato) Docker Compose installato
- Se vuoi importare i vecchi dati, la vecchia cartella `data/` del database deve essere copiata fisicamente sul PC nuovo.

### Cosa fare:
1. Crea una nuova cartella per il progetto e chiamala come preferisci (es. `marcatempo-server`).
2. Copia dentro a questa cartella i seguenti file vitali dal tuo progetto originario:
   - `docker-compose.yml`
   - `Dockerfile.nginx` (Se ti serve usare Nginx in produzione)
   - `nginx.conf`
   - Cartella `certs` con i certificati ssl (Se HTTPS)
3. Apri il terminale nella cartella. **Non serve `docker build`**. Avvia semplicemente:
   ```bash
   docker-compose pull
   docker-compose up -d
   ```
   *Docker andrà a prendere la pre-compilazione su internet e accenderà tutto magicamente.*

4. **[Primo Avvio] Impostare un amministratore**. Se il database fosse totalmente vuoto e avessi bisogno di nominare l'admin iniziale del portale per iniziare a usarlo:
   ```bash
   docker exec -it marcatempo_app ./make_admin [ID_DIPENDENTE]
   ```
   (es. `docker exec -it marcatempo_app ./make_admin 1`)
