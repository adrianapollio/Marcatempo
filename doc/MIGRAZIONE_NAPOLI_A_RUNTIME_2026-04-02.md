# Migrazione Napoli A Runtime

Data: 2026-04-02

## 1. Scopo

Questa guida definisce la procedura standard per migrare l'istanza di Napoli dal layout storico:

- `data/`
- `certs/`

al layout unificato:

- `runtime/data/`
- `runtime/certs/`
- `runtime/imports/`

L'obiettivo e:

- avere la stessa struttura di Ferrara
- eliminare cartelle duplicate o ambigue
- rendere piu semplice il lavoro futuro anche in locale
- evitare errori dovuti a path simili come `data/`, `data/data/`, `runtime/data/`

## 2. Stato di partenza confermato per Napoli

Al momento della stesura di questa guida, Napoli usa:

- progetto: `~/marcatempo`
- mount attivo del container app: `/root/marcatempo/data -> /app/data`
- `MARCATEMPO_RUNTIME_DIR=.`

Quindi, prima della migrazione, il DB attivo e:

- `~/marcatempo/data/attendance.db`
- `~/marcatempo/data/attendance.db-wal`
- `~/marcatempo/data/attendance.db-shm`

La cartella `data/data/` non deve piu essere considerata attiva.

## 3. Struttura standard finale desiderata

Dopo la migrazione, Napoli dovra avere questa struttura:

```text
~/marcatempo/
  .env
  docker-compose.yml
  runtime/
    data/
      attendance.db
      attendance.db-wal
      attendance.db-shm
      backups/
      excels/
    certs/
      server.crt
      server.key
    imports/
```

Note pratiche:

- `runtime/data/excels/` puo essere usata per file Excel e import manuali
- `runtime/imports/` puo essere usata per file temporanei o estratti non ancora processati
- il DB attivo non e mai solo `attendance.db`
- il DB attivo e sempre il gruppo `attendance.db` + `attendance.db-wal` + `attendance.db-shm`

## 4. Principio di sicurezza

La migrazione va fatta con i container fermi.

Motivo:

- SQLite in WAL mode puo avere dati importanti nel file `attendance.db-wal`
- copiare solo `attendance.db` a container attivi puo produrre una copia incoerente

Questa guida usa sempre:

1. stop container
2. copia completa DB + WAL + SHM
3. riallineamento `.env`
4. riavvio container
5. verifica
6. archivio delle cartelle legacy

## 5. Checklist pre-migrazione

Prima di iniziare verificare:

- il browser admin vede i dati corretti
- i container sono in stato `Up`
- la sync Napoli e stabile
- la cartella `data/` contiene il DB attivo giusto

Comandi di controllo:

```bash
cd ~/marcatempo
docker ps
grep MARCATEMPO_RUNTIME_DIR .env
docker inspect marcatempo_app --format '{{range .Mounts}}{{println .Source "->" .Destination}}{{end}}'
ls -lh data
ls -lh certs
```

## 6. Procedura di migrazione Napoli

### 6.1 Fermare i container

```bash
cd ~/marcatempo
docker-compose down
```

### 6.2 Creare la struttura runtime standard

```bash
mkdir -p runtime/data/backups
mkdir -p runtime/data/excels
mkdir -p runtime/certs
mkdir -p runtime/imports
mkdir -p migration-backup-2026-04-02/active-data
mkdir -p migration-backup-2026-04-02/active-certs
```

### 6.3 Salvare una copia di sicurezza della situazione attiva

```bash
cp -a data/attendance.db migration-backup-2026-04-02/active-data/
cp -a data/attendance.db-wal migration-backup-2026-04-02/active-data/ 2>/dev/null || true
cp -a data/attendance.db-shm migration-backup-2026-04-02/active-data/ 2>/dev/null || true
cp -a data/backups/. migration-backup-2026-04-02/active-data/backups/ 2>/dev/null || true
cp -a certs/server.crt migration-backup-2026-04-02/active-certs/ 2>/dev/null || true
cp -a certs/server.key migration-backup-2026-04-02/active-certs/ 2>/dev/null || true
```

Se necessario, creare prima la directory dei backup storici:

```bash
mkdir -p migration-backup-2026-04-02/active-data/backups
```

### 6.4 Copiare il DB attivo dentro runtime

```bash
cp -a data/attendance.db runtime/data/
cp -a data/attendance.db-wal runtime/data/ 2>/dev/null || true
cp -a data/attendance.db-shm runtime/data/ 2>/dev/null || true
cp -a data/backups/. runtime/data/backups/ 2>/dev/null || true
cp -a data/excels/. runtime/data/excels/ 2>/dev/null || true
```

### 6.5 Copiare i certificati dentro runtime

```bash
cp -a certs/server.crt runtime/certs/ 2>/dev/null || true
cp -a certs/server.key runtime/certs/ 2>/dev/null || true
```

