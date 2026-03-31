# Analisi Semplificazione Stati Marcatempo

Data analisi: 2026-03-31

## 1. Obiettivo

Valutare la semplificazione delle timbrature da 8 stati raw / 6 stati canonici realmente usati a 3 soli stati operativi lato utente:

- `in_out`
- `pausa`
- `trasferta`

L'idea e che ogni stato rappresenti una coppia logica:

- `in_out` -> `entrata` oppure `uscita`
- `pausa` -> `inizio_pausa` oppure `fine_pausa`
- `trasferta` -> `inizio_trasferta` oppure `ritorno_trasferta`

Il sistema dovrebbe quindi distinguere automaticamente se una timbratura rappresenta il primo o il secondo evento della coppia, mantenendo invariati:

- numero totale di timbrature
- struttura visuale delle tabelle
- logica generale di bilancio ore

## 2. Stato attuale del sistema

### 2.1 Stati gestiti oggi

Nel codice attuale il dispositivo Anviz decodifica fino a 8 stati raw:

- `0` -> `In`
- `1` -> `Out`
- `2` -> `I_pausa`
- `3` -> `F_pausa`
- `4` -> `U_trasf`
- `5` -> `R_trasf`
- `6` -> `I_break`
- `7` -> `F_break`

Riferimento: `anviz_sync.go`

Nell'applicazione, per il calcolo e il rendering, vengono pero normalizzate e usate di fatto 6 azioni canoniche:

- `entrata`
- `uscita`
- `inizio_pausa`
- `fine_pausa`
- `inizio_trasferta`
- `ritorno_trasferta`

Riferimenti principali:

- `index.html`
- `admin.html`
- `doc/LOGICA_CONTEGGIO_ORE.md`

### 2.2 Evidenza dai dati reali

Sul database locale analizzato il 2026-03-31 risultano:

- record totali: `26692`
- `device`: `26660`
- `manual_web`: `22`
- `web`: `10`

Distribuzione per azione:

- `In`: `8785`
- `Out`: `8520`
- `I_pausa`: `4257`
- `F_pausa`: `4171`
- `R_trasf`: `549`
- `U_trasf`: `410`

Distribuzione per `status_code`:

- `0`: `8785`
- `1`: `8520`
- `2`: `4257`
- `3`: `4171`
- `4`: `410`
- `5`: `549`

Conclusione importante:

- gli stati `6` e `7` non compaiono nello storico attuale
- i due stati "mai usati" sono quindi realmente assenti nei dati analizzati

## 3. Problema che la semplificazione risolve

Il problema non e il numero di record, ma il numero di scelte possibili per l'utente al momento della timbratura.

Oggi l'utente deve selezionare esplicitamente l'evento corretto tra piu opzioni simili. Questo genera:

- errori di distrazione
- marcature incoerenti nella sequenza giornaliera
- fallback complessi per evitare bilanci penalizzanti
- warning e anomalie che rimangono comunque aperti

Ridurre le scelte da 6 operative a 3 gruppi logici e una semplificazione molto sensata dal punto di vista UX.

## 4. Decisione architetturale raccomandata

La scelta piu importante non e "3 pulsanti si o no", ma **in quale punto della pipeline si introduce la semplificazione**.

Dopo l'analisi e la discussione funzionale, la soluzione raccomandata e questa:

- i **device** passano a 3 stati operativi
- le **timbrature manuali** restano a 6 stati espliciti
- le **timbrature web** restano a 6 stati espliciti
- il DB continua ad avere una separazione netta tra livello **raw** e livello **canonico finale**

In altre parole:

- il problema UX viene affrontato solo dove e davvero necessario, cioe sul terminale fisico
- i canali meno frequenti e piu controllati restano precisi e "chirurgici"
- il motore interno continua a lavorare sui 6 stati canonici, che sono quelli gia attesi da calcoli, tabelle, warning ed export

### 4.1 Decisione per canale

#### Device

Per i dispositivi la semplificazione e consigliata:

- input utente a 3 stati
- salvataggio iniziale in raw
- risoluzione backend in 6 stati canonici
- salvataggio finale in `records`

#### Manuale admin

Per le marcature manuali e consigliato **non cambiare nulla**:

