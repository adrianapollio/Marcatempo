# Stato Progetto al 20/03/2026

## 1. Contesto Applicativo

L'applicazione `Marcatempo` gestisce:

- timbrature da dispositivi Anviz
- timbrature web/manuali
- consultazione dipendente
- pannello admin HR
- pannello system admin tecnico

I dispositivi Anviz coinvolti sono:

- `device_id = 1` -> `192.168.1.245`
- `device_id = 2` -> `192.168.1.246`

Nel tempo il database è stato popolato da più sorgenti:

- sincronizzazione diretta dai device
- importazioni da Excel storico
- timbrature web/manuali

Questo ha prodotto una base dati ibrida, con una parte di record device storici privi di metadati moderni (`device_id`, `raw_device_timestamp`).

## 2. Problemi Iniziali Rilevati

I problemi emersi all'inizio dell'analisi erano:

- sincronizzazione Anviz che sul `245` andava in timeout prima di completare
- device molto affaticato durante il download completo dello storico
- presenza di record che risultavano "random non renderizzati" nel frontend
- difficoltà di accesso come admin o system admin in alcuni scenari
- documentazione e runtime non sempre coerenti tra loro

Il comportamento storico della sync era stato impostato in modo prudenziale:

- scarico completo dei record ad ogni sincronizzazione
- delay tra i chunk per non sovraccaricare i device
- evitata la dipendenza da `new records + clear`, perché in passato aveva prodotto perdite di timbrature

Il problema strutturale era che questa strategia, con device pieni di storico, diventava troppo costosa lato tempo e stabilità.

## 3. Diagnostica Eseguita

### 3.1 Analisi del repository

Sono stati analizzati in particolare:

- [main.go](/abs/path/c:/Users/brt/Documents/Marcatempo/main.go)
- [anviz_sync.go](/abs/path/c:/Users/brt/Documents/Marcatempo/anviz_sync.go)
- [db.go](/abs/path/c:/Users/brt/Documents/Marcatempo/db.go)
- [auth.go](/abs/path/c:/Users/brt/Documents/Marcatempo/auth.go)
- [admin.html](/abs/path/c:/Users/brt/Documents/Marcatempo/admin.html)
- [index.html](/abs/path/c:/Users/brt/Documents/Marcatempo/index.html)
- [docker-compose.yml](/abs/path/c:/Users/brt/Documents/Marcatempo/docker-compose.yml)

Temi verificati:

- schema DB
- pipeline raw/final
- login admin/system admin
- scheduling sync
- logica di rendering frontend
- configurazione Docker/runtime

### 3.2 Verifica dei database locali

Nel workspace erano presenti più database:

- `data/attendance.db`
- `data/attendance_real.db`
- `data/attendance_pre_device_recovery_2026-03-19.db`
- `data/attendance_new.db`
- `data/attendance_linux.db`

Distinzione importante:

- `attendance.db` era diventato il DB post-delete / parziale
- `attendance_real.db` conteneva lo storico reale prima della delete

### 3.3 Confronto tra DB corrente e DB reale

Prima della ricostruzione, il confronto mostrava:

`attendance.db`

- `records_total = 2745`
- `records_device = 2713`
- `raw_total = 2713`
- `device 1 = 1250`
- `device 2 = 1463`
- `device_missing_meta = 0`

`attendance_real.db`

- `records_total = 34452`
- `records_device = 24547`
- `raw_total = 7904`
- `device_missing_meta = 16643`
- `device 1 = 6446`
- `device 2 = 1458`
- `legacy device final senza metadati = 16643`

Conclusione:

- il DB reale era fortemente ibrido
- una grossa parte dello storico device proveniva da importazioni Excel legacy
- i record raw e i finali non erano allineati in modo uniforme

### 3.4 Analisi runtime Docker

È stata verificata anche la situazione del container:

- il runtime aveva avuto in precedenza mismatch con `attendance_linux.db`
- la configurazione del container e il codice del repository non erano sempre perfettamente allineati
- è stato poi riallineato il `docker-compose.yml` al DB corretto `attendance.db`

### 3.5 Analisi del frontend

La diagnosi sul problema "record non renderizzati" ha mostrato che:

- il frontend non perde necessariamente il record lato DB/API
- alcune timbrature vengono compresse in colonne giornaliere fisse
- marcature multiple o anomale finiscono in `extraMarks`
- questo può far sembrare "mancante" una timbratura che in realtà esiste

File coinvolti:

- [admin.html](/abs/path/c:/Users/brt/Documents/Marcatempo/admin.html)
- [index.html](/abs/path/c:/Users/brt/Documents/Marcatempo/index.html)

## 4. Strategia Dati Decisa

Dopo il confronto e il recupero dei file storici, è stata adottata questa linea:

- `Excel = sorgente storica`
- `Anviz = sorgente live`

Questo evita di dover dipendere da una full sync dal device per ricostruire anni di storico.

## 5. Import Storico Eseguito

### 5.1 Import device 246

File usato:

- `Dispositivo 246.xlsx`

Import eseguito in `attendance.db` con:

- `source = 'device'`
- `device_id = 2`
- `raw_device_timestamp` derivato dal timestamp

Backup creato prima dell'import:

- `data/attendance_before_246_import_20260320.db`

Risultato:

- righe valide processate: `1456`
- nuovi record inseriti: `296`
- resto già presente come duplicato

### 5.2 Import device 245

File usati:

- `APRILE - AGOSTO2025.xlsx`
- `Settembre - Dicembre 2025.xlsx`
- `Gennaio - Marzo 2026.xlsx`

Import eseguito in `attendance.db` con:

- `source = 'device'`
- `device_id = 1`
- `raw_device_timestamp` derivato dal timestamp

Backup creato prima dell'import:

- `data/attendance_before_245_import_20260320.db`

Risultato:

- `APRILE - AGOSTO2025`: `6354` nuovi record
- `Settembre - Dicembre 2025`: `9888` nuovi record
- `Gennaio - Marzo 2026`: `6467` nuovi record
- totale nuovi record `245`: `22709`

### 5.3 Righe non importate

Sono state isolate e analizzate.

Esito:

- `368` righe saltate in totale
- causa unica: righe con `3 colonne` invece di `4`
- quasi tutte riferite all'utente `46`
- le ultime `2` riferite all'utente `156`

Pattern tipico:

- `ID | Data | Stato`

invece di:

- `ID | Nome | Data | Stato`

Dato che l'utente `46` risulta `Nome vuoto` ed è presumibilmente un ex dipendente, si è deciso di non forzare il recupero di queste righe.

### 5.4 Import delta 19-20 marzo

Per coprire il frangente tra gli export storici e il passaggio alla nuova sync live sono stati integrati anche due Excel delta:

- `(245) 19-20 Marzo.xlsx`
- `(246) 19-20 Marzo.xlsx`

Backup creato prima dell'import delta:

- `data/attendance_before_delta_import_20260320.db`

Risultato import:

- `245`: `179` righe processate, `74` nuove inserite, `0` saltate
- `246`: `8` righe processate, `2` nuove inserite, `0` saltate

Questo passaggio è servito a ridurre il rischio di buchi temporali tra:

- export Excel storico
- stato attuale del DB
- futura attivazione della sync live in modalità `new-records`

## 6. Stato del Database Dopo la Ricostruzione

Dopo l'import storico dei due device, lo stato di `data/attendance.db` è:

- `records_total = 25826`
- `records_device = 25794`
- `records_web = 10`
- `records_manual_web = 22`
- `raw_total = 25794`
- `device_missing_meta = 0`

Per device:

`device 1 / 245`

- `24033` record
- finestra: `2025-02-27T08:43:41+01:00 -> 2026-03-20T11:24:40+01:00`

`device 2 / 246`

- `1761` record
- finestra: `2025-02-27T08:47:27+01:00 -> 2026-03-20T07:44:53+01:00`

Conclusione:

- il nuovo `attendance.db` è una base molto più pulita del DB reale precedente
- i record device ora hanno metadati completi
- `device_id null` nei record device è stato azzerato

## 7. Implementazione P1 Completata

### 7.1 Obiettivo

Ridurre il rischio di timeout nei device live, in particolare sul `245`, e migliorare il debug.

