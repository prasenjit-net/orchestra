# ── Stage 1: build the React UI ───────────────────────────────────────────────
FROM node:22-alpine AS ui-builder

WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json* ./
RUN npm ci --prefer-offline

COPY ui/ ./
RUN npm run build

# ── Stage 2: build the Go binary (embeds ui/dist from stage 1) ────────────────
FROM golang:1.25-alpine AS go-builder

# git is needed for `go generate` / version detection via git describe
RUN apk add --no-cache git

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

# Copy source, then overlay the freshly built UI dist so //go:embed finds it
COPY . .
COPY --from=ui-builder /src/ui/dist ./ui/dist

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w \
      -X github.com/prasenjit-net/orchestra/internal/version.Version=${VERSION} \
      -X github.com/prasenjit-net/orchestra/internal/version.Commit=${COMMIT} \
      -X github.com/prasenjit-net/orchestra/internal/version.BuildDate=${BUILD_DATE}" \
    -o /orchestra .

# ── Stage 3: minimal runtime image ────────────────────────────────────────────
FROM alpine:3.21

# ca-certificates needed for outbound HTTPS calls in activities
RUN apk add --no-cache ca-certificates tzdata

# Non-root user
RUN addgroup -S orchestra && adduser -S orchestra -G orchestra

COPY --from=go-builder /orchestra /usr/local/bin/orchestra

USER orchestra
WORKDIR /home/orchestra

EXPOSE 8080
EXPOSE 8081

ENTRYPOINT ["orchestra"]
CMD ["serve"]