- l'admin continua a scegliere direttamente tra 6 stati
- il valore della marcatura manuale e proprio la precisione
- spesso serve per correggere casi anomali che un criterio automatico non risolverebbe bene

#### Web

Per le marcature web conviene anch'esse **restino cosi come sono**:

- non hanno un vero livello raw device
- sono numericamente molto meno rilevanti
- sono piu controllate
- mantenerle esplicite riduce ambiguita e non complica il backend

## 5. Pipeline raccomandata

La pipeline consigliata, limitatamente al canale device, e la seguente.

### 5.1 Vista ad alto livello

`device -> device_raw_records -> resolver backend -> records -> rendering / conteggio ore / export`

### 5.2 Dettaglio fase per fase

#### Fase A - Input dal dispositivo

Il terminale espone 3 soli stati operativi:

- `in_out`
- `pausa`
- `trasferta`

Questo riduce le possibilita di errore lato utente senza ridurre il numero di timbrature.

#### Fase B - Persistenza raw

Ogni timbratura proveniente dal device viene salvata **prima** in `device_raw_records`.

Con questa impostazione:

- non si perde il dato originario
- ogni trasformazione successiva e tracciabile
- il sistema puo sempre essere rieseguito o rianalizzato partendo dal raw
- la pipeline rimane coerente con il principio gia presente nel progetto: prima raw, poi final

Nel nuovo scenario il campo `device_raw_records.action` conterra il valore raw del gruppo, cioe:

- `in_out`
- `pausa`
- `trasferta`

Non e quindi strettamente necessario aggiungere una colonna `action_group` in `device_raw_records`, perche il gruppo e gia espresso direttamente in `action`.

#### Fase C - Risoluzione backend

Una funzione backend dedicata legge:

- il record raw appena inserito
- la giornata del dipendente
- le timbrature finali gia presenti in `records`
- il gruppo raw della marcatura

e risolve il raw in una delle 6 azioni canoniche:

- `In`
- `Out`
- `I_pausa`
- `F_pausa`
- `U_trasf`
- `R_trasf`

#### Fase D - Persistenza finale

Il record risolto viene salvato in `records`.

`records` resta quindi la tabella canonica dell'applicazione, esattamente come oggi dal punto di vista concettuale:

- `action` contiene il valore finale a 6 stati
- `status_code` contiene il codice coerente con lo stato finale
- tutte le logiche esistenti continuano a leggere il dato gia interpretato

#### Fase E - Consumo applicativo

Tutto cio che oggi usa `records` continua a lavorare quasi come prima:

- rendering della tabella dipendente
- rendering della tabella admin
- warning e anomalie
- export
- riepiloghi
- conteggio ore

Questo e il punto che riduce davvero il rischio del progetto.

### 5.3 Esempio concreto di pipeline

Caso semplice:

- il dipendente timbra `in_out` alle `09:00`
- il dipendente timbra `in_out` alle `18:00`

Pipeline:

1. il device invia due record raw con `action = in_out`
2. entrambi vengono salvati in `device_raw_records`
3. il resolver backend legge la sequenza cronologica
4. la prima marcatura viene risolta in `In`
5. la seconda marcatura viene risolta in `Out`
6. in `records` vengono salvati i due eventi canonici
7. il resto dell'applicazione continua a vedere la giornata come la vede oggi

### 5.4 Regola base di risoluzione

Per ogni coppia logica il criterio base e cronologico:

- prima marcatura coerente della coppia -> apertura
- seconda marcatura coerente della coppia -> chiusura

Applicato ai gruppi:

- `in_out`: prima `In`, seconda `Out`
- `pausa`: prima `I_pausa`, seconda `F_pausa`
- `trasferta`: prima `U_trasf`, seconda `R_trasf`

Il principio e semplice e corretto. La parte delicata non e il caso normale, ma la formalizzazione dei casi anomali.

## 6. Raccomandazione finale

La soluzione raccomandata e:

- **3 stati raw solo per i device**
- **salvataggio immediato in `device_raw_records`**
- **risoluzione backend verso 6 stati canonici**
- **`records` invariata come tabella finale**
- **manual_web e web lasciati a 6 stati espliciti**

Motivi principali:

