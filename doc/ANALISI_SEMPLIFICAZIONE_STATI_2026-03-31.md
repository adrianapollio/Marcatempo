# Analisi Semplificazione Stati Marcatempo

Data analisi iniziale: 2026-03-31  
Aggiornato allo stato operativo: 2026-04-03

## 1. Obiettivo del documento

Questo documento non descrive piu una proposta futura, ma lo stato architetturale e operativo attuale della semplificazione stati lato device.

La decisione attiva e questa:

- lato terminale si usano solo 2 gruppi operativi: `IN/OUT` e `PAUSA`
- lato backend il raw device viene salvato separatamente
- lato `records` il sistema continua a salvare azioni finali esplicite
- lo storico legacy con trasferte e break resta leggibile, ma non fa piu parte del flusso operativo normale dei device

## 2. Modello operativo attuale

### 2.1 Input utente sul device

Per i terminali riprogrammati, l'utente non deve piu scegliere tra molti stati diversi.

I soli input operativi previsti sono:

- `IN/OUT`
- `PAUSA`

Nel protocollo raw semplificato questi due input vengono rappresentati come:

- `in_out`
- `pausa`

Questa differenza di scrittura non ha significato funzionale:

- `IN/OUT` e `PAUSA` sono le etichette visibili all'utente sul terminale
- `in_out` e `pausa` sono i nomi interni usati dal backend e dal salvataggio raw

Il mapping quindi non dipende dal nome testuale mostrato sul device, ma dal codice/stato raw realmente inviato dal terminale.

### 2.2 Mappatura reale dei terminali riprogrammati

Sui terminali Anviz riprogrammati a 2 tasti, il significato degli slot non coincide piu con la semantica legacy dei codici originali:

- slot/tasto `IN` del terminale -> gruppo raw `in_out`
- slot/tasto `OUT` del terminale -> gruppo raw `pausa`

Questa e la nota operativa piu importante emersa durante i test su Napoli del 2026-04-02 / 2026-04-03.

In pratica:

- `status=0` del terminale riprogrammato non va letto come semplice `In` legacy, ma come input del gruppo `in_out`
- `status=1` del terminale riprogrammato non va letto come semplice `Out` legacy, ma come input del gruppo `pausa`

## 3. Stato attuale della pipeline

La pipeline attuale e:

`device -> device_raw_records -> resolver backend -> records`

### 3.1 Salvataggio raw

Quando la semplificazione e attiva:

- `device_raw_records.action` contiene `in_out` oppure `pausa`

Quando la semplificazione non e attiva:

- `device_raw_records.action` continua a contenere i valori legacy (`In`, `Out`, `I_pausa`, `F_pausa`, `U_trasf`, `R_trasf`, `I_break`, `F_break`)

### 3.2 Risoluzione backend

Il resolver backend non lascia il gruppo raw dentro `records`.

Legge:

- il nuovo raw
- la sequenza cronologica della giornata del dipendente
- lo stato gia aperto o chiuso della sessione di lavoro
- lo stato gia aperto o chiuso della pausa

e produce uno dei soli stati finali operativi:

- `In`
- `Out`
- `I_pausa`
- `F_pausa`

### 3.3 Regole base del resolver

Per `in_out`:

- se c'e una pausa aperta -> `Out`
- se non c'e una sessione di lavoro aperta -> `In`
- se c'e una sessione di lavoro aperta -> `Out`
- se non c'e una sessione di lavoro aperta ma nella giornata esiste gia una marcatura pausa -> `Out`

Per `pausa`:

- se c'e una pausa aperta -> `F_pausa`
- se non c'e una pausa aperta ma c'e una sessione di lavoro aperta -> `I_pausa`
- se non c'e una sessione di lavoro aperta -> fallback a `I_pausa` tracciato nei log

## 4. Cosa e "semplificato" oggi e cosa no

La semplificazione attuale riguarda il flusso device.

Quindi oggi dobbiamo distinguere bene 3 livelli:

