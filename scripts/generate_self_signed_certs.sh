 #!/bin/sh

set -eu

usage() {
  cat <<'EOF'
Uso:
  ./scripts/generate_self_signed_certs.sh <ip-o-hostname> [output-dir]

Esempi:
  ./scripts/generate_self_signed_certs.sh 192.168.1.106
  ./scripts/generate_self_signed_certs.sh marcatempo.local ./certs

Variabili opzionali:
  CERT_DAYS=825            Durata del certificato in giorni
  CERT_COUNTRY=IT          Country del subject
  CERT_STATE=Italia        State del subject
  CERT_LOCALITY=Local      Locality del subject
  CERT_ORG=Marcatempo      Organization del subject
EOF
}

if [ "${1:-}" = "" ] || [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
  usage
  exit 0
fi

if ! command -v openssl >/dev/null 2>&1; then
  echo "Errore: openssl non trovato. Su Alpine installalo con: apk add openssl" >&2
  exit 1
fi

TARGET="$1"
OUTPUT_DIR="${2:-./certs}"

CERT_DAYS="${CERT_DAYS:-825}"
CERT_COUNTRY="${CERT_COUNTRY:-IT}"
CERT_STATE="${CERT_STATE:-Italia}"
CERT_LOCALITY="${CERT_LOCALITY:-Local}"
CERT_ORG="${CERT_ORG:-Marcatempo}"

mkdir -p "$OUTPUT_DIR"

timestamp="$(date +%Y%m%d-%H%M%S)"

backup_if_exists() {
  file_path="$1"
  if [ -f "$file_path" ]; then
    mv "$file_path" "$file_path.$timestamp.bak"
  fi
}

backup_if_exists "$OUTPUT_DIR/server.crt"
backup_if_exists "$OUTPUT_DIR/server.key"

is_ipv4() {
  printf '%s' "$1" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$'
}

SAN_ENTRIES="DNS:localhost,IP:127.0.0.1"
if is_ipv4 "$TARGET"; then
  SAN_ENTRIES="IP:$TARGET,$SAN_ENTRIES"
else
  SAN_ENTRIES="DNS:$TARGET,$SAN_ENTRIES"
fi

SUBJECT="/C=$CERT_COUNTRY/ST=$CERT_STATE/L=$CERT_LOCALITY/O=$CERT_ORG/CN=$TARGET"

openssl req \
  -x509 \
  -nodes \
  -newkey rsa:2048 \
  -sha256 \
  -days "$CERT_DAYS" \
  -keyout "$OUTPUT_DIR/server.key" \
  -out "$OUTPUT_DIR/server.crt" \
  -subj "$SUBJECT" \
  -addext "subjectAltName=$SAN_ENTRIES" \
  -addext "keyUsage=digitalSignature,keyEncipherment" \
  -addext "extendedKeyUsage=serverAuth"

chmod 600 "$OUTPUT_DIR/server.key"
chmod 644 "$OUTPUT_DIR/server.crt"

echo "Certificati generati in: $OUTPUT_DIR"
echo " - $OUTPUT_DIR/server.crt"
echo " - $OUTPUT_DIR/server.key"
echo
echo "SAN inclusi: $SAN_ENTRIES"
echo
echo "Riavvio consigliato:"
echo "  docker compose up -d --build nginx"
