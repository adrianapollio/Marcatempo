# Piano Nuova Sede E Migrazione Stati

Data: 2026-04-01

## 1. Scopo del documento

Questo documento raccoglie in modo unitario e molto esplicito:

- i problemi emersi durante il deploy su una nuova sede
- i rischi attuali della codebase rispetto a dati, configurazione e sicurezza
- le risposte operative immediate ai dubbi emersi
- il piano di implementazione raccomandato per rendere il progetto adatto a una distribuzione multi-sede
- il percorso per arrivare in sicurezza alla futura semplificazione degli stati descritta in `doc/ANALISI_SEMPLIFICAZIONE_STATI_2026-03-31.md`

L'obiettivo non e solo "far funzionare il nuovo server", ma evitare che una nuova istanza erediti dati, configurazioni e comportamenti della sede originale.

## 2. Sintesi esecutiva

La situazione attuale presenta un problema strutturale: la repository non contiene solo codice applicativo, ma anche dati reali, backup e configurazioni operative che non dovrebbero viaggiare con `git clone`.

Questo produce una serie di effetti collaterali importanti:

- un nuovo server puo partire gia con dati reali della sede originaria
- un database pulito puo venire ripopolato automaticamente dai device della sede sbagliata
- la configurazione dei device e parzialmente hardcoded nel backend
- parte dell'interfaccia `system_admin.html` mostra informazioni statiche che non riflettono la configurazione reale
- il bootstrap del system admin non e sufficientemente esplicito e robusto per installazioni nuove o recovery
- la base attuale non e ancora pronta, cosi com'e, per un rollout pulito verso una nuova societa del gruppo

La direzione raccomandata e la seguente:

1. separare definitivamente codice, dati runtime e segreti
2. rendere il deploy "safe by default"
3. spostare tutta la configurazione dei device fuori dal codice
4. introdurre una convenzione chiara per le configurazioni di sede, nel caso attuale `Napoli` e `Ferrara`
5. rendere il bootstrap del system admin affidabile e verificabile
6. introdurre un minimo di gestione ordinata delle migrazioni schema
7. solo dopo questi passaggi, implementare la semplificazione degli stati device a 3 gruppi raw mantenendo `records` a 6 stati canonici

## 3. Contesto del problema

Durante il deploy su un nuovo server sono emersi i seguenti sintomi:

- mancavano file che in realta erano presenti nel branch `dev`
- il server era stato clonato sul branch `main`, non su `dev`
- dopo il passaggio corretto a `dev`, e stato possibile vedere i file applicativi attesi
- e poi emerso che il server conteneva o poteva contenere dati non desiderati per la nuova societa
- in parallelo si e evidenziato che la sincronizzazione Anviz puo partire verso i device della sede originaria
- inoltre l'accesso system admin non era lineare, nonostante il tentativo di bootstrap tramite `.env`

Il problema del branch e stato solo il primo sintomo. Il problema piu importante e architetturale: il progetto oggi non separa in modo netto:

- codice
- dati reali
- configurazione per sede
- credenziali bootstrap

## 4. Risposte operative gia chiarite

### 4.1 Se cancello il DB, il DB si ricrea pulito automaticamente?

Si, a condizione che l'applicazione venga riavviata e che il path del DB sia scrivibile.

Il comportamento attuale e questo:

- all'avvio `InitDB()` apre il database usando `DB_PATH` oppure, in fallback, `data/attendance.db`
- se il file non esiste, SQLite lo crea
- subito dopo vengono eseguiti i `CREATE TABLE IF NOT EXISTS`
- quindi il database si ricrea automaticamente con le tabelle di base

Questo significa che, tecnicamente, e possibile partire da un DB vuoto.

Pero questa affermazione, da sola, e fuorviante se non si considera il resto del sistema:

- la repository oggi include `data/attendance.db` e backup gia versionati
- la sincronizzazione Anviz puo partire subito e ripopolare il DB
- quindi "cancello il DB e riparto pulito" non e ancora una strategia sicura se non si disattiva prima la sync e non si separano i dati dalla repo

### 4.2 Se cancello anche i backup, si ricrea tutto correttamente?