### 6.6 Aggiornare `.env`

La variabile va riportata allo standard:

```bash
sed -i 's#^MARCATEMPO_RUNTIME_DIR=.*#MARCATEMPO_RUNTIME_DIR=./runtime#' .env
grep MARCATEMPO_RUNTIME_DIR .env
```

Valore atteso:

```env
MARCATEMPO_RUNTIME_DIR=./runtime
```

Per mantenere Napoli allineata alla configurazione locale ed evitare rumore inutile:

```env
ANVIZ_AUTO_SYNC_STAFF=false
ANVIZ_MANUAL_SYNC_STAFF=false
```

### 6.7 Riavviare i container

```bash
docker-compose up -d --force-recreate
```

## 7. Verifiche post-migrazione

### 7.1 Verifica mount attivi

```bash
docker inspect marcatempo_app --format '{{range .Mounts}}{{println .Source "->" .Destination}}{{end}}'
```

Output atteso:

```text
/root/marcatempo/runtime/data -> /app/data
```

Per il proxy, la cartella attesa e:

```text
/root/marcatempo/runtime/certs -> /etc/nginx/certs
```

### 7.2 Verifica file runtime

```bash
ls -lh runtime/data
ls -lh runtime/certs
```

Ci si aspetta:

- `attendance.db`
- `attendance.db-wal`
- `attendance.db-shm`
- `backups/`
- `excels/`
- `server.crt`
- `server.key`

### 7.3 Verifica log

```bash
docker ps
docker logs --tail 20 marcatempo_app
docker logs --tail 20 marcatempo_proxy
```

Ci si aspetta:

- entrambi i container `Up`
- nessun errore su certificati
- nessun errore su apertura DB
- sync normale di Napoli

### 7.4 Verifica funzionale

Da browser:

- hard refresh su `/admin`
- hard refresh su `/system-admin`
- verifica dati storici presenti
- verifica che la sync continui a funzionare

## 8. Gestione cartelle legacy

Non cancellare subito le vecchie cartelle.

Prima rinominarle:

```bash
mv data data.legacy-2026-04-02
mv certs certs.legacy-2026-04-02
```

Questa sequenza pero va fatta solo dopo una verifica completa.

Per evitare confusione, la strategia consigliata e:

1. migrare a `runtime/`
2. verificare per almeno una giornata lavorativa
3. rinominare le cartelle legacy
4. tenerle come archivio temporaneo
5. eliminarle solo in un secondo momento

### Variante piu prudente

Se non vuoi rinominare subito `data/` e `certs/`, puoi lasciarle ferme e non usarle piu.

In questo caso la vera regola operativa diventa:

- si lavora solo dentro `runtime/`
- `data/` e `certs/` sono considerate legacy e non vanno toccate

## 9. Procedura rollback

Se dopo la migrazione qualcosa non torna:

```bash
cd ~/marcatempo
docker-compose down
sed -i 's#^MARCATEMPO_RUNTIME_DIR=.*#MARCATEMPO_RUNTIME_DIR=.#' .env
rm -f data/attendance.db data/attendance.db-wal data/attendance.db-shm
cp -a migration-backup-2026-04-02/active-data/attendance.db data/
cp -a migration-backup-2026-04-02/active-data/attendance.db-wal data/ 2>/dev/null || true
cp -a migration-backup-2026-04-02/active-data/attendance.db-shm data/ 2>/dev/null || true
cp -a migration-backup-2026-04-02/active-certs/server.crt certs/ 2>/dev/null || true
cp -a migration-backup-2026-04-02/active-certs/server.key certs/ 2>/dev/null || true
docker-compose up -d --force-recreate
```

## 10. Standard futuro per il locale

Per mantenere il repository ordinato anche in locale, il principio deve essere lo stesso:

- nessun DB attivo in `data/`
- nessun cert attivo in `certs/`
- tutto dentro un solo `runtime/`

Gli eventuali file `.env.napoli` e `.env.ferrara` restano utili come template di configurazione,
ma il percorso runtime locale resta unico:

```env
MARCATEMPO_RUNTIME_DIR=./runtime
```

Questo e coerente con il caso d'uso reale:

- la macchina locale non deve mantenere due sedi attive in parallelo
- Ferrara avra vita propria sul server Ferrara
- Napoli avra vita propria sul server Napoli
- in locale si lavora sempre su una sola istanza per volta

## 11. Decisione operativa consigliata

La migrazione di Napoli a `runtime/` va fatta, ma non in emergenza.

La sequenza raccomandata e:

1. mantenere Ferrara gia standardizzata su `runtime/`
2. schedulare una breve finestra di manutenzione per Napoli
3. eseguire la procedura di questa guida con container fermi
4. verificare i dati da browser
5. solo dopo archiviare le cartelle legacy

Questo permette di avere, alla fine, uno standard unico su:

- Ferrara
- Napoli
- locale
