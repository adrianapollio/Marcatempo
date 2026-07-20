Scripts operativi del repository.

Tool usati dal build/runtime:
- `make_admin.go`
- `make_system_admin.go`
- `insert_device_record.go`
- `import_anviz_extract.go`
- `fix_anviz_extract_dst.go`

Tool operativi manuali:
- `audit_badge_migration.go` (audit read-only preliminare dei DB di deploy)
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
  -t zibola/marcatempo-audit:20260720 .

docker run --rm --read-only --network none \
  -v "$PWD/runtime/data:/data:ro" \
  zibola/marcatempo-audit:20260720 \
  -db /data/attendance.db -label Napoli
```

Il database viene aperto con `mode=ro` e `query_only`; il tool non stampa PIN
o password e non applica migrazioni.