Si, la directory dei backup viene ricreata al bisogno dal sistema di backup.

Pero anche qui vale la stessa nota:

- il problema non e solo se i backup si ricreano
- il problema e che i backup non dovrebbero stare dentro la repository Git e non dovrebbero essere distribuiti a nuove sedi

### 4.3 Se parto con un DB pulito ma lascio la sync attiva, rischio di importare i dati della sede originale?

Si.

Con il codice attuale:

- i device Anviz sono definiti nel backend come elenco statico
- la sincronizzazione parte automaticamente all'avvio se `ANVIZ_SYNC_ENABLED` e attiva
- il `docker-compose.yml` attuale imposta `ANVIZ_SYNC_ENABLED=true`

Quindi, se il server nuovo riesce a raggiungere quei device, il database nuovo e vuoto verra popolato con dati della sede sbagliata.

Se invece il server non raggiunge quegli IP:

- il DB restera vuoto
- ma il servizio continuera a tentare la sync
- i log saranno rumorosi
- e il deploy restera concettualmente insicuro

## 5. Stato attuale del codice e problemi strutturali

### 5.1 Dati reali e file runtime dentro la repository

Attualmente nella repository risultano versionati file che non dovrebbero far parte del codice sorgente condiviso:

- `data/attendance.db`
- file di backup del DB
- file Excel usati per import o analisi
- almeno parte dei certificati

Questa e la criticita principale per un progetto che deve supportare piu sedi o piu societa.

Effetti negativi:

- un `git clone` porta con se dati reali
- il repository diventa un contenitore misto di codice e dati operativi
- si aumenta il rischio di esposizione di dati sensibili
- si rende difficile distinguere cosa fa parte della release e cosa fa parte dell'istanza locale
- si crea ambiguita nei deploy, nei backup e nelle migrazioni

### 5.2 Configurazione device hardcoded nel backend

Nel backend esiste una lista statica di device Anviz con IP e ID.

Questo significa che:

- la build applicativa contiene una configurazione di sede
- per cambiare sede oggi bisogna modificare il codice o ricompilare un'immagine dedicata
- lo stesso artefatto applicativo non e davvero riusabile in piu contesti

In un sistema multi-sede, la configurazione dei device non deve stare nel codice.

### 5.3 Sync attiva di default nel deploy Docker

Il file `docker-compose.yml` attuale abilita la sync automatica di default.

Questa scelta e accettabile solo se:

- esiste una sola sede
- tutti i deploy sono fatti nello stesso contesto operativo
- non esiste mai il rischio di riuso in altre societa

Nel momento in cui il progetto viene portato su una nuova sede, questa impostazione diventa pericolosa.

La scelta corretta, in un sistema distribuito, e:

- sync disabilitata di default
- attivazione esplicita solo dopo aver completato la configurazione della sede corretta

### 5.4 Configurazione device hardcoded o percepita come hardcoded nel frontend system admin

In `system_admin.html` esiste almeno un'informazione statica non affidabile, ad esempio il numero di device connessi mostrato come valore fisso.

Questo e un problema per due motivi:

- dal punto di vista UX fa sembrare reale una configurazione che puo non esserlo
- dal punto di vista operativo rende piu difficile capire se il sistema sta leggendo davvero la configurazione runtime

Un pannello system admin non dovrebbe mostrare dati fissi per elementi che in realta dipendono dalla sede o dal runtime.

### 5.5 Bootstrap del system admin poco robusto

Il sistema attuale supporta un bootstrap del system admin tramite `DEFAULT_SYSTEM_ADMIN_PASSWORD`, ma con una logica limitata:

- l'utente e fisso: `admin`
- la password bootstrap viene usata solo se il system admin non esiste ancora
- se l'utente esiste gia, il bootstrap non sovrascrive nulla
- se il container non legge correttamente la variabile env, il bootstrap non avviene

Questo porta a diversi casi ambigui:

- installazione nuova con `.env` presente ma container non ricreato
- DB gia esistente con admin gia creato e password diversa
- recovery in cui non si capisce se conviene usare `.env` o `make_system_admin`

Per una nuova sede serve un comportamento piu esplicito e meno ambiguo.

