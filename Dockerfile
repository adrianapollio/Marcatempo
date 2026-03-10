# Usa l'immagine ufficiale Go basata su Alpine Linux
FROM golang:1.21-alpine AS builder

# Imposta la directory di lavoro
WORKDIR /app

# Copia i file delle dipendenze
COPY go.mod go.sum ./

# Scarica le dipendenze
RUN go mod download

# Copia il resto dell'applicazione
COPY . .

# Compila l'applicazione disabilitando CGO (visto che usi modernc.org/sqlite che non lo richiede)
RUN CGO_ENABLED=0 GOOS=linux go build -o marcatempo .

# Crea l'immagine finale basata su Debian slim (molto più compatibile con modernc.org/sqlite)
FROM debian:bookworm-slim

# Aggiorna i pacchetti e installa tzdata (necessario per i fusi orari) e ca-certificates
RUN apt-get update && apt-get install -y tzdata ca-certificates && rm -rf /var/lib/apt/lists/*
ENV TZ=Europe/Rome

WORKDIR /app

# Copia l'eseguibile compilato dallo stage precedente
COPY --from=builder /app/marcatempo .

# Copia i file statici necessari per il frontend
COPY admin.html admin.css index.html index.css ./

# Esponi la porta 8080
EXPOSE 8080

# Comando per avviare l'applicazione
CMD ["./marcatempo"]
