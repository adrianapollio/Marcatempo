# Riallineamento Ferrara Anviz

Data: 2026-06-01

## Scopo

Questa procedura riallinea le timbrature gia sincronizzate dal device Anviz di Ferrara quando il terminale non era ancora configurato con il flusso semplificato.

Il file Excel esportato dal device contiene ancora gli stati legacy, per esempio:

- `ENTRATA`
- `USCITA`
- `I_PAUSA`
- `F_PAUSA`
- `U_TRASFER`
- `R_TRASFER`

Lo script non importa questi stati come azioni finali. Li converte prima nei raw semplificati usati oggi:

- `ENTRATA`, `USCITA`, `U_TRASFER`, `R_TRASFER` -> `in_out`
- `I_PAUSA`, `F_PAUSA` -> `pausa`

Poi ricostruisce le azioni finali con la stessa logica del resolver:

- `In`
- `Out`
- `I_pausa`
- `F_pausa`

## Dati attesi

- Device Ferrara: `device_id=3`
- DB attivo in deploy lato host: `runtime/data/attendance.db`
- DB attivo nel container: `/app/data/attendance.db`
- Excel usato per il riallineamento: `20260601_R.xlsx`
- Range rilevato nel file: `2025-02-27 08:45:12` -> `2026-06-01 13:07:05`
- Righe valide rilevate: `3423`

## 1. Preparazione sul server

Entrare nella repository sul server Ferrara:

```bash
cd ~/Marcatempo
```

Verificare di avere l'ultima versione del codice:

```bash
git fetch origin
git status --short
git pull --ff-only origin dev
```

Creare una cartella per gli import manuali, se non esiste:

```bash
mkdir -p runtime/imports
```

Mettere il file Excel in:

```text
runtime/imports/20260601_R.xlsx
```

## 2. Fermare la sync/app

Fermare il container applicativo prima di lavorare sul DB.

Con compose standard:

```bash
docker compose stop marcatempo
```

Con compose host network, se quello e lo stack usato sul server:

```bash
docker compose -f docker-compose.host.yml stop marcatempo
```

Il proxy nginx puo restare avviato, ma durante questa finestra l'app non rispondera correttamente.

## 3. Backup del database

Creare una cartella di backup dedicata:

```bash
mkdir -p runtime/data/backups/manual/ferrara-realign-20260601
```

Copiare il DB e gli eventuali file WAL/SHM:

```bash
cp -a runtime/data/attendance.db runtime/data/backups/manual/ferrara-realign-20260601/
cp -a runtime/data/attendance.db-wal runtime/data/backups/manual/ferrara-realign-20260601/ 2>/dev/null || true
cp -a runtime/data/attendance.db-shm runtime/data/backups/manual/ferrara-realign-20260601/ 2>/dev/null || true
```

Verificare il backup:

```bash
ls -lh runtime/data/backups/manual/ferrara-realign-20260601/
```

## 4. Dry run

Entrare nella cartella degli script:

```bash
cd scripts
```

Eseguire il dry-run:

```bash
go run ./replace_device_from_extract.go \
  -file ../runtime/imports/20260601_R.xlsx \
  -device-id 3 \
  -db ../runtime/data/attendance.db
```

Il dry-run non modifica nulla.

Controllare che mostri:

- `righe=3423`
- `dipendenti=26`
- range `2025-02-27T08:45:12+01:00 -> 2026-06-01T13:07:05+02:00`
- raw semplificati simili a:

```text
in_out/raw_status_0 = 1223
in_out/raw_status_1 = 1201
in_out/raw_status_4 = 38
in_out/raw_status_5 = 40
pausa/raw_status_2 = 462
pausa/raw_status_3 = 459
```

Controllare anche `DB nel range extract`. Questo numero indica quante timbrature Ferrara verranno sostituite nel range dell'Excel.

## 5. Applicazione

Se il dry-run e coerente, applicare:

```bash
go run ./replace_device_from_extract.go \
  -file ../runtime/imports/20260601_R.xlsx \
  -device-id 3 \
  -db ../runtime/data/attendance.db \
  -apply
```

Lo script lavora in transazione:

- cancella da `device_raw_records` solo `device_id=3` nel range dell'Excel
- cancella da `records` solo `source='device'` e `device_id=3` nel range dell'Excel
- reinserisce i raw semplificati
- reinserisce i record finali calcolati dal resolver

Alla fine deve stampare:

```text
Riallineamento completato: cancellati records=... raw=..., inseriti records=3423 raw=3423
```

## 6. Verifica immediata

Rilanciare il dry-run dopo l'applicazione:

```bash
go run ./replace_device_from_extract.go \
  -file ../runtime/imports/20260601_R.xlsx \
  -device-id 3 \
  -db ../runtime/data/attendance.db
```

Controllare:

- `DB nel range extract: records=3423 raw=3423`
- `DB raw actions` contiene solo raw `in_out` e `pausa`
- `DB record actions` contiene azioni finali tra `In`, `Out`, `I_pausa`, `F_pausa`

## 7. Riavvio applicazione

Tornare alla root della repository:

```bash
cd ..
```

Riavviare il servizio applicativo.

Con compose standard:

```bash
docker compose up -d marcatempo
```

Con compose host network:

```bash
docker compose -f docker-compose.host.yml up -d marcatempo
```

Controllare i log:

```bash
docker logs --tail 100 marcatempo_app
```

## 8. Verifica funzionale

Da interfaccia:

- aprire alcune giornate Ferrara gia presenti nell'Excel
- verificare entrata/uscita
- verificare inizio/fine pausa
- controllare almeno un dipendente con trasferte legacy, perche ora vengono incanalate nel flusso `in_out`

## 9. Rollback

Se qualcosa non torna, fermare di nuovo l'app:

```bash
docker compose stop marcatempo
```

Oppure:

```bash
docker compose -f docker-compose.host.yml stop marcatempo
```

Ripristinare i file dal backup:

```bash
cp -a runtime/data/backups/manual/ferrara-realign-20260601/attendance.db runtime/data/
cp -a runtime/data/backups/manual/ferrara-realign-20260601/attendance.db-wal runtime/data/ 2>/dev/null || true
cp -a runtime/data/backups/manual/ferrara-realign-20260601/attendance.db-shm runtime/data/ 2>/dev/null || true
```

Riavviare:

```bash
docker compose up -d marcatempo
```

Oppure:

```bash
docker compose -f docker-compose.host.yml up -d marcatempo
```

## Nota su Go nel server

La procedura usa `go run`. Se sul server non e installato Go, compilare lo script su una macchina compatibile oppure eseguire il riallineamento su una copia del DB presa dal deploy, poi riportare il DB riallineato sul server mantenendo lo stesso backup di sicurezza.