### 5.6 Schema migration non ancora strutturato

Lo schema viene oggi gestito in larga parte da `InitDB()` tramite:

- `CREATE TABLE IF NOT EXISTS`
- `ALTER TABLE` tentati in modo opportunistico
- creazione indici anch'essa inline

Questo approccio ha funzionato finche il progetto era relativamente concentrato, ma diventa fragile quando:

- esistono piu installazioni indipendenti
- si prepara una nuova pipeline device
- si introducono nuove colonne o legami raw -> final
- si vuole garantire rollout ripetibili

Prima di introdurre la semplificazione stati, conviene mettere un minimo di ordine nelle migrazioni.

## 6. Obiettivo architetturale da raggiungere

Il sistema deve poter supportare questa situazione target:

- una sola codebase
- una sola immagine applicativa riusabile
- una configurazione per sede esterna al codice
- una convenzione semplice per i file env di sede
- una directory dati separata dalla repository
- un bootstrap chiaro del system admin
- una nuova sede che possa partire vuota senza ereditare dati della sede originale
- un percorso evolutivo sicuro verso la semplificazione dei device a 3 stati raw

In altre parole, il deploy di una nuova sede deve diventare:

1. clonazione del codice
2. creazione configurazione di sede
3. creazione directory dati vuota
4. bootstrap credenziali iniziali
5. avvio applicazione con sync disattivata
6. test locale della nuova istanza
7. attivazione dei device della sede corretta

Nel caso attuale le due sedi note sono:

- `Napoli`, sede originale
- `Ferrara`, nuova sede

La convenzione raccomandata per i file env locali e:

- `.env.napoli`
- `.env.ferrara`

Sui server, invece, la raccomandazione resta di usare un solo file attivo chiamato:

- `.env`

con contenuto diverso a seconda della sede.

Quindi:

- sul server di Napoli ci sara solo `.env` con configurazione Napoli
- sul server di Ferrara ci sara solo `.env` con configurazione Ferrara

In locale puoi invece tenere entrambi i file senza conflitto, purche non vengano usati contemporaneamente come file attivo dello stack.

## 7. Decisioni raccomandate

### 7.1 Separare definitivamente il runtime dalla repository

Da repository devono sparire:

- database SQLite runtime
- backup runtime
- file import temporanei
- file Excel di appoggio non necessari al codice
- segreti e certificati privati

La repository deve contenere:

- codice sorgente
- template di configurazione
- file di documentazione
- eventuali esempi anonimi e minimali

I dati di una sede devono vivere fuori da Git.

### 7.2 Rendere il deploy safe by default

Una nuova installazione non deve:

- sincronizzare device senza esplicita abilitazione
- contenere dati preesistenti
- mostrare dati frontend statici che fanno pensare a una configurazione gia pronta

Per questo il default corretto e:

- `ANVIZ_SYNC_ENABLED=false`
- nessun device attivo se non configurato
- nessun bootstrap implicito incomprensibile del system admin

### 7.3 Spostare la configurazione device fuori dal codice

IP e ID device devono essere forniti da:

- variabili env ben definite
- oppure un file di configurazione locale per sede

La scelta puo essere fatta in base alla semplicita di deploy, ma il principio non cambia:

- codice generico
- configurazione esterna

Nel caso specifico del progetto attuale questo significa:

- Napoli avra la propria configurazione locale dei device
- Ferrara avra la propria configurazione locale dei device
- nessuna delle due configurazioni dovra essere hardcoded nella codebase
- i due file env possono coesistere in locale come `.env.napoli` e `.env.ferrara`
- sul singolo server dovra esistere solo il `.env` della sede corrispondente

### 7.4 Far riflettere la UI lo stato reale del backend

`system_admin.html` non deve avere indicatori statici per:

- numero device
- stato configurazione
- presenza o assenza di diagnostica

La UI deve sempre derivare questi dati da API runtime.

### 7.5 Rendere il bootstrap system admin esplicito e verificabile

Serve una policy semplice da comunicare e sempre valida:

- DB vuoto + env bootstrap = creazione iniziale dell'utente `admin`
- admin gia esistente = nessuna sovrascrittura implicita
- reset o recovery password = comando esplicito con `make_system_admin`

In aggiunta, e utile poter capire in modo chiaro se il bootstrap e avvenuto o no.

### 7.6 Preparare il terreno per la semplificazione stati senza anticipare scorciatoie

Il documento `doc/ANALISI_SEMPLIFICAZIONE_STATI_2026-03-31.md` indica una direzione molto sensata:

- 3 stati raw solo per i device
- `device_raw_records` come livello raw
- resolver backend verso 6 stati canonici
- `records` come tabella finale canonica

Questa direzione va confermata e mantenuta.

Non e raccomandato:

- migrare subito tutto il sistema a 3 stati interni
- cambiare in parallelo web, admin e device
- introdurre la nuova logica prima di aver sistemato deploy e configurazione multi-sede

## 8. Piano di implementazione raccomandato

Di seguito il piano in ordine consigliato. L'ordine e importante, perche alcune attivita sono prerequisiti delle successive.

### Fase 1 - Messa in sicurezza della distribuzione

Obiettivo: evitare che una nuova installazione erediti dati o comportamenti della sede originale.

Attivita:

1. Rimuovere dalla repository i file runtime gia versionati:
   - `data/attendance.db`
   - backup
   - Excel di lavoro
   - altri file non sorgente
2. Verificare e rafforzare `.gitignore` per evitare ricadute future.
3. Definire una struttura dati esterna alla repo, per esempio:
   - directory `runtime/`
   - oppure percorso assoluto fuori dal progetto
4. Aggiornare la documentazione di deploy per chiarire che la repo non deve piu essere usata come contenitore dati.

Risultato atteso:

- un `git clone` porta solo codice e file di configurazione di esempio
- una nuova sede non riceve accidentalmente dati reali

### Fase 2 - Normalizzazione della configurazione di deploy

Obiettivo: fare in modo che la nuova istanza parta in modalita prudente.

Attivita:

1. Cambiare il `docker-compose.yml` in modo che:
   - `ANVIZ_SYNC_ENABLED` sia disattivata di default
   - i path dati siano chiaramente configurabili
   - la configurazione device sia passata tramite env o file montato
2. Introdurre un `.env.example` piu completo e rappresentativo.
3. Formalizzare la convenzione dei file env di sede:
   - `.env.napoli`
   - `.env.ferrara`
   - `.env` come file attivo sul singolo server
4. Distinguere chiaramente:
   - variabili obbligatorie
   - variabili opzionali
   - valori per ambienti nuovi
5. Aggiornare la guida di deploy per nuova sede.

Risultato atteso:

- il sistema si avvia senza fare sync automatica
- la distinzione tra configurazione Napoli e configurazione Ferrara e chiara
- l'istanza e neutra fino a quando non viene configurata

### Fase 3 - Esternalizzazione completa della configurazione device

Obiettivo: eliminare la dipendenza da IP hardcoded nel codice.

Attivita:

1. Sostituire la lista hardcoded dei device con una configurazione caricata da env o file.
2. Validare la configurazione all'avvio.
3. Se la configurazione e assente o invalida:
   - non partire con la sync
   - loggare in modo esplicito il motivo
4. Eventualmente introdurre un endpoint API che esponga:
   - elenco device configurati
   - stato configurazione
   - device attivi per la sync

Risultato atteso:

- la stessa build puo essere usata in piu sedi
- il comportamento dell'istanza dipende dalla sua configurazione locale, non dal codice compilato

### Fase 4 - Allineamento del pannello system admin

Obiettivo: fare in modo che il frontend mostri solo informazioni reali.

Attivita:

1. Rimuovere testi o valori statici relativi ai device.
2. Far derivare il numero di device e il loro stato dalle API.
3. Se non ci sono device configurati:
   - mostrare messaggio esplicito
   - evitare indicatori fuorvianti
4. Rendere piu chiaro il pannello sicurezza:
   - stato bootstrap system admin
   - possibilita di cambio password
   - eventuali messaggi di recovery

Risultato atteso:

- il system admin dashboard diventa un pannello affidabile di stato reale

