# Procedura Gestione Badge Anviz e Storico

Data: 8 Giugno 2026

## 1. Scopo

Questo documento definisce una procedura minima e sicura per gestire:

- badge persi
- badge ritrovati
- badge riassegnati
- cambio ID Anviz
- conservazione dello storico presenze
- superamento degli alias configurati in `.env`

Il vincolo operativo fondamentale e:

- `ID Anviz + seriale card` sono una coppia indivisibile

Inoltre:

- non viene mai assegnata contemporaneamente la stessa coppia `ID Anviz + seriale card`
- la stessa persona deve poter marcare sia a Napoli sia a Ferrara
- la sede non deve essere modellata nella storia badge

## 2. Problema Attuale

Oggi il sistema usa `employees.id` come identificativo principale del dipendente.

Questo diventa fragile quando:

- una persona perde il badge e riceve un nuovo ID/card
- il vecchio badge viene ritrovato
- una coppia ID/card viene riassegnata piu avanti
- gli alias diventano tanti e non piu gestibili in `.env`

Con la soluzione attuale, gli alias fanno un merge logico tra ID diversi.

Esempio:

```env
EMPLOYEE_ALIAS_MAPPINGS=56>58,33>58
```

Questa soluzione e utile come ponte temporaneo, ma non basta per gestire le riassegnazioni nel tempo.

## 3. Principio Corretto

Il sistema deve distinguere due cose:

- persona reale
- coppia storica `ID Anviz + seriale card`

Le timbrature hardware devono restare come arrivano dal device.

Lo storico deve essere risolto in lettura tramite intervalli temporali:

```text
questa coppia ID/card apparteneva a questa persona da questa data a questa data
```

La sede non va messa in questa struttura.

La sede si ricava gia dai dati di timbratura, tramite `device_id`.

## 4. Regola Di Sicurezza

La regola piu importante e:

- non fare merge fisici delle timbrature come procedura standard

Motivo:

- `records` contiene lo storico visibile
- `device_raw_records` contiene la barriera di deduplica hardware
- se si modifica la parte raw, una sync `all-records` puo reimportare eventi gia noti
- se si spostano timbrature senza una logica temporale, si perde la distinzione tra proprietari diversi dello stesso badge nel tempo

Quindi la strategia ordinaria deve essere:

- lasciare le timbrature con l'ID originale
- collegare quell'ID alla persona corretta in base alla data
- mostrare i report aggregati per persona

## 5. Modello Dati Minimo

Per partire non serve una struttura pesante.

Servono solo due tabelle.

### 5.1 Tabella Persone

```sql
CREATE TABLE people (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    display_name TEXT NOT NULL
);
```

Questa tabella rappresenta la persona reale.

Non coincide con l'ID Anviz.

### 5.2 Tabella Storico Badge

```sql
CREATE TABLE person_badge_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    person_id INTEGER NOT NULL,
    anviz_employee_id INTEGER NOT NULL,
    card_serial TEXT,
    valid_from DATETIME NOT NULL,
    valid_to DATETIME,
    FOREIGN KEY(person_id) REFERENCES people(id)
);
```

Questa tabella dice:

- quale persona
- quale `anviz_employee_id`
- quale `card_serial`
- in quale intervallo temporale

Per i dati legacy il `card_serial` puo restare vuoto finche non viene recuperato.

## 6. Vincoli Applicativi Minimi

Il backend o lo script devono impedire:

- due assegnazioni aperte dello stesso `anviz_employee_id`
- due assegnazioni aperte dello stesso `card_serial`, se noto
- due intervalli sovrapposti per lo stesso `anviz_employee_id`
- due intervalli sovrapposti per lo stesso `card_serial`, se noto

SQLite non rende semplice esprimere questi controlli solo con vincoli statici.

Quindi il controllo va fatto prima della scrittura.

## 7. Risoluzione Dello Storico