### 4.1 Livello terminale / input utente

Qui gli stati operativi sono solo 2:

- `IN/OUT`
- `PAUSA`

### 4.2 Livello raw salvato dal device

Qui i gruppi salvati sono solo 2 quando la semplificazione e attiva:

- `in_out`
- `pausa`

### 4.3 Livello finale applicativo (`records`)

Qui gli stati finali correnti del nuovo flusso device sono 4:

- `In`
- `Out`
- `I_pausa`
- `F_pausa`

Quindi la semplificazione non significa che `records` contiene solo due valori.  
Significa invece che il device espone solo due scelte utente e il backend le risolve in 4 stati finali espliciti.

Nota pratica importante:

- se il dipendente dimentica l'`In`
- ma nella giornata compare gia una `pausa`
- il successivo `in_out` non deve aprire una nuova giornata con `In` a fine turno

Per questo il resolver tratta `in_out` come chiusura (`Out`) quando esiste gia evidenza di pausa nella timeline della giornata.

## 5. Stato legacy ancora supportato

Lo storico legacy non viene riscritto.

Questo significa che nel database possono ancora esistere record storici con:

- `U_trasf`
- `R_trasf`
- `I_break`
- `F_break`

Questi record:

- restano validi per lettura storica
- restano validi per conteggi su giornate gia pregresse
- non devono piu essere prodotti dal normale flusso device semplificato

Nel nuovo flusso operativo device:

- `U_trasf` e `R_trasf` non sono piu input utente
- `I_break` e `F_break` non sono piu input utente

## 6. Configurazione richiesta

Per attivare il comportamento semplificato lato device serve:

```env
ANVIZ_SIMPLIFIED_DEVICE_RAW_ACTIONS=true
```

Questo flag deve essere presente nel container in esecuzione, non solo nei file del repository.

Nel contesto operativo attuale sono inoltre stati disattivati i flussi staff automatici e manuali:

```env
ANVIZ_AUTO_SYNC_STAFF=false
ANVIZ_MANUAL_SYNC_STAFF=false
```

## 7. Impatto su database e logica applicativa

### 7.1 Database

La struttura non richiede stravolgimenti:

- `device_raw_records` conserva il raw
- `records` conserva il finale risolto

La deduplica del raw continua a basarsi soprattutto su:

- `device_id`
- `employee_id`
- `raw_device_timestamp`
- `status_code`

### 7.2 Conteggio ore

Il motore di conteggio ore continua a leggere `records`, non `device_raw_records`.

Per questo motivo la semplificazione device non impone una riscrittura del calcolo:

- il raw viene interpretato prima
- il conteggio continua a lavorare su stati finali espliciti
- lo storico legacy resta compatibile

## 8. Esempi pratici

### 8.1 Caso lavoro normale

Input device:

- `in_out` alle `09:00`
- `in_out` alle `18:00`

Output finale:

- `In`
- `Out`

### 8.2 Caso pausa normale

Input device:

- `in_out` alle `09:00`
- `pausa` alle `13:00`
- `pausa` alle `14:00`
- `in_out` alle `18:00`

Output finale:

- `In`
- `I_pausa`
- `F_pausa`
- `Out`

## 9. Chiarimento importante

Le frasi del vecchio documento che parlavano di:

- "soluzione raccomandata"
- "futuro flusso semplificato"
- "transitorio"

vanno ormai lette come superate per il canale device operativo.

Lo stato corretto da considerare oggi e:

- il device lavora a 2 gruppi utente
- il raw salvato e a 2 gruppi
- il finale applicativo resta a 4 stati
- il legacy resta solo per compatibilita storica

## 10. Sintesi finale

La semplificazione attuale non e:

- "8 stati legacy ovunque"
- neppure "2 soli valori finali in tutto il sistema"

La semplificazione attuale e:

- **2 scelte utente sul terminale**
- **2 gruppi raw salvati dal device**
- **4 stati finali espliciti in `records`**
- **supporto storico ai vecchi stati legacy**
