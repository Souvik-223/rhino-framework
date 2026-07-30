# Multi-stage build: the final image contains neither Node nor the Go
# toolchain, just the compiled rhino binary with the frontend embedded.
# See plans/web_portal.md §8.1.

# --- stage 1: build the Vue app ---
FROM node:22-alpine AS web-build
WORKDIR /frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# --- stage 2: build the Go binary (embeds backend/dist via go:embed) ---
FROM golang:1.25-alpine AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-build /frontend/dist ./backend/dist
RUN CGO_ENABLED=0 go build -o /out/rhino ./cmd/rhino

# --- stage 3: minimal runtime ---
FROM gcr.io/distroless/static-debian12
COPY --from=go-build /out/rhino /usr/local/bin/rhino
EXPOSE 8080
ENTRYPOINT ["rhino", "serve"]
