# Guida Al Deploy Di Marcatempo

Questa guida descrive il deploy dopo la separazione tra:

- codice della repository
- dati runtime
- certificati
- configurazione locale di sede

Il principio e semplice: `git clone` deve portare solo codice e file di esempio, non database reali, backup o certificati di produzione.

## 1. Struttura raccomandata

Repository:

- contiene codice, documentazione, `docker-compose.yml`, `nginx.conf`, `.env.example`

Directory runtime della macchina:

- contiene `data/`
- contiene `data/backups/`
- contiene `certs/`
- non viene versionata in Git

Esempio:

```text
/opt/marcatempo/
  docker-compose.yml
  ...

/opt/marcatempo-runtime/
  data/
  data/backups/
  certs/
    server.crt
    server.key
```

Nel `docker-compose.yml` la directory runtime viene montata tramite `MARCATEMPO_RUNTIME_DIR`.

## 2. File `.env`

Ogni server deve avere un solo file attivo chiamato `.env`.

Esempio operativo:

- server Napoli -> `.env` con configurazione Napoli
- server Ferrara -> `.env` con configurazione Ferrara

In locale puoi tenere piu file con nomi diversi, per esempio:

- `.env.napoli`
- `.env.ferrara`

e copiarne uno alla volta come `.env` sul server corretto.

## 3. Primo deploy su una nuova sede

1. Clona la repository.
2. Crea la directory runtime della sede.
3. Copia nella directory runtime i certificati reali:
   - `server.crt`
   - `server.key`
4. Prepara il file `.env` della sede.
   Imposta in particolare:
   - `ANVIZ_SYNC_ENABLED=false` al primo avvio
   - `ANVIZ_DEVICES=` con i soli device della sede corrente
   - opzionalmente `ANVIZ_ACTIVE_DEVICE_IDS=` se vuoi attivarne solo un sottoinsieme
5. Lascia `ANVIZ_SYNC_ENABLED=false` al primo avvio.
6. Avvia lo stack:

```bash
docker compose up -d --build
```

7. Verifica che il database venga creato pulito nella directory runtime.
8. Verifica l'accesso system admin.
9. Se il bootstrap non e sufficiente o serve recovery password, usa:

```bash
docker exec -it marcatempo_app ./make_system_admin admin '<nuova_password>'
```

10. Solo dopo le verifiche, abilita la sync dei device della sede corretta.

## 4. Aggiornamento di una sede esistente

1. Aggiorna il codice:

```bash
git pull --ff-only origin dev
```

2. Verifica che il file `.env` della sede sia ancora corretto.
3. Riapplica lo stack:

```bash
docker compose up -d --build
```

Poiche i dati stanno fuori dalla repository, l'aggiornamento del codice non deve sostituire il database o i certificati della sede.

## 5. Certificati

I certificati di produzione non devono stare nella repository.

La procedura corretta e:

1. conservare i certificati in un archivio sicuro aziendale o nel sistema di gestione certificati scelto
2. copiarli sul server nella directory runtime
3. montarli nel container nginx

Non e consigliato committare in Git:

- `server.key`
- `server.crt` di produzione

## 6. Dati e backup

Il database SQLite e i backup:

- non devono stare nella repository
- devono vivere nella directory runtime della sede
- devono essere trattati come dati locali di istanza

Se il file DB viene cancellato e l'applicazione viene riavviata:

- SQLite ricrea il file
- l'app ricrea automaticamente le tabelle di base

Questo permette di far partire una nuova sede con database pulito, a condizione che la sync device resti disattivata finche la configurazione non e pronta.

## 7. Bootstrap system admin

Il system admin usa username fisso:

- `admin`

Il comportamento raccomandato e:

- se il DB e vuoto e `DEFAULT_SYSTEM_ADMIN_PASSWORD` e presente, il bootstrap crea l'utente `admin`
- se `admin` esiste gia, il bootstrap non sovrascrive automaticamente la password
- in caso di recovery si usa il tool `make_system_admin`

Per la diagnostica bootstrap fai riferimento a:

- log applicativi
- comportamento del login
- recovery esplicita tramite `make_system_admin`

## 8. Nota operativa per Napoli e Ferrara

Convenzione consigliata in locale:

- `.env.napoli`
- `.env.ferrara`

Convenzione sui server:

- un solo `.env` attivo per server

Risultato atteso:

- Napoli continua a usare solo la propria configurazione
- Ferrara puo partire con DB vuoto e senza ereditare dati o device della sede originaria
