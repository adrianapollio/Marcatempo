# 🔒 Audit di Sicurezza — Marcatempo

**Data:** 3 Marzo 2026  
**Scope:** Backend Go (`main.go`, `db.go`, `anviz_sync.go`) + Frontend (`index.html`, `admin.html`)

---

## Riepilogo Esecutivo

L'applicazione presenta **vulnerabilità significative** in diverse aree. Le più critiche riguardano l'autenticazione (assenza di sessioni/token), l'autorizzazione (endpoint admin non protetti) e la protezione dei dati (PIN in chiaro). Di seguito ogni issue è classificata per **severità** e **priorità di intervento**.

---

## 🔴 Severità CRITICA

### 1. Nessuna Gestione Sessioni — Autenticazione Client-Side

| Dettaglio | |
|---|---|
| **File** | [admin.html](admin.html) |
| **Problema** | l'autenticazione è gestita interamente lato client tramite `localStorage`. Il server non emette sessioni né token JWT. |
| **Impatto** | Un utente può manipolare `localStorage` nel browser per fingersi admin: `localStorage.setItem('isAdmin', 'true')` → accesso completo al pannello admin. |
| **Rimedio** | Implementare sessioni server-side (cookie `HttpOnly`/`Secure`/`SameSite`) o token JWT con firma HMAC/RSA e scadenza. |

### 2. Endpoint Admin Senza Autenticazione Server-Side

| Dettaglio | |
|---|---|
| **File** | [main.go](main.go) — `handleAttendances`, `handleEmployees`, `handlePendingValidations` |
| **Problema** | `/api/attendances`, `/api/employees`, `/api/admin/pending-validations` sono accessibili **senza alcuna autenticazione**. Chiunque con l'URL può leggere tutti i dati. |
| **Impatto** | Leak completo dei dati di tutti i dipendenti, presenze, coordinate GPS, nomi, ID. |
| **Rimedio** | Aggiungere un middleware di autenticazione a tutti gli endpoint sensibili. Verificare sessione/token prima di servire dati. |

### 3. PIN Salvati in Chiaro nel Database e nel Browser

| Dettaglio | |
|---|---|
| **File** | [db.go](db.go) — `VerifyPIN`, [index.html](index.html) |
| **Problema** | I PIN sono salvati come testo in chiaro nella tabella `employees`. Il PIN viene anche salvato in `localStorage` nel browser. Il confronto avviene con `dbPin == pin` (stringa). |
| **Impatto** | Se il database viene compromesso, tutti i PIN sono esposti. Chiunque acceda al browser può leggere il PIN dal `localStorage`. |
| **Rimedio** | Hashare i PIN con `bcrypt` (o `argon2`) prima del salvataggio. Confrontare con `bcrypt.CompareHashAndPassword()`. Eliminare il PIN da `localStorage` e usare un token di sessione al suo posto. |

---

## 🟠 Severità ALTA

### 4. Autorizzazione Admin Solo con `IsEmployeeAdmin(adminId)` — Senza Verifica PIN

| Dettaglio | |
|---|---|
| **File** | [main.go](main.go) |
| **Problema** | `handleAdminManualClock` e `handleEditAdminManualClock` verificano solo `IsEmployeeAdmin(data.AdminID)` — cioè controllano che l'ID fornito nel body sia di un admin, **senza verificare che chi fa la richiesta sia effettivamente quell'admin**. |
| **Impatto** | Chiunque può inviare `{"adminId": 15, ...}` e inserire/modificare marcature, basta conoscere l'ID di un admin. |
| **Rimedio** | Validare l'identità dell'admin tramite sessione/token, non tramite un campo auto-dichiarato nel payload. |

### 5. Assenza di HTTPS

| Dettaglio | |
|---|---|
| **File** | [main.go](main.go) |
| **Problema** | Il server usa `http.ListenAndServe` (HTTP in chiaro). PIN, credenziali e dati sensibili viaggiano in chiaro sulla rete. |
| **Impatto** | Vulnerabile a sniffing di rete e attacchi Man-in-the-Middle (MitM). |
| **Rimedio** | Usare `http.ListenAndServeTLS` con certificato valido, oppure posizionare un reverse proxy (Nginx/Caddy) davanti con TLS. |

### 6. Credenziali in Header HTTP in Chiaro

| Dettaglio | |
|---|---|
| **File** | [main.go](main.go) |
| **Problema** | `handleEmployeeAttendances` richiede `X-Employee-ID` e `X-Employee-PIN` come header custom. Questi header sono loggate, visibili a proxy, e trasmessi in chiaro senza HTTPS. |
| **Impatto** | I PIN sono intercettabili in transito e possono finire nei log di eventuali proxy intermedi. |
| **Rimedio** | Sostituire con autenticazione a token/sessione. Il PIN dovrebbe essere usato solo al momento del login. |

