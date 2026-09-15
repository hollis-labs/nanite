# Stage 1: Build React frontend
FROM node:20-alpine AS ui-build
WORKDIR /app/ui
COPY ui/package.json ui/package-lock.json* ./
RUN npm ci
COPY ui/ ./
RUN npm run build

# Stage 2: Build Go binary
FROM golang:1.26-alpine AS go-build
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Copy built UI into the embed directory
COPY --from=ui-build /app/ui/dist ./internal/server/ui_dist/
RUN CGO_ENABLED=0 go build -o nanite ./cmd/nanite

# Stage 3: Minimal runtime image
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
RUN adduser -D -u 1000 nanite
WORKDIR /app
COPY --from=go-build /app/nanite .
RUN mkdir -p /data && chown nanite:nanite /data
USER nanite
EXPOSE 8090
ENTRYPOINT ["./nanite", "serve", "-db", "/data/nanite.db"]
