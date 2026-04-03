# Installazione Ferrara Da Zero

Data: 2026-04-01

## 1. Scopo

Questa guida descrive una procedura pulita e ripetibile per preparare da zero il server destinato alla sede di Ferrara.

La guida e stata costruita sulla base di cio che ha realmente funzionato durante la preparazione del server, tenendo conto anche degli errori incontrati lungo il percorso.

L'obiettivo e ottenere un server Ferrara che:

- parta con database pulito
- non erediti dati della sede di Napoli
- usi il branch corretto della repository
- abbia il system admin funzionante
- abbia nginx configurato correttamente con i certificati
- usi la configurazione device di Ferrara
- possa restare con sync disattivata finche il server non e fisicamente nella rete di Ferrara

## 2. Premessa importante

Durante questa preparazione il server si trova ancora a Napoli, non a Ferrara.

Questo significa una cosa fondamentale:

- il server puo essere configurato per Ferrara
- ma non puo ancora sincronizzare davvero il device di Ferrara, perche non si trova nella rete corretta

Quindi la procedura corretta e:

1. preparare tutto il server come istanza Ferrara
2. lasciare `ANVIZ_SYNC_ENABLED=false` finche il server non viene portato nella sede corretta
3. attivare la sync solo quando il server e realmente in Ferrara o comunque raggiunge il device Ferrara

## 3. Dati di riferimento Ferrara

Configurazione emersa e confermata:

- branch applicativo da usare: `dev`
- device Ferrara: `ID 3`
- IP device Ferrara: `192.168.2.245`

Configurazione env attesa:

```env
ANVIZ_DEVICES=3@192.168.2.245
```

## 4. Requisiti minimi

Sul server devono essere disponibili:

- Git
- Docker
- Docker Compose plugin oppure `docker compose`

## 5. Clone iniziale corretto

### 5.1 Posizionarsi nella home o nella cartella scelta

```bash
cd ~
```

### 5.2 Clonare la repository

```bash
git clone <URL_REPOSITORY> Marcatempo
cd ~/Marcatempo
```

### 5.3 Passare subito al branch corretto

Questa parte e importante, perche il clone normale puo portarti su `main`.

```bash
git fetch origin
git checkout dev
git pull --ff-only origin dev
```

### 5.4 Verifica branch

```bash
git branch --show-current
git rev-parse --short HEAD
```

Ci si aspetta:

- branch `dev`
- ultimo commit aggiornato della linea di sviluppo

## 6. Preparazione del file `.env` di Ferrara

Sul server deve esistere un solo file attivo:

- `.env`

Non usare `.env.napoli` o `.env.ferrara` direttamente sul server come file attivi dello stack.

### 6.1 Contenuto consigliato del `.env` di Ferrara

```env
MARCATEMPO_RUNTIME_DIR=./runtime

DEFAULT_SYSTEM_ADMIN_PASSWORD=cambia_subito_questa_password

TZ=Europe/Rome
PORT=8080
BACKUP_HOUR=23
BACKUP_MINUTE=59
BACKUP_ON_STARTUP=false

ANVIZ_SYNC_ENABLED=false
ANVIZ_DEVICES=3@192.168.2.245
ANVIZ_ACTIVE_DEVICE_IDS=

ANVIZ_SYNC_INTERVAL_MINUTES=30
ANVIZ_AUTO_ATTENDANCE_MODE=new
ANVIZ_MANUAL_ATTENDANCE_MODE=all
ANVIZ_ATTENDANCE_CHUNK_LIMIT=25
ANVIZ_COMMAND_DELAY_MS=5000
ANVIZ_READ_TIMEOUT_SECONDS=20
ANVIZ_WRITE_TIMEOUT_SECONDS=10
ANVIZ_CONNECT_TIMEOUT_SECONDS=5
ANVIZ_AUTO_SYNC_STAFF=false
ANVIZ_MANUAL_SYNC_STAFF=false
```

### 6.2 Perche la sync deve partire disattivata

Il server e ancora fisicamente a Napoli.

Quindi se la sync fosse attiva:

- il worker proverebbe a raggiungere il device Ferrara
- ma il device non sarebbe raggiungibile dalla rete attuale
- i log si riempirebbero di tentativi inutili

Per questo:

```env
ANVIZ_SYNC_ENABLED=false
```

