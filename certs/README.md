# Runtime Certificates

Questa directory e solo un placeholder documentale.

I certificati reali di produzione non devono essere committati nella repository.

Per ogni server copia localmente:

- `server.crt`
- `server.key`

in una directory runtime esterna al repository, poi monta quella directory nel
container nginx tramite `MARCATEMPO_RUNTIME_DIR`.
