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
