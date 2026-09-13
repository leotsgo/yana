# syntax=docker/dockerfile:1

# Stage 1: build the browser client.
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: build the server with the client embedded.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/yana ./cmd/yana

# Stage 3: runtime. Alpine so ripgrep is available for regex search. The
# binary is static; a `FROM scratch` image works too and only loses the
# `raw=` search endpoint (it answers 501 without rg).
FROM alpine:3.21
RUN apk add --no-cache ripgrep ca-certificates tzdata \
    && mkdir -p /notes
COPY --from=build /out/yana /usr/local/bin/yana
ENV YANA_NOTES_ROOT=/notes \
    YANA_LISTEN=:8080
VOLUME ["/notes"]
EXPOSE 8080
# Runs as root by default so a freshly created bind mount is writable (the
# scanner writes ids into frontmatter). Set `user:` in compose to drop it.
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/readyz || exit 1
ENTRYPOINT ["yana"]
CMD ["serve"]
