Scripts operativi del repository.

Tool usati dal build/runtime:
- `make_admin.go`
- `make_system_admin.go`
- `insert_device_record.go`
- `import_anviz_extract.go`
- `fix_anviz_extract_dst.go`

Tool operativi manuali:
- `replace_device_from_extract.go`
- `shift_device_records.go`
- `dump_user_records.go`
- `check_missing.go`
- `recover_from_excel.go`
- `debug_excel.go`

Note di pulizia:
- binari compilati, cache Go e file temporanei non vanno versionati
- gli script storici one-off di manutenzione DB sono stati rimossi dal percorso operativo
