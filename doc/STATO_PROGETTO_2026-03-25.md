# Stato Progetto al 25/03/2026

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

Nel tempo il database e stato popolato da piu sorgenti:

- sincronizzazione diretta dai device
- importazioni da Excel storico
- timbrature web/manuali

Questo ha prodotto una base dati ibrida, con una parte di record device storici privi di metadati moderni (`device_id`, `raw_device_timestamp`).

## 2. Problemi Iniziali Rilevati

I problemi emersi nella fase iniziale erano:

- sincronizzazione Anviz troppo costosa sui device con molto storico
- timeout, soprattutto sul device `245`
- difficolta nel capire se i record "mancanti" fossero persi o solo non ben renderizzati
- incoerenze tra database, runtime Docker e stato reale del frontend
- mancanza di una procedura standard per badge smarriti / badge sostitutivi

## 3. Diagnostica Eseguita

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
- comportamento della multiselect admin
- gestione badge storici / ex

## 4. Strategia Dati Decisa

Dopo l'analisi del repository e dei dati:

- `Excel = sorgente storica`
- `Anviz = sorgente live`

Questo evita di dover dipendere da una full sync dai device per ricostruire anni di storico.

Per i casi di badge smarrito e stato deciso inoltre:

- nessun merge della tabella `records`
- nessuno spostamento massivo dello storico da un ID all'altro
- aggiornamento mirato della sola tabella `employees`
- adattamento frontend per consultare badge storici/ex senza trattarli come badge attivi

## 5. Import Storico Eseguito

### 5.1 Import device 246

File usato:

- `Dispositivo 246.xlsx`

Import eseguito in `attendance.db` con:

- `source = 'device'`
- `device_id = 2`
- `raw_device_timestamp` derivato dal timestamp

Risultato:

- righe valide processate: `1456`
- nuovi record inseriti: `296`

### 5.2 Import device 245

File usati:

- `APRILE - AGOSTO2025.xlsx`
- `Settembre - Dicembre 2025.xlsx`
- `Gennaio - Marzo 2026.xlsx`

Import eseguito in `attendance.db` con:

- `source = 'device'`
- `device_id = 1`
- `raw_device_timestamp` derivato dal timestamp

Risultato:

- `APRILE - AGOSTO2025`: `6354` nuovi record
- `Settembre - Dicembre 2025`: `9888` nuovi record
- `Gennaio - Marzo 2026`: `6467` nuovi record
- totale nuovi record `245`: `22709`

### 5.3 Delta temporale

Per coprire il passaggio tra export storico e sync live sono stati integrati anche delta aggiuntivi del 19-20 marzo 2026.

## 6. Stato del Database Dopo la Ricostruzione

Nel DB locale `data/attendance.db`, dopo ricostruzione e pulizia:

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

- la base dati device e coerente
- raw e final sono riallineati
- i record device hanno metadati completi

## 7. Sync Live Anviz

### 7.1 Obiettivo

Ridurre il rischio di timeout nei device live e separare chiaramente:

- sync automatica ricorrente
- sync manuale completa
- sync staff

### 7.2 Modifiche implementate

File coinvolti:

- [anviz_sync.go](/abs/path/c:/Users/brt/Documents/Marcatempo/anviz_sync.go)
- [docker-compose.yml](/abs/path/c:/Users/brt/Documents/Marcatempo/docker-compose.yml)

Configurazione attuale:

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

Comportamento atteso:

- sync automatica: `new-records`
- sync manuale: `all-records`
- staff sync automatica: disattivata
- staff sync manuale: attiva

### 7.3 Verifica pratica della staff sync

E stata eseguita una sync manuale completa.

Dai log:

- la fase staff e partita davvero
- i `staff_chunks` sono stati eseguiti prima della fase attendance
- i timeout osservati sono avvenuti nella parte attendance, non nella parte staff

Conclusione operativa:

- la staff sync non si e dimostrata sufficiente a riallineare in modo affidabile `employees`
- non e stato ritenuto utile abilitare `ANVIZ_AUTO_SYNC_STAFF=true`

## 8. Gestione Badge Smarrito / Badge Multipli

E stato gestito un caso reale di dipendente con piu badge persi nel tempo.

Scenario reale:

- badge storico `33`
- badge storico `56`
- badge attivo `58`

Decisione adottata:

- non fare merge dei record storici
- non spostare le timbrature da un ID all'altro
- aggiornare solo la tabella `employees`
- lasciare i badge storici consultabili solo a fini di storico

Motivazioni:

- minore rischio sui dati
- niente collisioni o riscritture dello storico
- nessun impatto sulla pipeline di deduplicazione
- maggiore semplicita nel passaggio in produzione

Documento operativo dedicato:

- [GESTIONE_SMARRIMENTO_BADGE_E_STORICO.md](/abs/path/c:/Users/brt/Documents/Marcatempo/doc/GESTIONE_SMARRIMENTO_BADGE_E_STORICO.md)

## 9. Modifiche Recenti a Frontend e Backend

### 9.1 Frontend admin

File modificato:

- [admin.html](/abs/path/c:/Users/brt/Documents/Marcatempo/admin.html)

Modifiche introdotte:

- gli ID storici `33` e `56` sono esclusi dal filtro frontend che nasconde gli `EX`
- gli ID `33` e `56` sono visibili anche nella multiselect
- gli ID `33` e `56` non generano piu righe vuote sintetiche di assenza
- le timbrature reali per `33` e `56` restano renderizzate

Impatto:

- storico badge persi consultabile
- niente assenze artificiali giornaliere per badge ex
- riepilogo periodo piu veritiero per gli ID storici

### 9.2 Backend attendances multi-ID

File modificato:

- [db.go](/abs/path/c:/Users/brt/Documents/Marcatempo/db.go)

Problema corretto:

- la multiselect inviava `employee_id=33,56` o `56,58`
- il backend filtrava solo con `employee_id = ?`
- i record reali quindi non venivano restituiti per piu ID insieme

Correzione introdotta:

- parsing degli ID separati da virgola
- costruzione query con `employee_id IN (?, ?, ...)`

Risultato:

- la multiselect recupera correttamente le timbrature di piu dipendenti

## 10. Verifiche Eseguite

Verifica build:

- `go build ./...` eseguito con successo il 25/03/2026

Verifica consistenza dati:

- raw e final device risultano allineati
- `device_missing_meta = 0`

Verifica scenario badge:

- verificato caso di badge storico + badge attivo
- confermato che l'anagrafica `employees` puo richiedere update mirati
- confermato che i report admin richiedevano adattamenti per badge storici/ex

## 11. Stato Attuale

Ad oggi il progetto si trova in questo stato:

- storico `245` e `246` ricostruito nel DB corrente
- base dati device pulita e coerente
- sync live P1 implementata
- logging sync estensivo implementato
- configurazione Docker aggiornata
- gestione badge smarrito documentata e operativa
- frontend admin adattato per badge storici/ex consultabili
- backend attendances corretto per filtri multi-ID
- tema `extraMarks` / record compressi ancora da rifinire

## 12. Prossimo Passo Consigliato

Il prossimo step naturale resta il `P2` frontend:

- migliorare la visualizzazione di `extraMarks`
- rendere piu evidente in admin e/o nel portale dipendenti che alcune marcature esistono ma non entrano nelle colonne fisse

Altri passi utili:

- stabilizzare una procedura standard per futuri badge persi
- valutare se rendere configurabile da UI la lista degli ID storici consultabili
- valutare una distinzione formale tra account attivi e account storico-soltanto

## 13. Piano Deploy Produzione

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
9. Solo dopo stabilizzazione, valutare eventuale pulizia dello storico dai dispositivi.

## 14. Nota Operativa Importante

I dati dai dispositivi non vanno cancellati immediatamente.

Prima va confermato che:

- la sync live nuova e stabile
- i log non mostrano timeout ripetuti critici
- il frontend rende correttamente i casi critici
- il deploy produzione e allineato al DB corretto

Solo a quel punto la cancellazione dati dai device potra essere valutata come passo finale.

## 15. Checklist Operativa

### Fase 0 - Diagnosi iniziale

- [x] Ricostruito il contesto tecnico del progetto
- [x] Analizzati backend, frontend, auth, sync e runtime Docker
- [x] Verificata la presenza di piu database locali
- [x] Distinto il DB reale dal DB post-delete
- [x] Identificato il problema dello storico ibrido con record device legacy senza metadati

### Fase 1 - Strategia dati

- [x] Decisa la strategia `Excel = storico`, `Anviz = live`
- [x] Scelto di non usare il device come unica fonte per il refill storico
- [x] Confermato che la delete massiva non era il primo passo sicuro

### Fase 2 - Import storico

- [x] Importato lo storico del `246`
- [x] Importato lo storico del `245`
- [x] Popolati `device_id` e `raw_device_timestamp`
- [x] Integrati delta temporali di transizione
- [x] Verificato allineamento raw/final

### Fase 3 - Sync live e logging

- [x] Implementata nuova strategia di sync live
- [x] Distinta sync automatica da sync manuale
- [x] Impostata sync automatica su `new-records`
- [x] Impostata sync manuale su `all-records`
- [x] Disattivata staff sync automatica
- [x] Lasciata attiva staff sync manuale
- [x] Aggiornato `docker-compose.yml`
- [x] Verificata build con `go build ./...`

### Fase 4 - Badge smarrito / ex storici

- [x] Analizzato un caso reale con badge multipli persi
- [x] Verificata una sync staff manuale completa
- [x] Confermato che la staff sync non riallineava l'anagrafica come atteso
- [x] Scelto di non fare merge della tabella `records`
- [x] Documentata la procedura DB locale e produzione
- [x] Aggiornato il frontend per rendere visibili `33` e `56`
- [x] Evitata la generazione di righe vuote sintetiche per `33` e `56`
- [x] Corretto il backend per filtri attendances multi-ID
- [x] Creato documento operativo dedicato

### Fase 5 - Ancora aperto

- [ ] Miglioramento UI per mostrare meglio `extraMarks`
- [ ] Test locale end-to-end con sync live reale
- [ ] Valutazione di una gestione configurabile per futuri badge storici/ex
- [ ] Preparazione del pacchetto di deploy produzione finale
- [ ] Backup completo della produzione prima del rilascio
- [ ] Monitoraggio dei primi cicli automatici post-deploy
- [ ] Verifica finale dell'assenza di buchi nel periodo di transizione Excel -> live
- [ ] Decisione finale sulla pulizia dei dispositivi