### 7.2 Modifiche effettuate in `anviz_sync.go`

File modificato:

- [anviz_sync.go](/abs/path/c:/Users/brt/Documents/Marcatempo/anviz_sync.go)

Sono stati introdotti:

- parametri runtime per la sync
- modalità distinte tra sync automatica e manuale
- logging estensivo per ogni run

Nuove configurazioni supportate:

- `ANVIZ_SYNC_INTERVAL_MINUTES`
- `ANVIZ_AUTO_ATTENDANCE_MODE`
- `ANVIZ_MANUAL_ATTENDANCE_MODE`
- `ANVIZ_ATTENDANCE_CHUNK_LIMIT`
- `ANVIZ_COMMAND_DELAY_MS`
- `ANVIZ_READ_TIMEOUT_SECONDS`
- `ANVIZ_WRITE_TIMEOUT_SECONDS`
- `ANVIZ_CONNECT_TIMEOUT_SECONDS`
- `ANVIZ_AUTO_SYNC_STAFF`
- `ANVIZ_MANUAL_SYNC_STAFF`

Comportamento attuale previsto:

- sync automatica: `new-records`
- sync manuale: `all-records`
- staff sync automatica: disattivata di default
- staff sync manuale: attiva di default

### 7.3 Logging aggiunto

Per ogni run vengono loggati:

- `run id`
- `device`
- `ip`
- `manual/auto`
- strategia attendance
- chunk limit
- timeout
- durata run
- numero staff chunks
- numero attendance chunks
- ultimo chunk riuscito
- ultimo chunk mode
- count ricevuti / inseriti / duplicati / errori
- finestra temporale dei record

### 7.4 Compatibilità

Per non perdere lo storico della logica preesistente:

- la vecchia implementazione è stata rinominata `syncFromDeviceLegacy`
- la nuova implementazione è ora quella richiamata da `syncFromDevice`

## 8. Modifiche a Docker Compose

File modificato:

- [docker-compose.yml](/abs/path/c:/Users/brt/Documents/Marcatempo/docker-compose.yml)

Sono state aggiunte esplicitamente le env per la sync:

- `ANVIZ_SYNC_ENABLED=true`
- `ANVIZ_SYNC_INTERVAL_MINUTES=30`
- `ANVIZ_AUTO_ATTENDANCE_MODE=new`
- `ANVIZ_MANUAL_ATTENDANCE_MODE=all`
- `ANVIZ_ATTENDANCE_CHUNK_LIMIT=25`
- `ANVIZ_COMMAND_DELAY_MS=5000`
- `ANVIZ_READ_TIMEOUT_SECONDS=20`
- `ANVIZ_WRITE_TIMEOUT_SECONDS=10`
- `ANVIZ_CONNECT_TIMEOUT_SECONDS=5`
- `ANVIZ_AUTO_SYNC_STAFF=false`
- `ANVIZ_MANUAL_SYNC_STAFF=true`

Il DB puntato dal compose è:

- `DB_PATH=/app/data/attendance.db`

## 9. Verifiche Eseguite

Verifica build:

- `go build ./...` eseguito con successo

Verifica consistenza dati:

- raw e final dei device ricostruiti risultano allineati
- `device_missing_meta = 0`

## 10. Stato Attuale

Ad oggi il progetto si trova in questo stato:

- storico `245` e `246` ricostruito nel DB corrente
- base dati device pulita e coerente
- P1 backend sync live implementato
- logging sync estensivo implementato
- configurazione Docker aggiornata
- issue frontend sui record "compressi" ancora da rifinire

## 11. Prossimo Passo Consigliato

Il prossimo step naturale è il `P2`:

- migliorare la visualizzazione di `extraMarks`
- rendere più evidente in admin e/o nel portale dipendenti che alcune marcature esistono ma non entrano nelle colonne fisse

Questo è il punto più probabile in cui nasce ancora la percezione di "record non renderizzati".

## 12. Piano Deploy Produzione

Sequenza raccomandata:

1. Backup completo del DB produzione.
2. Backup della configurazione/container produzione.
3. Build della nuova immagine/binario.
4. Deploy con:
   - `DB_PATH=/app/data/attendance.db`
   - `ANVIZ_AUTO_ATTENDANCE_MODE=new`
   - `ANVIZ_AUTO_SYNC_STAFF=false`
5. Avvio del servizio.
6. Verifica log dei primi cicli automatici.
7. Test smoke:
   - login dipendente
   - login admin
   - login system admin
   - apertura dashboard admin
   - sync manuale
8. Monitoraggio 24-48 ore.
9. Solo dopo stabilizzazione, valutare la pulizia dello storico dai dispositivi.

## 13. Nota Operativa Importante

I dati dai dispositivi non vanno cancellati immediatamente.

Prima va confermato che:

- la sync live nuova è stabile
- i log non mostrano timeout ripetuti
- il frontend rende correttamente i casi critici
- il deploy produzione è allineato al DB corretto

Solo a quel punto la cancellazione dati dai device potrà essere valutata come passo finale.

## 14. Checklist Operativa

### Fase 0 - Messa in sicurezza e diagnosi iniziale

- [x] Ricostruito il contesto tecnico del progetto
- [x] Analizzati backend, frontend, auth, sync e runtime Docker
- [x] Verificata la presenza di più database locali
- [x] Distinto il DB reale dal DB post-delete
- [x] Confrontati `attendance.db` e `attendance_real.db`
- [x] Identificato il problema dello storico ibrido con record device legacy senza metadati

### Fase 1 - Strategia dati

- [x] Decisa la strategia `Excel = storico`, `Anviz = live`
- [x] Scelto di non usare il device come unica fonte per il refill storico
- [x] Confermato che la delete massiva non è il primo passo sicuro

### Fase 2 - Import storico device 246

- [x] Verificato il file Excel del `246`
- [x] Creato backup del DB prima dell'import
- [x] Importato lo storico del `246` in `attendance.db`
- [x] Popolati `device_id` e `raw_device_timestamp` per il `246`
- [x] Verificata la finestra temporale finale del `246`

### Fase 3 - Import storico device 245

- [x] Verificati i tre file Excel del `245`
- [x] Creato backup del DB prima dell'import
- [x] Importato lo storico del `245` in `attendance.db`
- [x] Popolati `device_id` e `raw_device_timestamp` per il `245`
- [x] Analizzate le righe saltate
- [x] Confermato che quasi tutte le righe escluse sono dell'utente `46` con nome assente
- [x] Azzerati i record device con metadati mancanti nel DB corrente

### Fase 4 - Stato DB corrente

- [x] Verificato che `attendance.db` sia ora la base dati candidata
- [x] Verificato allineamento `raw_total == records_device`
- [x] Verificato `device_missing_meta = 0`
- [x] Verificate le finestre temporali finali dei device `245` e `246`
- [x] Integrato il delta 19-20 marzo per ridurre il rischio di buchi nel cutover

### Fase 5 - P1 Sync live e logging

- [x] Implementata nuova strategia di sync live in `anviz_sync.go`
- [x] Distinta sync automatica da sync manuale
- [x] Impostata sync automatica su `new-records`
- [x] Impostata sync manuale su `all-records`
- [x] Disattivata di default la staff sync automatica
- [x] Lasciata attiva di default la staff sync manuale
- [x] Introdotte env configurabili per timeout, delay, chunk e intervallo
- [x] Aggiunto logging estensivo per ogni run
- [x] Aggiornato `docker-compose.yml`
- [x] Verificata la build con `go build ./...`

### Fase 6 - Ancora aperto

- [ ] Verifica dei record compressi/non evidenti nel frontend
- [ ] Miglioramento UI per mostrare meglio `extraMarks`
- [ ] Test locale end-to-end con sync live reale
- [ ] Preparazione del pacchetto di deploy produzione
- [ ] Backup completo della produzione prima del rilascio
- [ ] Deploy produzione della nuova sync
- [ ] Monitoraggio dei primi cicli automatici post-deploy
- [ ] Verifica che non ci siano buchi nel periodo di transizione Excel -> live
- [ ] Decisione finale sulla pulizia dei dispositivi
- [ ] Eventuale cancellazione dello storico dai device