### 7. XSS (Cross-Site Scripting) tramite `innerHTML`

| Dettaglio | |
|---|---|
| **File** | [admin.html](admin.html) |
| **Problema** | Dati provenienti dall'API (es. `e.name`, `v.employee_name`) vengono inseriti in `innerHTML` senza sanitizzazione. |
| **Impatto** | Se un nome dipendente contiene `<script>alert('xss')</script>`, il codice viene eseguito nel browser di chi visualizza la pagina admin. |
| **Rimedio** | Usare `textContent` o funzione di escape HTML. Evitare template literal con `innerHTML` per dati non fidati. |

---

## 🟡 Severità MEDIA

### 8. Nessuna Protezione CSRF

| Dettaglio | |
|---|---|
| **File** | Tutti gli handler POST/PUT/DELETE in [main.go](main.go) |
| **Problema** | Nessun token CSRF è richiesto per le operazioni mutanti. Un sito malevolo può far eseguire azioni all'utente loggato. |
| **Impatto** | Un attaccante potrebbe approvare/rifiutare validazioni, inserire marcature o cancellare festività a nome dell'utente. |
| **Rimedio** | Implementare token CSRF (es. `gorilla/csrf`, double-submit cookie, oppure header custom + check `Origin`/`Referer`). |

### 9. Nessun Header di Sicurezza HTTP

| Dettaglio | |
|---|---|
| **File** | [main.go](main.go) |
| **Problema** | Mancano: `Content-Security-Policy`, `X-Content-Type-Options`, `X-Frame-Options`, `Strict-Transport-Security`, `Referrer-Policy`. |
| **Impatto** | L'applicazione è vulnerabile a clickjacking (iframe), MIME-type sniffing, e manca la protezione XSS del browser. |
| **Rimedio** | Aggiungere un middleware che imposti gli header di sicurezza su ogni risposta. |

### 10. Nessun Rate Limiting

| Dettaglio | |
|---|---|
| **File** | [main.go](main.go) — `handleLogin` |
| **Problema** | Nessun limite sui tentativi di login. Un attaccante può fare brute force sui PIN. |
| **Impatto** | I PIN numerici corti (3-6 cifre) possono essere indovinati in pochi minuti senza rate limiting. |
| **Rimedio** | Implementare rate limiting per IP/utente (es. max 5 tentativi/minuto), lockout dopo tentativi falliti. |

### 11. Librerie Esterne da CDN senza Subresource Integrity (SRI)

| Dettaglio | |
|---|---|
| **File** | [admin.html](admin.html) |
| **Problema** | Flatpickr, jsPDF e AutoTable sono caricati da CDN **senza attributi `integrity`**. |
| **Impatto** | Se il CDN viene compromesso, codice malevolo può essere iniettato nell'applicazione. |
| **Rimedio** | Aggiungere attributi `integrity` e `crossorigin`. |

### 12. Database SQLite File Accessibile

| Dettaglio | |
|---|---|
| **File** | [db.go](db.go) — `attendance.db` |
| **Problema** | `attendance.db` si trova nella stessa directory dell'applicazione. |
| **Impatto** | Accesso completo a tutti i dati: PIN in chiaro, presenze, coordinate GPS. |
| **Rimedio** | Spostare il DB in una directory non servita, con permessi restrittivi. |

---

## 🟢 Severità BASSA

### 13. Log Eccessivi con Dati Sensibili

| Dettaglio | |
|---|---|
| **File** | [main.go](main.go), [anviz_sync.go](anviz_sync.go) |
| **Problema** | PIN e ID admin loggati indiscriminatamente. |
| **Impatto** | I log possono esporre dati sensibili. |
| **Rimedio** | Rimuovere PIN e dati sensibili dai log. |

---

## ✅ Aspetti Positivi

| Aspetto | Dettaglio |
|---|---|
| **Query Parametriche** | Tutte le query SQL usano placeholder `?` → **nessuna SQL Injection** |
| **PIN non esposto in JSON** | Il campo PIN dell'Employee è annotato `json:"-"` → non serializzato nelle risposte API |
| **Validazione Source** | L'edit record verifica `source = 'manual_web'` → le marcature device non sono modificabili |

---

## 📊 Matrice di Priorità