### Fase 5 - Consolidamento del bootstrap system admin

Obiettivo: rendere l'accesso iniziale prevedibile e supportabile.

Attivita:

1. Formalizzare il comportamento del bootstrap:
   - su DB vuoto, se e presente `DEFAULT_SYSTEM_ADMIN_PASSWORD`, viene creato `admin`
   - se `admin` esiste gia, nessuna sovrascrittura automatica
2. Documentare il percorso di recovery:
   - `./make_system_admin admin <nuova_password>`
3. Valutare l'introduzione di un endpoint o stato diagnostico che dica:
   - admin bootstrap presente o assente
4. Aggiornare la UI o i log per rendere evidente la situazione iniziale.

Risultato atteso:

- su una nuova sede non ci sono ambiguita su come si entra la prima volta
- in caso di password smarrita esiste una procedura chiara

### Fase 6 - Preparazione DB e migrazioni

Obiettivo: mettere ordine prima delle modifiche piu profonde alla pipeline device.

Attivita:

1. Introdurre una versione schema o una piccola tabella migrazioni.
2. Spostare le evoluzioni schema piu rilevanti in passaggi tracciabili.
3. Mantenere comunque la facilita di bootstrap di un DB nuovo.
4. Preparare eventuali colonne future utili alla pipeline raw -> final.

Risultato atteso:

- ogni nuova istanza arriva a uno schema coerente e verificabile
- le modifiche future sono meno rischiose

### Fase 7 - Implementazione della semplificazione stati device

Obiettivo: applicare quanto deciso in `doc/ANALISI_SEMPLIFICAZIONE_STATI_2026-03-31.md` senza rompere il resto dell'applicazione.

Decisione di fondo da mantenere:

- 3 stati raw solo per i device
- `web` e `manual_web` restano a 6 stati espliciti
- `records` resta la tabella finale a 6 stati canonici

Attivita:

1. Adeguare la pipeline device per accettare i nuovi gruppi raw:
   - `in_out`
   - `pausa`
   - `trasferta`
2. Salvare sempre il raw in `device_raw_records`.
3. Introdurre una funzione resolver backend dedicata che converta raw -> final.
4. Mantenere `records.action` e `records.status_code` nel formato canonico attuale.
5. Valutare un collegamento esplicito tra record finale e record raw di origine.

Risultato atteso:

- il conteggio ore, i warning e il rendering continuano a lavorare sul modello consolidato a 6 stati
- la semplificazione UX agisce solo dove serve davvero

### Fase 8 - Compatibilita con il periodo di transizione

Obiettivo: evitare un cut-over troppo rigido.

Attivita:

1. Consentire al backend di convivere con:
   - raw legacy a 6 stati
   - raw nuovi a 3 gruppi
2. Evitare migrazioni massive dello storico.
3. Garantire che lo storico gia esistente continui a essere leggibile e coerente.

Risultato atteso:

- si puo distribuire il codice prima di completare il cambio lato terminale

### Fase 9 - Testing e validazione

Obiettivo: ridurre regressioni su dati, ore, warning e comportamento dei device.

Attivita:

1. Test su DB vuoto di nuova sede:
   - creazione schema
   - bootstrap admin
   - sync disattivata
2. Test su copia DB reale:
   - compatibilita storico
   - conteggi ore
   - warning e anomalie
3. Test sul resolver raw -> final:
   - casi standard
   - edge case
   - duplicati ravvicinati
4. Test UI system admin:
   - stato device reale
   - cambio password
   - diagnostica configurazione

Risultato atteso:

- il sistema e validato sia come nuova installazione sia come evoluzione di installazione esistente

### Fase 10 - Rollout nuova sede

Obiettivo: attivare una nuova societa senza contaminazione dalla sede originale.

Procedura target:

1. clonare il codice
2. preparare in locale il file della sede corretta, ad esempio `.env.ferrara`
3. predisporre directory dati vuota
4. copiare sul server della sede il file corretto come `.env`
5. avviare stack con sync disattivata
6. verificare accesso system admin
7. verificare dashboard e assenza dati
8. configurare device della nuova sede
9. attivare la sync solo dopo verifica finale

