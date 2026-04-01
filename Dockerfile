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

# Compila anche il tool per impostare l'admin
RUN CGO_ENABLED=0 GOOS=linux go build -o make_admin scripts/make_admin.go
RUN CGO_ENABLED=0 GOOS=linux go build -o make_system_admin scripts/make_system_admin.go

# Compila lo script per inserire marcature device-like
RUN CGO_ENABLED=0 GOOS=linux go build -o insert_device_record scripts/insert_device_record.go
RUN CGO_ENABLED=0 GOOS=linux go build -o import_anviz_extract scripts/import_anviz_extract.go
RUN CGO_ENABLED=0 GOOS=linux go build -o fix_anviz_extract_dst scripts/fix_anviz_extract_dst.go

# Crea l'immagine finale basata su Debian slim (molto più compatibile con modernc.org/sqlite)
FROM debian:bookworm-slim

# Aggiorna i pacchetti e installa tzdata (necessario per i fusi orari) e ca-certificates
RUN apt-get update && apt-get install -y tzdata ca-certificates && rm -rf /var/lib/apt/lists/*
ENV TZ=Europe/Rome

WORKDIR /app

# Copia gli eseguibili compilati dallo stage precedente
COPY --from=builder /app/marcatempo .
COPY --from=builder /app/make_admin .
COPY --from=builder /app/make_system_admin .
COPY --from=builder /app/insert_device_record .
COPY --from=builder /app/import_anviz_extract .
COPY --from=builder /app/fix_anviz_extract_dst .

# Copia i file statici necessari per il frontend
COPY admin.html admin.css index.html index.css system_admin.html system_admin.css ./

# Esponi la porta 8080
EXPOSE 8080

# Comando per avviare l'applicazione
CMD ["./marcatempo"]
