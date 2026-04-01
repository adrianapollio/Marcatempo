# Runtime Data

Questa directory non deve contenere file versionati di produzione.

Uso corretto:

- `attendance.db` creato localmente dall'applicazione
- backup locali della singola sede
- eventuali file di import temporanei solo fuori da Git

Se usi Docker Compose con la configurazione attuale, il percorso raccomandato e una
directory runtime esterna alla repository, configurata tramite `MARCATEMPO_RUNTIME_DIR`.
