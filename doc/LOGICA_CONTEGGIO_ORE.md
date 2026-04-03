# Logica di Calcolo Ore e Gestione Eccezioni

Questo documento descrive la logica attuale usata da Marcatempo per:
- calcolo ore lavorate giornaliere
- ore teoriche
- bilancio ore
- gestione anomalie

---

## 1. Regole Base Bilancio

- Giornata teorica standard: **8:00** ore.
- Il bilancio e calcolato come: `ore lavorate - ore teoriche`.
- Domenica esclusa dal teorico.
- Festivita e chiusure aziendali: teorico `0:00`.

---

## 2. Giorno Corrente (Anti-Distorsione)

Per evitare bilanci falsati durante la giornata in corso:
- le ore di oggi entrano nei totali solo se la giornata e considerata **chiusa** (`isFinished`)
- se non e chiusa, il bilancio giornaliero mostra neutralizzazione del teorico (evita negativi fittizi)

Una giornata e considerata chiusa quando vale almeno una condizione:
- `uscita` presente
- trasferta completa senza entrata: `inizio_trasferta + ritorno_trasferta`
- caso implicito pro-lavoratore: `entrata + ritorno_trasferta` senza `inizio_trasferta`

---

## 3. Calcolo Ore Lavorate Giornaliere

Il calcolo usa intervalli validi e poi li unisce per evitare doppio conteggio.

Intervalli usati:
- Entrata -> primo stop coerente (`uscita` / `inizio_trasferta` / `inizio_pausa` solo se pausa completa)
- Fine pausa -> evento successivo coerente (solo se pausa completa)
- Inizio trasferta -> Ritorno trasferta
- Ritorno trasferta -> Uscita (o uscita implicita quando applicabile)

Regola doppia trasferta (stabilizzazione visuale):
- se nella stessa giornata ci sono piu coppie `inizio_trasferta`/`ritorno_trasferta`
- nelle colonne principali viene mantenuta solo la prima coppia coerente (mattina)
- le timbrature trasferta successive (es. pomeriggio) vengono spostate negli extra e mostrate nel tooltip warning

Uscita implicita da ritorno trasferta:
- se c'e `entrata`
- manca `uscita`
- manca `inizio_trasferta`
- c'e `ritorno_trasferta`
allora `ritorno_trasferta` viene trattato come uscita implicita per il conteggio ore.

---

## 4. Gestione Pausa

| Scenario | Azione |
| :--- | :--- |
| Pausa completa (`inizio_pausa` + `fine_pausa`) | La pausa e gia esclusa dagli intervalli, nessuna sottrazione extra |
| Pausa parziale (solo uno dei due) | Sottrazione fissa di **60 min** |
| Nessuna pausa marcata e giornata piena | Sottrazione fissa di **60 min** se lavoro >= 8:00 |

Regola anti-distorsione importante:
- con `entrata` e `uscita` presenti, una pausa parziale **non tronca la giornata** a meta
- si calcola la giornata continua e poi si sottrae 1 ora

---

## 5. Giornata Non Chiusa (Fallback)

Per giornate passate non chiuse:
- se manca chiusura valida, fallback a **8:00** ore lavorate
- anomalia comunque visibile (es. uscita mancante)

Caso ufficio "tutte le timbrature tranne una":
- se nella giornata sono presenti 3 marcature su 4 tra `entrata`, `inizio_pausa`, `fine_pausa`, `uscita`
- e non sono coinvolte marcature trasferta
- per una giornata passata il sistema mantiene il calcolo reale quando dagli intervalli rimasti emerge gia una durata coerente
- le **8:00** ore vanno quindi intese come soglia minima/fallback, non come tetto fisso
- se il dipendente e rimasto di piu, il monte ore reale resta valido e si scala solo la pausa dovuta
- con pausa completa gli intervalli escludono gia la durata reale della pausa
- con pausa mancante o parziale viene applicata la sottrazione standard di **60 min**
- l'anomalia resta visibile nel warning

Per la giornata odierna:
- in sede senza uscita non c'e stima realtime automatica
- trasferta aperta (`inizio_trasferta` senza `ritorno_trasferta`) puo usare stima realtime per il segmento trasferta

---

## 6. Anomalie (Warning)

Il warning segnala dati incompleti/incoerenti, ad esempio:
- entrata mancante
- uscita mancante
- inizio/fine pausa mancanti
- inizio/ritorno trasferta mancanti
- marcature extra

Comportamento conservativo su `entrata` mancante:

- la UI segnala l'anomalia
- il bilancio non inventa automaticamente ore di lavoro partendo solo da `uscita` o da marcature pausa
- sulle giornate passate, se non esistono intervalli validi, il risultato resta `0:00` con bilancio tipicamente `-8:00`
- la sottrazione automatica `-1h` per pausa non marcata si applica solo quando esiste gia una giornata lavorata valida, non per creare ore dal nulla