Risultato atteso:

- nuova sede autonoma
- nessun dato storico importato accidentalmente
- nessun tentativo di contatto verso i device della sede originale

## 9. Dipendenze tra i lavori

La dipendenza corretta tra i vari temi e questa:

1. prima va sistemata la distribuzione multi-sede
2. poi va ripulita la gestione dati e configurazione
3. poi va consolidato il bootstrap del system admin
4. poi va introdotto ordine nelle migrazioni schema
5. solo a quel punto conviene implementare la semplificazione stati

Non e consigliato invertire l'ordine.

Se si implementasse prima la semplificazione stati senza aver sistemato deploy e configurazione:

- si aumenterebbe la complessita
- si renderebbe piu difficile diagnosticare problemi
- si rischierebbe di testare la nuova pipeline in un ambiente ancora ambiguo

## 10. Rischi principali e mitigazioni

### Rischio 1 - Distribuire ancora dati reali tramite Git

Mitigazione:

- rimozione dei dati runtime dalla repo
- rafforzamento `.gitignore`
- documentazione esplicita di separazione dati/codice

### Rischio 2 - Ripopolare un DB nuovo con dati della sede sbagliata

Mitigazione:

- sync disattivata di default
- configurazione device esterna
- attivazione manuale solo a configurazione conclusa

### Rischio 3 - Ambiguita nell'accesso system admin

Mitigazione:

- bootstrap formalizzato
- recovery password documentata
- eventuale stato diagnostico dedicato

### Rischio 4 - UI fuorviante sullo stato reale del sistema

Mitigazione:

- rimozione valori statici
- uso esclusivo di dati runtime via API

### Rischio 5 - Regressioni nella futura semplificazione stati

Mitigazione:

- mantenere `records` a 6 stati
- test su copia DB
- introduzione graduale della nuova pipeline raw -> final

## 11. Criteri di accettazione del lavoro

Il piano puo considerarsi completato quando sono vere tutte queste condizioni:

- un nuovo `git clone` non contiene dati reali di altre sedi
- una nuova installazione puo partire con DB vuoto
- la sync device non parte da sola se la sede non e ancora configurata
- nessun IP device e piu hardcoded nel codice di business
- il system admin puo essere bootstrapato e recuperato con procedura chiara
- `system_admin.html` mostra solo dati reali runtime
- la nuova sede puo essere attivata senza rischi di contaminazione
- il DB e pronto a ricevere le future estensioni della pipeline device
- la semplificazione stati puo essere implementata sopra una base finalmente stabile

## 12. Conclusione

Il problema emerso con il nuovo server non e un semplice incidente di deploy, ma un segnale utile: il progetto ha ormai superato il punto in cui puo essere trattato come una singola installazione con dati, configurazione e codice mescolati insieme.

La priorita non e solo correggere il branch o far entrare il system admin, ma portare il progetto a una forma piu matura:

- codice separato dai dati
- configurazione separata dal codice
- deploy prudente
- bootstrap amministrativo chiaro
- base affidabile per la futura semplificazione a 3 stati raw lato device

Questo ordine di lavoro consente di:

- mettere in sicurezza la nuova sede
- ridurre il rischio operativo
- preparare bene il terreno per le prossime evoluzioni applicative

## 13. Prossimo passo raccomandato

Il prossimo passo pratico consigliato e aprire un intervento tecnico in due blocchi consecutivi:

1. "messa in sicurezza multi-sede"
2. "preparazione pipeline device a 3 stati"

Il primo blocco deve essere completato prima di iniziare il secondo.

## 14. Checklist tecnica operativa

Questa sezione traduce il piano in una checklist concreta e spuntabile.

### 14.1 Blocco A - Messa in sicurezza multi-sede

- [x] Rimuovere dalla repository `data/attendance.db`
- [x] Rimuovere dalla repository i backup del database
- [x] Rimuovere dalla repository gli Excel di lavoro e gli artefatti di import non indispensabili
- [x] Verificare che `.gitignore` impedisca di recommittare DB, backup, export e file runtime
- [x] Decidere il path runtime definitivo per ogni sede, ad esempio:
  - directory dati
  - directory backup
  - directory certificati
