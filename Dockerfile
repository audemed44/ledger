# ── Frontend ────────────────────────────────────────────────────────────────
FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# ── Server ──────────────────────────────────────────────────────────────────
FROM golang:1.27-alpine AS server
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/embed.go web/
COPY --from=frontend /src/web/dist web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /ledger ./cmd/ledger

# ── Runtime ─────────────────────────────────────────────────────────────────
# qpdf decrypts statements and poppler's pdftotext extracts their text.
FROM alpine:3.23
RUN apk add --no-cache ca-certificates poppler-utils qpdf \
    && adduser -D -H -u 1000 -s /sbin/nologin ledger \
    && mkdir -p /data && chown 1000:1000 /data
# In PATH, so `docker exec ledger ledger check-statement` works.
COPY --from=server /ledger /usr/local/bin/ledger
ENV LEDGER_DATA_DIR=/data \
    LEDGER_LISTEN=:8080 \
    GOMEMLIMIT=32MiB
USER 1000:1000
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s CMD ["ledger", "healthcheck"]
ENTRYPOINT ["ledger"]