- massimo beneficio nel punto dove gli utenti sbagliano di piu
- minimo impatto sul resto del sistema
- compatibilita piena con i casi legacy
- rischio basso sul database
- rischio medio ma controllabile sul backend
- rischio basso o medio-basso sul conteggio ore, perche continua a lavorare su 6 stati canonici

L'alternativa di portare anche `records` a 3 soli stati e sconsigliata, perche aumenterebbe in modo netto la complessita nel cuore del sistema.

## 7. Impatto sul database

### 7.1 Struttura attuale rilevante

#### `device_raw_records`

Struttura attuale:

- `id`
- `device_id`
- `employee_id`
- `employee_name`
- `raw_device_timestamp`
- `parsed_timestamp`
- `action`
- `status_code`
- `imported_at`

#### `records`

Struttura attuale:

- `id`
- `employee_id`
- `timestamp`
- `action`
- `source`
- `latitude`
- `longitude`
- `status_code`
- `employee_name`
- `device_id`
- `raw_device_timestamp`

### 7.2 Impatto reale sul DB

Dal punto di vista del database, il fatto che i raw device passino da 6/8 stati a 3 stati **non e un problema strutturale**.

Motivi:

- il DB non impone un numero fisso di stati
- meno valori distinti in `action` non rompe tabelle o indici
- il dato discriminante device continua a essere soprattutto la combinazione di:
  - `device_id`
  - `employee_id`
  - `raw_device_timestamp`
  - `status_code`

Il vero punto di attenzione non e il DB, ma il codice che interpreta i dati.

### 7.3 Modifiche consigliate minime

La proposta minima e:

- in `device_raw_records.action` salvare il valore raw a 3 stati
- in `records.action` continuare a salvare i 6 stati canonici

Questa e la scelta piu pulita.

### 7.4 Colonne aggiuntive opzionali

Colonne che possono essere utili, ma non obbligatorie:

- in `records`: un riferimento al raw, ad esempio `raw_record_id`
- in `device_raw_records`: eventuali campi di audit come:
  - `resolved_action`
  - `resolved_status_code`
  - `resolution_status`
  - `resolved_at`
  - `resolution_note`

Questi campi sarebbero utili per debugging e tracciabilita, ma non sono strettamente necessari alla prima iterazione.

### 7.5 Raccomandazione su `action_group`

Alla luce della struttura attuale, `action_group` **non e la prima colonna che aggiungerei**.

Motivo:

- nel flusso device raw il gruppo e gia contenuto naturalmente in `device_raw_records.action`
- in `records` non conviene salvare il gruppo, perche li dovrebbe vivere il dato gia risolto

Se si vuole aggiungere una colonna in piu, e piu utile un legame esplicito tra `records` e il raw di origine che non una duplicazione del gruppo.

## 8. Impatto applicativo

### 8.1 Backend

Aree coinvolte:

- acquisizione raw dai device
- risoluzione raw -> 6 stati canonici
- persistenza finale in `records`
- deduplica del flusso device
- eventuale audit/logging della risoluzione

Punti rilevanti nel codice:

- `anviz_sync.go` per decodifica stati raw
- `db.go` per inserimento record e deduplica
- `main.go` per i flussi web/manuali che invece resteranno invariati

### 8.2 Frontend dipendente

Aree coinvolte:

- solo se il frontend utente riflette davvero i 3 stati del device
- eventuali etichette di supporto e feedback
- nessuna necessita di toccare il rendering storico se `records` resta a 6 stati

### 8.3 Frontend admin

Aree coinvolte:

- impatto basso
- le correzioni manuali restano a 6 stati
- il pannello continua a leggere `records`

Nota importante:

oggi in admin le correzioni manuali hanno valore "chirurgico". Conviene non semplificarle subito.

### 8.4 Calcolo ore e anomalie

La logica attuale usa azioni esplicite, con regole documentate per:

- pause complete e parziali
- trasferta completa e incompleta
- uscita implicita da ritorno trasferta
- doppie trasferte
- giornata non chiusa

Se `records` continua a contenere 6 azioni esplicite, questa logica cambia poco.

Questa e una raccomandazione importante:

- il grosso del lavoro non e riscrivere il conteggio ore
- il grosso del lavoro e introdurre un resolver affidabile tra raw device e `records`

## 9. Punti critici

