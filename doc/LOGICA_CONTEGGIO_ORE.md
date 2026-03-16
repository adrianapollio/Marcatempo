# Logica di Calcolo Ore e Gestione Eccezioni

Questo documento descrive in dettaglio come il sistema Marcatempo calcola le ore lavorate, il bilancio teorico e come gestisce le anomalie e le dimenticanze dei dipendenti.

---

## 1. Calcolo Ore Teoriche (Bilancio)

Il bilancio mensile o del periodo selezionato si basa su una giornata lavorativa standard di **8 ore**.

### Gestione del Giorno Corrente (Anti-Distorsione)
Per evitare che il bilancio del mese in corso appaia distorto durante la giornata lavorativa, il sistema applica la seguente logica:
*   **Esclusione Totale**: Le ore lavorate oggi e le 8 ore teoriche di oggi sono **escluse** dai totali complessivi (Totale Ore Lav. e Bilancio) per tutta la durata del turno.
*   **Inclusione Post-Uscita**: Solo quando viene registrata una marcatura di **Uscita**, la giornata viene considerata "finita" e incorporata nei totali.
*   **Vantaggio**: Il bilancio rimane stabile alla situazione di ieri sera per tutto il giorno, evitando che le ore lavorate al mattino sembrino un "falso attivo" (dato che il debito di 8 ore non è ancora stato pienamente maturato).

---

## 2. Calcolo Ore Lavorate Giornaliere

La funzione principale sottrae l'orario di Entrata dall'Uscita, gestendo poi i vari segmenti.

### Scenari Standard
1.  **Entrata e Uscita Presenti**: Si calcola la differenza temporale tra i due eventi.
2.  **Trasferta**: I segmenti di trasferta (`Ritorno - Inizio`) vengono sommati alle ore ordinarie se registrati correttamente.

---

## 3. Gestione della Pausa (Pena o Fallback)

Il sistema applica una logica resiliente per la pausa pranzo (o break) per correggere le dimenticanze comuni:

| Scenario | Azione del Sistema | Note |
| :--- | :--- | :--- |
| **Pausa Completa** | Sottrazione esatta dei minuti | Es: Inizio 13:00, Fine 13:45 -> -45 min. |
| **Pausa Parziale** | Sottrazione fissa di **60 minuti** | Se manca l'Inizio o la Fine pausa. |
| **Pausa Assente (> 6h)** | Sottrazione fissa di **60 minuti** | Se l'utente lavora più di 6h senza segnare pausa. |
| **Pausa Assente (< 6h)** | Nessuna sottrazione | Assunto come part-time o mezza giornata. |

---

## 4. Gestione Dimenticanze (Uscita Mancante)

Per evitare che una dimenticanza azzeri il conteggio della giornata, sono state introdotte delle regole di fallback:

### In Sede (No Trasferta)
Se un utente dimentica di timbrare l'uscita:
*   Vengono assegnate d'ufficio **8 ore** di lavoro.
*   Viene mostrata un'anomalia "Uscita mancante".

### In Trasferta
*   **Oggi (In corso)**: Le ore vengono calcolate in **tempo reale** (ora attuale - inizio), permettendo di vedere il bilancio crescere.
*   **Giorni Passati**: Se la trasferta è rimasta aperta, il sistema assegna d'ufficio **8 ore** per chiudere il conteggio in modo sensato.

---

## 5. Visualizzazione Anomalie (Warning)

Un'icona a forma di triangolo giallo appare accanto alle ore se il sistema rileva dati incompleti. Passando il mouse sopra l'icona, il sistema specifica il problema:

*   **Entrata / Uscita mancante**
*   **Inizio / Fine pausa mancante**
*   **Inizio / Ritorno trasferta mancante**
*   **Timbrature dispari**: Indica un errore nella sequenza di timbratura fisica sul dispositivo.

*Nota per Trasferte: L'anomalia "Ritorno trasferta mancante" non viene mostrata se l'utente non ha ancora timbrato l'uscita finale (permettendo trasferte notturne).*

---

## 6. Portale Dipendenti e Normalizzazione

Per garantire coerenza tra i vari tipi di dispositivi (hardware Anviz, timbratura via web), il sistema normalizza ogni marcatura secondo uno standard canonico:
*   **In / Out**: Per l'inizio e la fine della giornata lavorativa.
*   **I_pausa / F_pausa**: Per l'inizio e la fine della pausa pranzo.
*   **U_trasf / R_trasf**: Per gli spostamenti in trasferta.

Questo assicura che il calcolo delle ore sia sempre accurato, indipendentemente dalla lingua o dalla dicitura originale usata dal dispositivo.