e il valore corretto durante la fase di preparazione.

## 7. Preparazione delle directory runtime

Con la configurazione attuale, dati e certificati non vanno piu messi nella root del repository.

Vanno invece nella directory runtime:

```bash
~/Marcatempo/runtime
```

### 7.1 Creazione struttura

```bash
cd ~/Marcatempo
mkdir -p runtime/data
mkdir -p runtime/data/backups
mkdir -p runtime/certs
```

### 7.2 Verifica

```bash
ls -la runtime
ls -la runtime/data
ls -la runtime/certs
```

## 8. Copia dei certificati nella cartella giusta

Questo passaggio e stato un punto critico reale.

I certificati non devono stare in:

- `~/Marcatempo/certs`

ma in:

- `~/Marcatempo/runtime/certs`

### 8.1 File richiesti

Dentro `runtime/certs` devono esserci:

- `server.crt`
- `server.key`

### 8.2 Se li copi via WinSCP

Controlla con attenzione il percorso di destinazione.

Percorso corretto:

```bash
~/Marcatempo/runtime/certs/
```

Percorso sbagliato:

```bash
~/Marcatempo/certs/
```

### 8.3 Verifica locale sul server

```bash
ls -la ~/Marcatempo/runtime/certs
```

Devi vedere entrambi i file.

## 9. Primo avvio dell'istanza Ferrara

### 9.1 Avvio stack

```bash
cd ~/Marcatempo
docker compose up -d --build
```

### 9.2 Controllo container

```bash
docker ps
```

Ci si aspetta:

- `marcatempo_app` in stato `Up`
- `marcatempo_proxy` in stato `Up`

Se nginx non parte, la causa piu probabile e:

- certificati mancanti in `runtime/certs`

## 10. Verifica applicazione backend

### 10.1 Log applicazione

```bash
docker logs --tail 100 marcatempo_app
```

Le righe sane da cercare sono:

- database inizializzato correttamente
- system admin bootstrap presente o creato
- configurazione device Anviz caricata
- sync disabilitata da `ANVIZ_SYNC_ENABLED`

Esempio di log atteso in questa fase:

- `Configurazione device Anviz caricata: configurati=1 attivi=1`
- `Anviz background sync disabilitato da ANVIZ_SYNC_ENABLED`

### 10.2 Verifica variabili env dentro il container

```bash
docker exec marcatempo_app printenv ANVIZ_DEVICES
docker exec marcatempo_app printenv ANVIZ_SYNC_ENABLED
docker exec marcatempo_app printenv DEFAULT_SYSTEM_ADMIN_PASSWORD
```

Output atteso:

```bash
3@192.168.2.245
false
cambia_subito_questa_password
```

## 11. Verifica database pulito

### 11.1 Controllo file database

```bash
ls -la ~/Marcatempo/runtime/data
```

Output atteso:

- `attendance.db`
- `attendance.db-wal`
- `attendance.db-shm`
- directory `backups`

Nota importante:

I file:

- `attendance.db-wal`
- `attendance.db-shm`

sono normali e non indicano un errore. SQLite li usa in WAL mode.

### 11.2 Se `sqlite3` non e installato

Il comando:

```bash
sqlite3 runtime/data/attendance.db ".tables"
```

puo non essere disponibile. Non e un problema bloccante.

In quel caso basta verificare:

- che il file DB esista
- che l'app sia partita
- che l'accesso system admin funzioni

## 12. Verifica nginx e certificati

### 12.1 Log nginx

```bash
docker logs --tail 50 marcatempo_proxy
```

Se tutto e corretto:

- non ci devono essere errori tipo `cannot load certificate`
- il container deve restare `Up`

### 12.2 Errore tipico incontrato

Errore gia visto durante la preparazione:

```text
cannot load certificate "/etc/nginx/certs/server.crt"
```

Causa reale:

- certificati copiati nella cartella sbagliata

Correzione:

- spostarli in `~/Marcatempo/runtime/certs`
- poi rilanciare:

```bash
docker compose up -d --force-recreate
```

## 13. Verifica del system admin

### 13.1 Accesso via browser

Aprire:

- `/system-admin`

### 13.2 Credenziali iniziali

Username fisso:

- `admin`

Password iniziale:

- quella definita in `DEFAULT_SYSTEM_ADMIN_PASSWORD`

Nel caso attuale:

- `cambia_subito_questa_password`

### 13.3 Cambio password

Una volta entrati:

- cambiare subito la password dal pannello sicurezza

### 13.4 Recovery se il login non funziona

Se serve forzare la password:

```bash
docker exec -it marcatempo_app ./make_system_admin admin '<nuova_password>'
```

## 14. Verifica pannello admin

Aprire:

- `/admin`

Verificare che:

- la pagina non mostri piu l'errore `can't access property "forEach", empList is null`

Questo errore era stato causato da una lista dipendenti nulla e non e piu atteso dopo la correzione applicata.

## 15. Cosa NON fare mentre il server e ancora a Napoli

Non attivare ancora la sync in modo definitivo.

Se il server e ancora fisicamente a Napoli:

- non puo validare la raggiungibilita del device Ferrara
- non puo dimostrare che la sincronizzazione dati Ferrara funzioni davvero

Quindi in questa fase il server deve restare:

- installato
- pulito
- pronto
- ma con `ANVIZ_SYNC_ENABLED=false`

## 16. Attivazione della sync quando il server arriva a Ferrara

Quando il server sara nella rete corretta:

### 16.1 Modifica `.env`

```bash
cd ~/Marcatempo
sed -i 's/^ANVIZ_SYNC_ENABLED=false$/ANVIZ_SYNC_ENABLED=true/' .env
grep ANVIZ_SYNC_ENABLED .env
```

### 16.2 Ricreazione container

```bash
docker compose up -d --build --force-recreate
```

### 16.3 Verifica

```bash
docker exec marcatempo_app printenv ANVIZ_SYNC_ENABLED
docker logs --tail 100 marcatempo_app
```

Output atteso:

- `ANVIZ_SYNC_ENABLED=true`
- log con avvio del worker Anviz per `device=3 ip=192.168.2.245`

Esempio:

```text
ANVIZ sync run=... device=3 ip=192.168.2.245 mode=auto starting sync ...
```

## 17. Checklist finale di server Ferrara pronto

Il server Ferrara puo considerarsi correttamente preparato quando:

- la repo e sul branch `dev`
- il `.env` attivo e quello di Ferrara
- `ANVIZ_DEVICES=3@192.168.2.245`
- la directory runtime esiste
- i certificati sono in `runtime/certs`
- `marcatempo_app` e `marcatempo_proxy` sono `Up`
- il DB e stato creato in `runtime/data`
- il system admin entra correttamente
- il pannello admin non mostra l'errore `forEach`
- la sync resta disattivata finche il server non si trova nella rete di Ferrara

## 18. Procedura rapida riassunta

### Preparazione iniziale

```bash
cd ~
git clone <URL_REPOSITORY> Marcatempo
cd ~/Marcatempo
git fetch origin
git checkout dev
git pull --ff-only origin dev
mkdir -p runtime/data
mkdir -p runtime/data/backups
mkdir -p runtime/certs
```

Poi:

- copiare `.env` di Ferrara in `~/Marcatempo/.env`
- copiare `server.crt` e `server.key` in `~/Marcatempo/runtime/certs`

### Avvio

```bash
cd ~/Marcatempo
docker compose up -d --build
docker ps
docker logs --tail 100 marcatempo_app
docker logs --tail 50 marcatempo_proxy
```

### Attivazione futura sync

```bash
cd ~/Marcatempo
sed -i 's/^ANVIZ_SYNC_ENABLED=false$/ANVIZ_SYNC_ENABLED=true/' .env
docker compose up -d --build --force-recreate
docker exec marcatempo_app printenv ANVIZ_SYNC_ENABLED
docker logs --tail 100 marcatempo_app
```

## 19. Nota finale

La parte piu importante imparata durante questa preparazione e questa:

non basta "avere i file sul server", bisogna averli nel percorso giusto rispetto ai mount Docker e rispetto al `.env` attivo.

In particolare:

- i cert devono stare in `runtime/certs`
- il DB deve stare in `runtime/data`
- il `.env` deve stare nella root di `~/Marcatempo`
- il branch giusto e `dev`

Seguendo questa guida, il server Ferrara puo essere ricreato da zero in modo molto piu lineare e prevedibile.