Questa sezione evidenzia i punti davvero sensibili del progetto.

### 9.1 Il criterio base e chiaro, ma va formalizzato bene

Nel caso standard il criterio e semplice:

- prima marcatura cronologica della coppia = apertura
- seconda marcatura cronologica della coppia = chiusura

Esempio:

- `in_out` alle `09:00` -> `In`
- `in_out` alle `18:00` -> `Out`

Questa parte non e problematica.

Il punto critico e definire in modo deterministico gli edge case.

### 9.2 Edge case che vanno decisi prima di sviluppare

Lo storico mostra gia oggi giornate con:

- una sola marcatura della coppia
- tre marcature dello stesso gruppo
- `R_trasf` senza `U_trasf`
- pause incomplete
- uscite mancanti
- extra mark

La semplificazione riduce l'errore futuro, ma non elimina automaticamente i casi storti gia esistenti.

Gli edge case da definire formalmente sono almeno questi:

- terza marcatura `in_out` nella stessa giornata
- doppia pausa senza chiusura coerente
- doppia trasferta nella stessa giornata
- pausa aperta e poi `in_out`
- trasferta aperta e poi `pausa`
- due marcature quasi identiche a pochi secondi/minuti di distanza
- raw arrivati in ritardo o non perfettamente ordinati

### 9.3 Flussi diversi tra device e web/manuale

Il beneficio vero si ottiene solo se il cambio riguarda il canale usato davvero dagli operatori.

Nel caso attuale questa differenziazione e un vantaggio, non uno svantaggio:

- device semplificati per ridurre errore utente
- web/manuale espliciti per mantenere precisione amministrativa

### 9.4 Tracciabilita e debug della risoluzione

Se la risoluzione avviene tra raw e final, bisogna poter capire facilmente:

- quale raw e arrivato
- come e stato interpretato
- quale record finale e stato creato
- perche un caso anomalo e stato risolto in un certo modo

Senza un minimo di tracciabilita, il progetto funzionera ma sara piu difficile da gestire in produzione.

### 9.5 Duplicazione di logica frontend

Una parte del comportamento giornaliero e oggi duplicata in:

- `index.html`
- `admin.html`

Questo aumenta il costo del cambiamento e il rischio di divergenze.

## 10. Impatto sul conteggio ore

Questa parte merita un chiarimento esplicito.

Il conteggio ore **non e il punto piu critico del progetto**, a condizione che:

- i raw device vengano risolti correttamente nei 6 stati canonici
- `records` continui a contenere quei 6 stati

In questo scenario il conteggio ore:

- continua a vedere `In`, `Out`, `I_pausa`, `F_pausa`, `U_trasf`, `R_trasf`
- continua quindi a funzionare secondo la logica attuale
- richiede soprattutto test di regressione, non una riscrittura radicale

L'area da sorvegliare di piu non e quindi il calcolo in se, ma:

- correttezza dello smistamento
- correttezza dei warning sui casi anomali
- coerenza tra prima e seconda marcatura della coppia

## 11. Stima tempi

### Scenario raccomandato - Solo device semplificati, `records` invariata

Tempo stimato:

- sviluppo: `3 - 5 giorni`
- test e verifica su casi reali: `1 - 2 giorni`

Totale prudenziale:

- `4 - 7 giorni lavorativi`

Questo e lo scenario consigliato.

### Scenario alternativo - 3 stati anche in `records`

Tempo stimato:

- `1 - 2 settimane`

Con rischio sensibilmente piu alto di:

- regressioni
- incongruenze sui bilanci
- warning meno leggibili
- difficolta di manutenzione

## 12. Strategia consigliata

### Decisione raccomandata

Implementare la semplificazione come:

- 3 stati raw solo sui device
- salvataggio immediato in `device_raw_records`
- risoluzione server-side verso 6 stati canonici
- salvataggio finale in `records`
- nessuna migrazione massiva dello storico
- `manual_web` invariato a 6 stati
- `web` invariato a 6 stati

### Vantaggi

- massimo beneficio UX
- minimo impatto dati
- rollback semplice
- comparazione prima/dopo piu facile

## 13. Piano di implementazione step by step

### Fase 0 - Allineamento funzionale