- [x] Formalizzare i file env di sede:
  - `.env.napoli`
  - `.env.ferrara`
- [x] Stabilire che sui server esiste solo `.env` come file attivo della sede
- [x] Aggiornare `docker-compose.yml` per montare dati e certificati da percorsi runtime della macchina
- [x] Portare `ANVIZ_SYNC_ENABLED` a default prudente
- [x] Aggiungere a `.env.example` tutte le variabili runtime necessarie per una nuova sede
- [x] Aggiornare la guida di deploy per chiarire che il clone non contiene piu dati operativi

### 14.2 Blocco B - Configurazione device

- [x] Rimuovere la lista device hardcoded dal backend
- [x] Definire il formato di configurazione device, ad esempio via env o file JSON
- [x] Implementare parser e validazione della configurazione device
- [x] Se non esistono device configurati, impedire la partenza della sync automatica
- [x] Esporre al frontend system admin il numero reale di device configurati
- [x] Esporre al frontend system admin lo stato reale dei device, non dati statici
- [ ] Verificare che l'istanza nuova sede con config vuota non tenti connessioni verso device della sede originale

### 14.3 Blocco C - System admin

- [x] Formalizzare il bootstrap iniziale dell'utente `admin`
- [x] Verificare che `DEFAULT_SYSTEM_ADMIN_PASSWORD` venga davvero passata al container
- [x] Documentare chiaramente quando il bootstrap crea l'utente e quando no
- [x] Documentare la procedura di recovery con `make_system_admin`
- [x] Rendere visibile nei log o nella UI se non esiste alcun system admin bootstrapato
- [ ] Verificare login iniziale su DB vuoto
- [ ] Verificare cambio password da `system_admin.html`

### 14.4 Blocco D - Frontend system admin

- [x] Eliminare valori hardcoded che rappresentano stato device o stato sistema
- [x] Sostituire il conteggio statico dei device con valori ottenuti via API
- [x] Mostrare messaggio esplicito se non esistono device configurati
- [x] Allineare il pannello di diagnostica al runtime reale
- [ ] Verificare che il pannello continui a funzionare sia con sync attiva sia con sync disattiva

### 14.5 Blocco E - Schema e migrazioni

- [ ] Introdurre una strategia minima di versionamento schema
- [ ] Centralizzare le migrazioni strutturali piu rilevanti
- [ ] Garantire bootstrap corretto di un DB completamente nuovo
- [ ] Preparare eventuali estensioni future utili alla pipeline raw -> final
- [ ] Verificare l'avvio su DB nuovo, su DB esistente e su DB copia storica

### 14.6 Blocco F - Semplificazione stati device

- [ ] Confermare ufficialmente che la semplificazione riguarda solo i device
- [ ] Confermare che `web` e `manual_web` restano a 6 stati
- [ ] Introdurre i 3 gruppi raw:
  - `in_out`
  - `pausa`
  - `trasferta`
- [ ] Salvare i raw device in `device_raw_records`
- [ ] Implementare il resolver backend raw -> 6 stati canonici
- [ ] Mantenere `records` come tabella finale a 6 stati
- [ ] Garantire compatibilita con lo storico legacy
- [ ] Introdurre logging utile per capire come un raw e stato risolto
- [ ] Coprire il resolver con test unitari
- [ ] Validare il comportamento su copia DB reale

### 14.7 Blocco G - Rollout nuova sede

- [ ] Preparare server nuova sede con directory runtime vuota
- [x] Preparare localmente `.env.ferrara`
- [ ] Copiare su server Ferrara il file corretto come `.env`
- [ ] Installare certificati della nuova sede fuori dalla repository
- [ ] Avviare stack con sync disattivata
- [ ] Verificare creazione DB pulito
- [ ] Verificare accesso system admin
- [ ] Verificare assenza di dati legacy
- [ ] Configurare device reali della nuova sede
- [ ] Attivare la sync solo dopo verifica finale
- [ ] Monitorare log e diagnostica nei primi giorni

## 15. Gestione dei certificati se li escludiamo da Git