| # | Vulnerabilità | Severità | Sforzo | Priorità |
|---|---|---|---|---|
| 1 | Nessuna gestione sessioni | 🔴 Critica | Alto | **P0** |
| 2 | Endpoint admin senza auth | 🔴 Critica | Medio | **P0** |
| 3 | PIN in chiaro (DB + localStorage) | 🔴 Critica | Medio | **P0** |
| 4 | Auth admin solo con ID nel body | 🟠 Alta | Medio | **P1** |
| 5 | Nessun HTTPS | 🟠 Alta | Basso | **P1** |
| 7 | XSS via innerHTML | 🟠 Alta | Basso | **P1** |

---

> [!IMPORTANT]
> Le vulnerabilità **P0** consentono a chiunque di accedere a tutti i dati dell'applicazione. Si consiglia di affrontarle **immediatamente**.

---

## 🔑 Piano di Integrazione Keycloak (SSO / OAuth2)

L'integrazione di Keycloak (soluzione IAM - Identity and Access Management) è la strategia raccomandata per mitigare le vulnerabilità critiche (P0 e P1) relative all'autenticazione e all'autorizzazione, sostituendo la gestione artigianale dei PIN sul web con uno standard enterprise (OAuth2 / OpenID Connect).

### Architettura Proposta
1. **Frontend (Browser):** Utilizzerà `keycloak-js` (o un semplice adapter OIDC) per gestire il login. Gli utenti non inseriranno più il PIN dell'orologio marcatempo, ma accederanno con il proprio account aziendale su Keycloak.
2. **Backend (Go):** Esporrà le API protette. Ogni richiesta dal frontend dovrà includere un token JWT (scadenza breve) rilasciato da Keycloak. Il backend validerà il JWT (verificandone la firma tramite le chiavi pubbliche di Keycloak) prima di processare la richiesta.
3. **Database (SQLite):** I PIN numerici resteranno nel DB al solo fine di mantenere la sincronizzazione con i dispositivi fisici (timbratrici Anviz), ma non saranno più usati o esposti sul web.
4. **Autorizzazione:** I permessi di amministrazione saranno gestiti tramite ruoli su Keycloak (es. ruolo `marcatempo-admin`). Il backend Go verificherà la presenza di questo ruolo all'interno del token JWT per consentire l'accesso agli endpoint di amministrazione.

### Fasi di Implementazione

#### Fase 1: Configurazione Keycloak e Preparazione Database
*   [ ] Creare un nuovo Client "marcatempo" (confidenziale o public con PKCE) sul realm Keycloak esistente.
*   [ ] Configurare ruoli specifici (es. `admin_marcatempo`).
*   [ ] Alterare la tabella `employees` nel database SQLite aggiungendo una colonna `email` o `keycloak_sub` (Subject ID) per mappare in modo univoco l'utente Keycloak all'ID dipendente del dispositivo fisico.

#### Fase 2: Messa in Sicurezza del Backend Go (`main.go`)
*   [ ] Introdurre un middleware di validazione JWT (es. tramite librerie standard o specifiche OIDC per Go) su tutte le route API (sia user che admin).
*   [ ] Rimuovere l'endpoint obsoleto `/api/login`, delegando completamente l'autenticazione a Keycloak.
*   [ ] Modificare l'autorizzazione degli endpoint admin: i controlli non avverranno più basandosi su ID forniti nel payload, ma sull'estrazione dei ruoli (Claims) direttamente dal token JWT emesso da Keycloak.
*   [ ] Rimuovere l'uso degli Header HTTP in chiaro (`X-Employee-ID`, `X-Employee-PIN`).

#### Fase 3: Adeguamento del Frontend (`index.html` e `admin.html`)
*   [ ] Includere la libreria `keycloak-js` (o pacchetto via CDN/NPM).
*   [ ] Sostituire le form di login attuali con il redirect verso l'Authorization Server di Keycloak.
*   [ ] Gestire il rientro dal login con l'acquisizione dell'access token e refresh token.
*   [ ] Aggiungere il token JWT (come `Authorization: Bearer <token>`) in tutte le chiamate `fetch()` verso le API Go.
*   [ ] Rimuovere qualsiasi salvataggio di PIN o ruoli (`isAdmin`) dal `localStorage`.

#### Fase 4: Test e Rilascio
*   [ ] Testare il flusso di login/logout utente e admin.
*   [ ] Verificare il corretto funzionamento della validazione del token e il suo eventuale refresh.
*   [ ] Verificare che gli accessi non autorizzati alle API restituiscano correttamente `HTTP 401 Unauthorized` o `HTTP 403 Forbidden`.
*   [ ] Rimuovere logiche e funzioni di fallback non più necessarie (es. `VerifyPIN` per il web, mantenendolo solo se necessario per gli script di sincronizzazione).
