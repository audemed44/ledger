FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.27-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/web.go web/web.go
COPY --from=frontend /src/web/dist/ web/dist/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /ledger ./cmd/ledger

FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata poppler-utils qpdf && adduser -D -u 10001 ledger && mkdir /data && chown ledger:ledger /data
COPY --from=backend /ledger /usr/local/bin/ledger
USER ledger
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["ledger"]