Questa e una domanda importante, perche escludere i certificati dalla repository non significa "non usarli piu", ma gestirli nel posto giusto.

### 15.1 Principio corretto

I certificati usati in produzione non dovrebbero vivere nella repository applicativa.

La repository deve contenere al massimo:

- istruzioni
- esempi
- placeholder

La macchina di destinazione deve invece contenere:

- il certificato reale
- la chiave privata reale

### 15.2 Perche conviene escluderli da Git

Motivi principali:

- la chiave privata non deve viaggiare insieme al codice
- ogni sede puo avere certificati diversi
- in caso di rinnovo o sostituzione non serve toccare la repository
- si evita di distribuire per errore il certificato o la chiave di una sede a un'altra sede

### 15.3 Come fare in pratica

La soluzione piu semplice e robusta e questa:

1. creare sulla macchina una directory runtime dedicata, ad esempio:
   - `/opt/marcatempo-runtime/certs`
2. copiare li dentro i file reali:
   - `server.crt`
   - `server.key`
3. montare quella directory nel container nginx

In questo modo:

- il codice resta identico
- il deploy resta semplice
- ogni server ha i suoi certificati locali

### 15.4 Come cambierebbe il deploy Docker

Oggi il compose monta `./certs:/etc/nginx/certs:ro`.

La direzione raccomandata e montare una directory esterna alla repo, per esempio:

- `/opt/marcatempo-runtime/certs:/etc/nginx/certs:ro`

Lo stesso principio vale per i dati e per i backup.

### 15.5 Se tolgo i certificati dalla repo, poi come li recupero?

Hai varie opzioni sane.

Opzione 1, la piu semplice:

- li copi manualmente sul server durante l'installazione
- li conservi in un archivio sicuro aziendale

Opzione 2:

- li tieni in un vault o password manager aziendale con allegati protetti

Opzione 3:

- li emetti direttamente sul server con Let's Encrypt o altra CA, se il dominio e il contesto lo consentono

### 15.6 Cosa mettere in repo al posto dei certificati reali

Le opzioni migliori sono:

- un file `certs/README.md` che spiega dove mettere i file reali
- eventualmente placeholder non sensibili
- eventualmente un esempio di struttura directory

Non e invece consigliato tenere in Git:

- `server.key`
- certificati reali di produzione

### 15.7 Nota pratica sul certificato `.crt`

Anche se il `.crt` pubblico e meno sensibile della chiave privata, nel tuo caso conviene comunque trattarlo come artefatto di deploy e non come file sorgente.

Motivo:

- e legato a una sede o a un dominio specifico
- non aggiunge valore al codice
- distribuito via Git aumenta la confusione tra "codice" e "configurazione di istanza"

### 15.8 Raccomandazione finale sui cert

La raccomandazione concreta e:

- togliere dalla repo sia `server.crt` sia `server.key`
- lasciare in repo solo documentazione e placeholder
- montare i certificati reali da una directory runtime esterna
- documentare nella guida di deploy dove vanno copiati

## 16. Convenzione operativa Napoli / Ferrara

Per evitare ambiguita operative, si raccomanda di adottare subito questa convenzione semplice.

### 16.1 File locali di configurazione

Sulla macchina di sviluppo puoi tenere:

- `.env.napoli`
- `.env.ferrara`

Questi file:

- non devono essere committati
- non vanno in conflitto tra loro perche hanno nomi diversi
- servono come sorgenti locali per preparare il deploy delle due sedi

### 16.2 File attivo sul server

Ogni server deve avere un solo file attivo:

- `.env`

Quindi:

- server Napoli -> `.env` con configurazione Napoli
- server Ferrara -> `.env` con configurazione Ferrara

Questo evita qualsiasi ambiguita durante l'avvio di Docker Compose o del servizio.

### 16.3 Risultato pratico atteso

Con questa impostazione:

- Napoli continuera a usare solo i suoi device
- Ferrara usera solo i device configurati nel suo `.env`
- Ferrara potra partire con DB pulito
- il DB di Ferrara si popolera solo se e quando verra attivata la sync verso i device di Ferrara
- non ci sara conflitto tra i due file env tenuti in locale