Eccezione warning su duplicati:
- se la giornata e completa (`entrata`, `inizio_pausa`, `fine_pausa`, `uscita`)
- e gli extra sono solo duplicati di queste azioni
- il warning extra viene soppresso

Nota: la soppressione del warning non altera il calcolo ore.

---

## 7. Impatto su Riepiloghi e Export

La stessa logica `isFinished` viene usata in:
- tabella giornaliera
- riepilogo periodo
- export (CSV/PDF)

Questo evita differenze tra visualizzazione e bilanci aggregati.

---

## 8. Normalizzazione Azioni

Le timbrature vengono normalizzate in chiavi canoniche:
- `entrata` (`In`)
- `uscita` (`Out`)
- `inizio_pausa` (`I_pausa`)
- `fine_pausa` (`F_pausa`)
- `inizio_trasferta` (`U_trasf`)
- `ritorno_trasferta` (`R_trasf`)

Questa normalizzazione mantiene il calcolo coerente tra dispositivi hardware e portale web.

---

## 9. Resolver Device Semplificato

Questa sezione descrive la logica introdotta per supportare il futuro flusso device semplificato a 2 gruppi raw:

- `in_out`
- `pausa`

### 9.1 Quando entra in funzione

Il collasso del raw device a 2 gruppi non e automatico: viene attivato solo con:

- `ANVIZ_SIMPLIFIED_DEVICE_RAW_ACTIONS=true`

Sui terminali riprogrammati a 2 tasti, i codici raw non corrispondono piu direttamente ai significati legacy:

- slot `IN` del terminale -> gruppo raw `in_out`
- slot `OUT` del terminale -> gruppo raw `pausa`

Se la variabile e `false`, il device continua a produrre il raw legacy:

- `In`
- `Out`
- `I_pausa`
- `F_pausa`
- `U_trasf`
- `R_trasf`
- `I_break`
- `F_break`

### 9.2 Cosa viene salvato

La pipeline device continua a lavorare in 2 passaggi:

1. salvataggio immediato del raw in `device_raw_records`
2. risoluzione del raw in uno stato finale salvato in `records`

Questo significa che:

- `device_raw_records.action` contiene il gruppo raw (`in_out` o `pausa`) solo quando la semplificazione e attiva
- `records.action` continua invece a contenere lo stato finale usato dal resto dell'applicazione

### 9.3 Criterio del resolver

Il resolver guarda la sequenza di record finali gia presenti nella stessa giornata del dipendente e decide il significato della nuova timbratura.

Stato interno osservato:

- `workOpen`: esiste una sessione di lavoro aperta
- `pauseOpen`: esiste una pausa aperta

Regole attuali:

- raw `in_out`
  - se `workOpen = false` -> `In`
  - se `workOpen = true` -> `Out`

- raw `pausa`
  - se `pauseOpen = true` -> `F_pausa`
  - se `pauseOpen = false` e `workOpen = true` -> `I_pausa`
  - se `pauseOpen = false` e `workOpen = false` -> fallback a `I_pausa`

Il fallback finale su `pausa` senza sessione aperta e intenzionale: non blocca il salvataggio, ma lascia al sistema una traccia leggibile e diagnosticabile.

### 9.4 Compatibilita legacy

Lo storico non viene alterato.

In particolare:

- i record gia presenti con `U_trasf` e `R_trasf` restano leggibili
- i record legacy device con `I_break` e `F_break` vengono mappati a `I_pausa` e `F_pausa`
- `manual_web` e `web`, al momento, restano invariati

### 9.5 Perche il conteggio ore resta coerente

La logica di calcolo ore descritta nelle sezioni precedenti continua a lavorare su `records`, non su `device_raw_records`.

Quindi il conteggio resta coerente perche:

- il raw semplificato viene risolto prima di entrare nel flusso di calcolo
- `records` continua a contenere stati finali compatibili con il motore esistente
- per il nuovo flusso device i finali realmente prodotti restano:
  - `In`
  - `Out`
  - `I_pausa`
  - `F_pausa`
- lo storico legacy con trasferte continua a essere letto come prima

In altre parole, il motore ore non deve "capire" `in_out` o `pausa`: vede comunque stati finali gia interpretati.

### 9.6 Effetto pratico sul periodo di transizione

Nel periodo di transizione possono convivere:

- record device legacy
- record device semplificati risolti dal backend
- record manuali/web ancora con trasferte

Questo non rompe il conteggio ore, a patto che `records` resti la fonte finale unica per:

- rendering giornaliero
- warning
- riepiloghi
- export

### 9.7 Limite attuale da tenere presente

Il resolver usa una logica cronologica semplice e coerente, ma non "ripara" da solo tutti i casi patologici.

Esempi:

- doppio `in_out` in una sequenza anomala
- `pausa` come prima timbratura della giornata
- combinazioni ibride tra storico legacy e nuova sequenza raw

Questi casi non impediscono il salvataggio, ma possono produrre anomalie che restano poi visibili al livello di warning o di verifica amministrativa.