1. Confermare ufficialmente che la semplificazione riguarda **solo i device**.
2. Confermare ufficialmente la regola dei 3 gruppi raw:
   - `in_out`
   - `pausa`
   - `trasferta`
3. Confermare che `manual_web` e `web` restano a 6 stati.
4. Definire le regole di risoluzione per ogni gruppo.
5. Decidere cosa fare nei casi ambigui:
   - errore bloccante
   - warning
   - fallback pro-lavoratore

### Fase 1 - Specifica della macchina a stati

Definire una funzione server-side che, dato:

- dipendente
- timestamp
- giornata locale
- storico delle timbrature gia registrate in `records`
- gruppo raw richiesto

restituisce l'azione canonica da salvare.

Esempio di regole iniziali:

- `in_out`
  - nessuna entrata aperta -> `In`
  - entrata aperta senza uscita -> `Out`
- `pausa`
  - entrata presente e pausa non aperta -> `I_pausa`
  - pausa aperta -> `F_pausa`
- `trasferta`
  - trasferta non aperta -> `U_trasf`
  - trasferta aperta -> `R_trasf`

Da definire con attenzione i casi limite:

- singola marcatura del gruppo
- terza marcatura della stessa coppia
- doppia pausa
- doppia trasferta
- pausa dopo trasferta
- trasferta dopo pausa
- timbrature fuori sequenza
- marcature duplicate ravvicinate

### Fase 2 - Implementazione backend centrale

1. Introdurre una funzione unica di risoluzione, usata da:
   - ingest device
   - pipeline raw -> final del device
2. Mantenere il salvataggio in `records.action` con valori canonici.
3. Mantenere `status_code` coerente con l'azione risolta.
4. Adattare deduplica e controlli equivalenza sul flusso device.
5. Valutare l'aggiunta di un riferimento dal final al raw di origine.

### Fase 3 - Persistenza raw e audit

1. Salvare sempre prima il record in `device_raw_records`.
2. Solo dopo il salvataggio raw eseguire la risoluzione.
3. Se utile, aggiungere metadati di audit per capire:
   - se il raw e stato risolto
   - in che stato finale
   - quando
4. Valutare un eventuale campo di collegamento `raw_record_id` nella tabella `records`.

### Fase 4 - Frontend e UX device

1. Portare il device ai 3 stati.
2. Se c'e un'interfaccia utente associata, allineare etichette e messaggi.
3. Se utile, mostrare feedback del tipo:
   - "Marcatura registrata come Entrata"
   - "Marcatura registrata come Uscita"
   - "Marcatura registrata come Inizio Pausa"

Questo aiuta l'utente a capire cosa ha interpretato il sistema.

### Fase 5 - Web e admin

1. Non cambiare la gestione manuale admin.
2. Non cambiare il flusso web.
3. Verificare solo che tutto continui a convivere correttamente con i nuovi record device.

### Fase 6 - Conteggio ore e anomalie

1. Non riscrivere subito la logica di conteggio ore.
2. Verificare che la logica attuale continui a produrre gli stessi risultati sui casi standard.
3. Controllare in particolare:
   - warning
   - extra mark
   - trasferte multiple
   - pause incomplete
   - uscita implicita da ritorno trasferta

### Fase 7 - Compatibilita storico

1. Non migrare in massa i record storici.
2. Lasciare leggibile lo storico attuale.
3. Assicurarsi che il motore di rendering continui a supportare i 6 stati canonici esistenti.

### Fase 8 - Test funzionali su copia DB

Preparare una batteria di casi reali:

- giornata standard completa
- giornata senza pausa marcata
- pausa solo iniziata
- pausa solo chiusa
- trasferta completa
- trasferta aperta
- ritorno trasferta senza inizio
- entrata + ritorno trasferta senza uscita
- doppia trasferta nella stessa giornata
- doppia timbratura ravvicinata dello stesso gruppo

Per ogni caso confrontare:

- colonne renderizzate
- ore lavorate
- bilancio
- warning / anomalie

### Fase 9 - Rollout graduale

1. Attivazione su un gruppo limitato di utenti.
2. Monitoraggio di:
   - numero anomalie
   - numero correzioni admin
   - numero extra mark
   - differenze nel bilancio medio
3. Estensione graduale al resto del personale.