Quando il sistema legge una timbratura device:

```text
records.employee_id
records.timestamp
```

deve cercare nella tabella `person_badge_history` la riga valida in quel momento:

```sql
anviz_employee_id = records.employee_id
AND valid_from <= records.timestamp
AND (valid_to IS NULL OR records.timestamp < valid_to)
```

Risultato:

- lo stesso `employee_id` puo appartenere a una persona fino a una certa data
- poi puo appartenere a un'altra persona da una data successiva
- i report mostrano lo storico giusto senza spostare fisicamente le timbrature

## 8. Procedura: Badge Perso

Scenario:

- una persona perde il badge
- riceve una nuova coppia `ID Anviz + seriale card`

Procedura:

1. Chiudere l'intervallo attivo del vecchio badge con `valid_to`.
2. Creare una nuova riga in `person_badge_history` per il nuovo badge.
3. Impostare `valid_from` alla data/ora in cui il nuovo badge entra in uso.
4. Verificare che non ci siano sovrapposizioni.

Non serve spostare nessuna timbratura.

## 9. Procedura: Badge Ritrovato

Scenario:

- un vecchio badge viene ritrovato dopo essere stato sostituito

Possibilita:

- non viene piu usato
- torna alla stessa persona
- viene riassegnato piu avanti a un'altra persona

### 9.1 Ritrovato Ma Non Riutilizzato

Non serve fare nulla sullo storico oltre a lasciare chiuso l'intervallo precedente.

### 9.2 Ritrovato E Riutilizzato Dalla Stessa Persona

Procedura:

1. Chiudere l'intervallo del badge sostitutivo.
2. Creare un nuovo intervallo per il vecchio badge sulla stessa persona.
3. Impostare `valid_from` alla data di riattivazione.

### 9.3 Ritrovato E Riassegnato A Un'Altra Persona

Procedura:

1. Verificare che l'intervallo precedente sia chiuso.
2. Verificare che non ci siano timbrature che rendono ambiguo il passaggio.
3. Creare un nuovo intervallo sulla nuova persona.
4. Impostare `valid_from` alla data/ora esatta di riassegnazione.

Questa operazione diventa sicura solo quando il backend usa davvero la risoluzione temporale.

## 10. Fase Transitoria Con Codice Attuale

Finche il codice non usa ancora `person_badge_history` nelle query:

- gli alias possono restare come ponte temporaneo
- non bisogna riassegnare lo stesso badge a un'altra persona senza prima registrare bene il cambio
- non bisogna fare merge fisici manuali in produzione
- non bisogna aggiornare `device_raw_records`

Regola pratica:

- se un vecchio badge resta solo storico, l'alias e accettabile
- se un vecchio badge viene riassegnato a un'altra persona, l'alias da solo non basta piu

## 11. Migrazione Dagli Alias Esistenti

Gli alias attuali devono diventare righe di storico, non restare configurazione permanente.

Esempio:

```env
EMPLOYEE_ALIAS_MAPPINGS=33>58,56>58
```

Interpretazione:

- esiste una persona canonica
- `33`, `56` e `58` appartengono o sono appartenuti a quella persona in periodi diversi

Procedura minima:

1. Creare una persona canonica in `people`.
2. Creare una riga in `person_badge_history` per ogni `employee_id` coinvolto.
3. Mettere `valid_from` e `valid_to` quando la data e nota.
4. Se la data non e nota, usare un intervallo provvisorio che poi verra corretto.
5. Tenere l'alias in `.env` solo durante la transizione.

## 12. Script Operativi Da Creare

Per evitare SQL manuale sui server, conviene usare script Go.

Nota stato repository al 19 Giugno 2026:

- gli script sotto sono pianificati ma non sono ancora presenti in questo repository
- i comandi mostrati in questa sezione sono quindi il target operativo previsto, non tool gia disponibili

Script consigliati:

