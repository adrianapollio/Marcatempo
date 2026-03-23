# Logica di Calcolo Ore e Gestione Eccezioni

Questo documento descrive come Marcatempo calcola ore lavorate, ore teoriche e bilancio.

---

## 1. Ore Teoriche (Bilancio)

La giornata teorica standard e di **8:00** ore.

### Giorno corrente (anti-distorsione)
Per evitare un bilancio "falso" durante la giornata:
- Le ore di oggi non entrano nei totali finche la giornata non risulta chiusa.
- La giornata viene considerata chiusa quando e presente l'uscita (oppure quando una trasferta risulta completa nei casi dedicati).
- In questo modo il bilancio resta stabile fino a fine giornata.

---

## 2. Calcolo Ore Lavorate Giornaliere

Le ore vengono ricavate sommando intervalli validi:
- Entrata -> primo evento successivo coerente (pausa/trasferta/uscita)
- Fine pausa -> evento successivo coerente
- Inizio trasferta -> ritorno trasferta
- Ritorno trasferta -> uscita

Gli intervalli sovrapposti vengono uniti per evitare doppio conteggio.

---

## 3. Gestione Pausa

| Scenario | Azione del sistema |
| :--- | :--- |
| **Pausa completa** | La pausa e gia esclusa dagli intervalli, quindi nessuna sottrazione extra |
| **Pausa parziale** | Sottrazione fissa di **60 minuti** |
| **Pausa non marcata su giornata piena** | Sottrazione fissa di **60 minuti** (soglia: almeno 8:00 ore) |

---

## 4. Giornata Non Chiusa (Fallback)

Per le giornate passate non chiuse:
- Se manca la chiusura (uscita mancante o trasferta aperta), il sistema applica fallback a **8:00** ore lavorate.
- L'anomalia resta visibile nel warning (es. "Uscita mancante").

Per la giornata odierna:
- In sede, senza uscita, non viene fatta stima in tempo reale.
- Per trasferta aperta, resta attiva la stima in tempo reale solo per il segmento di trasferta.

---

## 5. Visualizzazione Anomalie (Warning)

Il triangolo giallo appare quando i dati sono incompleti o incoerenti. Esempi:
- Entrata mancante
- Uscita mancante
- Inizio/Fine pausa mancanti
- Inizio/Ritorno trasferta mancanti
- Marcature extra non allineate alla sequenza attesa

---

## 6. Normalizzazione Marcature

Le marcature vengono normalizzate in uno schema comune:
- `In` / `Out`
- `I_pausa` / `F_pausa`
- `U_trasf` / `R_trasf`

Questo mantiene coerente il calcolo ore tra dispositivo fisico e portale web.