## 14. Testing e debug

Si, aggiungere un po di testing e di debug e **molto utile**, anzi e una delle raccomandazioni principali.

### 14.1 Perche il testing e utile

Il sistema attuale contiene gia una logica di conteggio e fallback non banale.

Anche se il conteggio ore non cambia radicalmente, il nuovo resolver raw -> final introduce un nuovo punto critico. Senza test, il rischio non e tanto "rompere tutto", quanto:

- risolvere male alcuni edge case
- avere warning strani
- produrre bilanci corretti nel caso standard ma incoerenti nei casi limite

### 14.2 Testing minimo consigliato

Almeno questi livelli:

- test unitari sul resolver
- test di integrazione sulla pipeline raw -> records
- test comparativi su copia del DB

#### Test unitari sul resolver

Casi da coprire:

- due `in_out` -> `In`, `Out`
- due `pausa` -> `I_pausa`, `F_pausa`
- due `trasferta` -> `U_trasf`, `R_trasf`
- terza marcatura della stessa coppia
- sequenze incoerenti
- duplicati ravvicinati

#### Test di integrazione

Verificare che:

- il raw venga sempre scritto prima
- il resolver giri dopo il raw
- il final venga scritto correttamente in `records`
- il collegamento e i log siano coerenti

#### Test comparativi su DB

Su una copia del DB reale, confrontare prima/dopo per:

- ore lavorate giornaliere
- bilancio periodo
- warning
- extra

### 14.3 Debug e osservabilita consigliati

Sono molto raccomandati log espliciti nel resolver, ad esempio:

- raw ricevuto
- giornata letta per il dipendente
- stato della coppia prima della risoluzione
- azione finale scelta
- eventuale motivo del fallback

Un buon debug qui e fondamentale, perche quando un utente dira "ho timbrato e mi ha interpretato male", il team dovra poter ricostruire la decisione in pochi minuti.

### 14.4 Raccomandazione pratica

Anche senza costruire una grossa suite completa subito, e consigliato introdurre almeno:

- qualche test unitario sul resolver
- log dettagliati ma leggibili
- una modalita di verifica su copia DB

Questo ha un ottimo rapporto costo/beneficio.

## 15. Rischi e mitigazioni

### Rischio 1 - Ambiguita di interpretazione

Mitigazione:

- macchina a stati chiara
- regole conservative
- feedback esplicito all'utente

### Rischio 2 - Divergenza tra frontend admin e dipendente

Mitigazione:

- spostare la risoluzione lato backend
- mantenere il frontend il piu sottile possibile

### Rischio 3 - Regressioni sul calcolo ore

Mitigazione:

- non toccare il formato interno delle 6 azioni
- test comparativo prima/dopo su copia del DB

### Rischio 4 - Il terminale non supporta davvero il nuovo schema

Mitigazione:

- verifica preventiva della configurabilita Anviz
- eventuale rollout iniziale solo su canale web/app

## 16. Conclusione finale

La semplificazione a 3 stati e una buona idea e ha senso dal punto di vista operativo, **se applicata solo dove serve davvero**.

La versione piu robusta, sostenibile e veloce da implementare e questa:

- **3 stati raw solo per i device**
- **salvataggio raw immediato in `device_raw_records`**
- **risoluzione backend in 6 stati canonici**
- **`records` mantenuta come tabella finale canonica**
- **marcature manuali e web lasciate esplicite a 6 stati**
- **nessuna migrazione storica massiva**

Con questa impostazione:

- il margine di errore utente si riduce davvero
- la semplificazione agisce proprio nel punto piu fragile, cioe il terminale fisico
- il numero di record nel DB resta invariato
- il rendering tabellare puo restare sostanzialmente identico
- l'impatto sul database e basso
- l'impatto sull'app e medio ma gestibile
- i casi legacy restano compatibili
- il conteggio ore puo restare in larga parte invariato
- test e debug aggiuntivi sono fortemente consigliati

Stima consigliata per questa strada:

- `4 - 7 giorni lavorativi`, inclusi test e verifica su casi reali

Se invece si volesse convertire davvero tutta l'applicazione a 3 soli stati interni, l'impatto salirebbe in modo netto e il beneficio non compenserebbe il rischio nella prima iterazione.
