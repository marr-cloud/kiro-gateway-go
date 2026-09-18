# Imagen Docker de kiro-gateway-go. Port a Go de jwadow/kiro-gateway; ver NOTICE.
#
# Build multi-stage: se compila un binario estatico (CGO_ENABLED=0) y se copia a
# una base minima distroless que ya trae certificados CA y un usuario no-root. No
# lleva Python, shell ni curl: es el motivo por el que el healthcheck usa el
# propio binario con --health (ver cmd/kiro-gateway/main.go y docs/DIFFERENCES.md).

# ---- Etapa de compilacion ----
FROM golang:1.27 AS builder

WORKDIR /src

# Las dependencias van en una capa aparte para aprovechar la cache de build:
# solo se vuelven a descargar cuando cambian go.mod o go.sum.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# VERSION se inyecta en internal/version.build igual que hace `task build`. Por
# defecto vacia: el binario reporta entonces la version por defecto de
# internal/version (2.4.dev.13+go), el mismo comportamiento que el Taskfile sin
# tags. Se puede fijar con: docker build --build-arg VERSION=$(git describe --tags).
ARG VERSION=""
ENV CGO_ENABLED=0
RUN go build -trimpath \
      -ldflags "-s -w -X github.com/marr-cloud/kiro-gateway-go/internal/version.build=${VERSION}" \
      -o /out/kiro-gateway ./cmd/kiro-gateway

# ---- Etapa de runtime ----
# distroless/static:nonroot: sin shell ni gestor de paquetes, con certificados CA
# (imprescindibles para el HTTPS saliente a Kiro/AWS) y un usuario no-root ya
# configurado (uid 65532, home /home/nonroot).
FROM gcr.io/distroless/static:nonroot

WORKDIR /app
COPY --from=builder /out/kiro-gateway /kiro-gateway

EXPOSE 8000

# --health consulta GET /health del servidor en marcha y sale 0 (sano) / 1. Con
# SERVER_HOST=0.0.0.0 el flag apunta a 127.0.0.1 (ver main.go, desviacion 2).
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/kiro-gateway", "--health"]

ENTRYPOINT ["/kiro-gateway"]
