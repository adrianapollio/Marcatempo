Scripts operativi del repository.

Tool usati dal build/runtime:
- `make_admin.go`
- `make_system_admin.go`
- `insert_device_record.go`
- `import_anviz_extract.go`
- `fix_anviz_extract_dst.go`

Tool operativi manuali:
- `audit_badge_migration.go` (audit read-only preliminare dei DB di deploy)
- `reassign_badge.go` (riassegnazione temporale con dry-run, backup e audit)
- `replace_device_from_extract.go`
- `shift_device_records.go`
- `dump_user_records.go`
- `check_missing.go`
- `recover_from_excel.go`
- `debug_excel.go`

Note di pulizia:
- binari compilati, cache Go e file temporanei non vanno versionati
- gli script storici one-off di manutenzione DB sono stati rimossi dal percorso operativo

Audit preliminare migrazione badge:

```bash
go run audit_badge_migration.go -db ../runtime/data/attendance.db -label locale
```

Per il deploy viene costruita un'immagine di audit separata, senza modificare
l'immagine o il container dell'applicazione:

```bash
docker build -f Dockerfile.audit \
  -t zibola/marcatempo-audit:20260720-v2 .

docker run --rm --read-only --network none \
  -v "$PWD/runtime/data:/data:ro" \
  zibola/marcatempo-audit:20260720-v2 \
  -db /data/attendance.db -label Napoli
```

Export CSV read-only per costruire il prospetto storico (contiene nomi ma non
PIN o password):

```bash
docker run --rm --read-only --network none \
  -v "$PWD/runtime/data:/data:ro" \
  zibola/marcatempo-audit:20260720-v2 \
  -db /data/attendance.db -export-csv > badge-inventory.csv
```

Il database viene aperto con `mode=ro` e `query_only`; il tool non stampa PIN
o password e non applica migrazioni.

Migrazione badge (tool separato dall'applicazione):

```bash
docker build -f Dockerfile.badge-migration \
  -t zibola/marcatempo-badge-migration:20260720 .
```

Senza `-apply` il comando esegue esclusivamente il preflight:

```bash
docker run --rm --network none \
  -v "$PWD/runtime/data:/data" \
  -v "$PWD/runtime/imports:/imports:ro" \
  zibola/marcatempo-badge-migration:20260720 \
  -db /data/attendance.db \
  -file /imports/badge-history-approved.csv
```

L'apply richiede sempre un percorso di backup nuovo e crea il backup con
`VACUUM INTO` prima di aprire la transazione di migrazione.

Modalita shadow nell'applicazione:

Dopo aver applicato e verificato la migrazione DB versione 1, impostare nel
file `.env` del deploy:

```bash
BADGE_HISTORY_MODE=shadow
```

Al successivo avvio l'applicazione legge `people` e
`person_badge_history`, confronta la risoluzione temporale di ogni riga con
gli alias correnti e scrive solo un riepilogo nei log. Non modifica record,
risposte API, login o filtri. `BADGE_HISTORY_MODE=off` disabilita il controllo.

```bash
docker logs --since 10m marcatempo_app 2>&1 | grep BADGE_HISTORY_SHADOW
```

Risultati attesi dopo l'import approvato:

- Napoli: `records` 37296 coerenti e 40 non gestiti; raw 43147 coerenti e 42
  non gestiti.
- Ferrara: `records` 4397 coerenti e 2 non gestiti; raw 4332 coerenti e 2
  non gestiti.
- In entrambi i casi: zero differenze, intervalli assenti, ambiguita e
  timestamp invalidi. Le righe non gestite appartengono all'ID test 156.

Gli alias devono rimanere attivi durante tutta la fase shadow.

Gestione futura di sostituzioni e riuso badge:

L'immagine dedicata non contiene l'applicazione web e non partecipa al normale
deploy:

```bash
docker build -f Dockerfile.badge-admin \
  -t zibola/marcatempo-badge-admin:20260720 .
```

Inventario delle assegnazioni correnti, senza PIN o password:

```bash
docker run --rm --read-only --network none \
  --tmpfs /tmp:rw,size=128m -e TMPDIR=/tmp \
  -v "$PWD/runtime/data:/data:ro" \
  zibola/marcatempo-badge-admin:20260720 \
  -db /data/attendance.db -list-current > badge-current.csv
```

Il dry-run richiede persona, ID Anviz, seriale, decorrenza RFC3339, motivo e
operatore. Non scrive sul DB:

```bash
docker run --rm --read-only --network none \
  --tmpfs /tmp:rw,size=128m -e TMPDIR=/tmp \
  -v "$PWD/runtime/data:/data:ro" \
  zibola/marcatempo-badge-admin:20260720 \
  -db /data/attendance.db \
  -person-key P-000001 \
  -anviz-id 126 \
  -card-serial 1234567 \
  -effective-at 2026-07-21T09:00:00+02:00 \
  -reason reassigned \
  -actor operatore
```

Per una persona nuova aggiungere `-create-person -display-name "Nome Cognome"`.
Per il rientro reale di una persona inattiva usare esplicitamente
`-reactivate-person`. Il tool chiude alla stessa decorrenza l'assegnazione del
vecchio proprietario del seriale, l'eventuale badge precedente della persona e
l'eventuale assegnazione precedente dell'ID Anviz. Intervalli futuri
conflittuali bloccano l'operazione.

L'apply deve essere eseguito con l'applicazione ferma, mount dati in
lettura/scrittura, `/tmp` scrivibile e un percorso backup nuovo:

```bash
docker stop marcatempo_app

docker run --rm --read-only --network none \
  --tmpfs /tmp:rw,size=128m -e TMPDIR=/tmp \
  -v "$PWD/runtime/data:/data" \
  zibola/marcatempo-badge-admin:20260720 \
  -db /data/attendance.db \
  -person-key P-000001 \
  -anviz-id 126 \
  -card-serial 1234567 \
  -effective-at 2026-07-21T09:00:00+02:00 \
  -reason reassigned \
  -actor operatore \
  -apply \
  -backup /data/backups/manual/badge-reassign-20260721_090000.db

RC=$?
docker start marcatempo_app
echo "Codice riassegnazione: $RC"
```

Ogni chiusura/creazione viene registrata in `badge_assignment_audit`; il tool
verifica integrita, foreign key e invarianza dei conteggi di `records` e
`device_raw_records`. La riesecuzione della stessa operazione e idempotente.

Durante la fase attuale usare in produzione soltanto `-list-current` e il
dry-run. L'apply di un riuso reale verra abilitato dopo il passaggio dal resolver
alias globale al resolver temporale dell'applicazione.
