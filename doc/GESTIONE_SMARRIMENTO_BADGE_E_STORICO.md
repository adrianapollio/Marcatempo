# Gestione Smarrimento Badge e Storico Ex Dipendenti

Data: 25 Marzo 2026

## 1. Scopo

Questo documento descrive come gestire il caso in cui un dipendente perda uno o piu badge Anviz e riceva un nuovo ID, senza accorpare fisicamente lo storico nel database.

L'obiettivo operativo e:

- mantenere separati i record storici dei vecchi badge
- usare il nuovo badge per le timbrature correnti
- continuare a consultare i vecchi ID nei report admin
- evitare assenze sintetiche giornaliere per badge storici non piu attivi

## 2. Caso Reale Analizzato

Nel caso gestito:

- `33` era un vecchio badge storico del dipendente
- `56` era un altro badge storico del dipendente
- `58` e il badge attivo corrente

Situazione desiderata:

- `33` deve restare consultabile solo come storico
- `56` deve restare consultabile solo come storico
- `58` deve essere l'account attivo per le timbrature correnti

## 3. Problematica Emersa

Il sistema salva le timbrature in `records` usando direttamente `employee_id`.

Questo comporta che:

- i vecchi badge (`33`, `56`) hanno il proprio storico separato
- il nuovo badge (`58`) ha le nuove timbrature
- non esiste una logica nativa di merge tra badge

Durante l'analisi e emerso anche che:

- la lista record Anviz mostrava correttamente i nomi per le timbrature
- la sync staff non ha riallineato i nomi in `employees` come atteso
- la sync manuale eseguiva i `staff_chunks`, ma il risultato finale su `employees` restava invariato

Conclusione pratica:

- non conviene affidarsi alla sync staff per sistemare questo scenario
- e preferibile un aggiornamento mirato della tabella `employees`

## 4. Decisione Adottata

E stata scelta una strategia conservativa:

- nessun merge dei record storici
- nessun `UPDATE` della tabella `records`
- aggiornamento solo della tabella `employees`
- adattamento frontend per mostrare gli ID storici ex nei filtri
- rimozione delle righe vuote sintetiche per gli ID storici

Questa strategia riduce i rischi in produzione perche:

- non mescola lo storico di badge diversi
- non altera i record grezzi/importati
- richiede solo interventi mirati su anagrafica e UI

## 5. Aggiornamenti DB Eseguiti

### 5.1 Obiettivo

Rendere coerente l'anagrafica locale:

- `33` -> `EX DI MEO`
- `56` -> `EX DI MEO`
- `58` -> `DI MEO L`
- PIN attivo `1755` assegnato al `58`
- PIN dei badge storici svuotato

### 5.2 Query SQL per DB locale

Eseguire:

```sql
UPDATE employees
SET name = 'EX DI MEO',
    pin = ''
WHERE id = 33;

UPDATE employees
SET name = 'EX DI MEO',
    pin = ''
WHERE id = 56;

UPDATE employees
SET name = 'DI MEO L',
    pin = '1755'
WHERE id = 58;

SELECT id, pin, name, is_admin
FROM employees
WHERE id IN (33, 56, 58);
```

### 5.3 Procedura per produzione via server

Dal server Linux:

1. Fermare il container applicativo:

```bash
docker compose stop marcatempo
```

2. Fare backup del database:

```bash
cp data/attendance.db data/attendance.db.bak-2026-03-25
```

3. Aprire il DB con un container SQLite temporaneo:

```bash
docker run --rm -it --user root -v "$PWD/data:/data" keinos/sqlite3 sqlite3 /data/attendance.db
```

4. Eseguire dentro SQLite:

```sql
UPDATE employees
SET name = 'EX DI MEO',
    pin = ''
WHERE id = 33;

UPDATE employees
SET name = 'EX DI MEO',
    pin = ''
WHERE id = 56;

UPDATE employees
SET name = 'DI MEO L',
    pin = '1755'
WHERE id = 58;

SELECT id, pin, name, is_admin
FROM employees
WHERE id IN (33, 56, 58);

.quit
```

5. Riavviare i container:

```bash
docker compose up -d
```

### 5.4 Nota sugli errori incontrati

Durante l'operazione in produzione sono emersi questi errori:

- `sqlite3: not found`
- `attempt to write a readonly database`

Soluzione adottata:

- usare il container `keinos/sqlite3`
- eseguirlo con `--user root`

## 6. Limiti della Sync Staff Anviz

Configurazione attuale in `docker-compose.yml`:

- `ANVIZ_AUTO_SYNC_STAFF=false`
- `ANVIZ_MANUAL_SYNC_STAFF=true`

E stata lanciata una sync manuale completa.

Dai log:

- la fase staff e stata effettivamente eseguita
- i `staff_chunks` sono stati completati prima dei timeout sui chunk attendance
- nonostante questo, i nomi in `employees` non sono stati riallineati come atteso

Conclusione:

- abilitare `ANVIZ_AUTO_SYNC_STAFF=true` non e stato considerato utile
- avrebbe solo ripetuto automaticamente una sync staff che non stava correggendo il problema

## 7. Cambiamenti Frontend Introdotti

I cambiamenti sono stati applicati in `admin.html`.

### 7.1 Visibilita degli ID storici ex

Per impostazione standard il frontend nasconde:

- nomi che iniziano per `EX`
- nomi che iniziano per `Nome vuoto`

Per il caso badge smarrito sono stati aggiunti come eccezioni:

- `33`
- `56`

Risultato:

- gli ID storici restano visibili nella pagina admin
- gli ex normali continuano a restare nascosti

### 7.2 Multiselect dipendenti

La multiselect aveva un filtro separato che escludeva comunque tutti gli `EX`.

E stata quindi aggiornata anche la logica di `populateEmployees()` per consentire:

- visualizzazione di `33`
- visualizzazione di `56`

anche se il nome inizia per `EX`.

### 7.3 Nessuna riga vuota sintetica per gli ID storici

La tabella admin genera normalmente righe vuote giornaliere per i giorni feriali senza timbrature.

Questo andava bene per i dipendenti attivi, ma non per i badge storici `33` e `56`.

Per questi due ID e stata introdotta una regola speciale:

- devono restare visibili se hanno marcature reali
- non devono generare righe vuote di assenza

Risultato:

- lo storico reale resta consultabile
- non vengono mostrate assenze finte ogni giorno

### 7.4 Impatto sul riepilogo periodo

Il riepilogo periodo usa gli stessi dati giornalieri preprocessati.

Con la modifica:

- `33` e `56` non ricevono piu giorni vuoti artificiali
- il riepilogo considera solo i giorni con marcature reali
- si evita di penalizzare il bilancio con assenze sintetiche

Questo rende il riepilogo veritiero per lo storico effettivamente presente nel DB.

## 8. Correzione Backend per la Multiselect

E stato corretto anche il backend in `db.go`.

Problema precedente:

- la multiselect inviava `employee_id=33,56` o `56,58`
- il backend filtrava con `employee_id = ?`
- quindi la query non trovava record reali per piu ID insieme

Correzione introdotta:

- supporto a liste di ID separate da virgola
- conversione del filtro in `employee_id IN (?, ?, ...)`

Risultato:

- la multiselect ora funziona correttamente anche con piu dipendenti selezionati

## 9. Procedura Consigliata per Futuri Smarrimenti Badge

Quando si ripresenta il caso di badge perso:

1. Verificare sull'Anviz quali ID storici appartengono alla stessa persona.
2. Non accorpare i record storici in `records`, salvo necessita specifiche.
3. Aggiornare la tabella `employees`:
   - vecchi badge -> `EX NOME`
   - nuovo badge -> nome attivo + PIN attivo
4. Se i vecchi badge devono restare consultabili:
   - aggiungerli alle eccezioni frontend
5. Se i vecchi badge sono solo storico:
   - evitare generazione di righe vuote sintetiche
6. Se si usano filtri multipli:
   - verificare che il backend supporti piu ID nel parametro `employee_id`

## 10. Quando NON fare il merge dei record

E preferibile NON fare merge di `records` quando:

- si vuole conservare lo storico dei badge persi come separato
- il nuovo badge e gia attivo e registrera le presenze correnti
- si vuole evitare rischio di collisioni o deduplicazioni inattese

Il merge dei record va considerato solo se si desidera unificare forzatamente tutta la storia su un unico ID.

## 11. File Coinvolti

I file interessati da questo scenario sono:

- `admin.html`
- `db.go`
- `docker-compose.yml`
- `data/attendance.db`

## 12. Stato Finale Atteso

Alla fine della procedura, il sistema deve comportarsi cosi:

- `58` appare come badge attivo del dipendente
- `33` e `56` restano consultabili come storico
- `33` e `56` compaiono nei filtri e nella multiselect
- `33` e `56` non generano nuove righe vuote di assenza
- il riepilogo periodo dei badge storici usa solo le marcature realmente presenti
- la multiselect con piu dipendenti selezionati recupera correttamente i record reali