```text
scripts/export_badge_history_template.go
scripts/import_badge_history.go
scripts/audit_badge_history.go
```

### 12.1 Export Template

Comando previsto:

```bash
go run scripts/export_badge_history_template.go -db runtime/data/attendance.db -out badge_history.csv
```

Lo script deve esportare una base gia precompilata con:

- `anviz_employee_id`
- nome attuale
- primo timestamp visto
- ultimo timestamp visto
- alias gia noti

In questo modo il CSV non va compilato da zero a mano.

### 12.2 Import

Comando previsto:

```bash
go run scripts/import_badge_history.go -db runtime/data/attendance.db -file badge_history.csv -dry-run
go run scripts/import_badge_history.go -db runtime/data/attendance.db -file badge_history.csv -apply
```

Il file puo restare molto semplice.

Esempio:

```csv
person_key,display_name,anviz_employee_id,card_serial,valid_from,valid_to
P001,MARIO ROSSI,33,123456,2024-01-01T00:00:00+01:00,2025-03-10T08:00:00+01:00
P001,MARIO ROSSI,56,789012,2025-03-10T08:00:00+01:00,2026-06-01T09:00:00+02:00
P001,MARIO ROSSI,58,456789,2026-06-01T09:00:00+02:00,
```

Il dry-run deve segnalare:

- intervalli sovrapposti
- `employee_id` doppi nello stesso periodo
- `card_serial` doppi nello stesso periodo
- righe senza persona canonica

### 12.3 Audit

Comando previsto:

```bash
go run scripts/audit_badge_history.go -db runtime/data/attendance.db
```

Deve mostrare:

- badge con piu persone nello stesso periodo
- persone con piu badge attivi nello stesso momento
- intervalli aperti incoerenti
- alias ancora presenti in `.env`

## 13. Operazioni Sul Server

Non e obbligatorio installare `sqlite3` sul server.

La strada preferita e:

- usare script Go del repository
- usare `go run` o binari compilati

Alternative:

- usare un container temporaneo `keinos/sqlite3` per emergenze
- installare `sqlite3` solo se serve ispezione manuale

## 14. Backup Obbligatorio

Prima di ogni modifica strutturale o massiva:

1. Fermare temporaneamente l'applicazione.
2. Creare una cartella backup manuale.
3. Copiare `attendance.db`.
4. Copiare `attendance.db-wal` se presente.
5. Copiare `attendance.db-shm` se presente.
6. Eseguire il dry-run.
7. Applicare solo se il dry-run e coerente.
8. Riavviare l'applicazione.
9. Eseguire verifica report.

## 15. Merge Fisico Delle Timbrature

Il merge fisico deve restare eccezionale.

E consentito solo quando:

- un vecchio badge non verra piu riutilizzato
- si vuole consolidare uno storico gia chiuso
- esiste un backup completo
- il dry-run non trova collisioni
- si modifica solo `records`
- non si modifica `device_raw_records`

Questa non deve essere la procedura ordinaria.

## 16. Modifiche Backend Necessarie

Il backend dovra evolvere cosi:

1. Aggiungere `people`.
2. Aggiungere `person_badge_history`.
3. Importare gli alias esistenti in questa struttura.
4. Creare un resolver per timestamp.
5. Usare il resolver nelle query report.
6. Lasciare invariata la deduplica hardware.

Punti da non cambiare:

- `device_raw_records` resta sorgente raw/deduplica
- le sync continuano a importare eventi come arrivano dal device

## 17. Decisione Operativa Raccomandata

La procedura ordinaria deve diventare:

- non spostare timbrature
- non usare gli alias in `.env` come soluzione definitiva
- registrare la storia `persona <-> badge` nel tempo
- risolvere i report in base alla data della timbratura
- usare merge fisico solo come eccezione

Questo e il modello minimo corretto: piccolo, gestibile e sufficiente per non perdere lo storico quando i badge cambiano.
