# Stage 1: Build React frontend
FROM node:20-alpine AS ui-build
WORKDIR /app/ui
COPY ui/package.json ui/package-lock.json* ./
RUN npm ci
COPY ui/ ./
RUN npm run build

# Stage 2: Build Go binary
FROM golang:1.25-alpine AS go-build
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Copy built UI into the embed directory
COPY --from=ui-build /app/ui/dist ./internal/server/ui_dist/
RUN CGO_ENABLED=0 go build -o mentat-chat ./cmd/mentat-chat

# Stage 3: Minimal runtime image
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
RUN adduser -D -u 1000 mentat
WORKDIR /app
COPY --from=go-build /app/mentat-chat .
RUN mkdir -p /data && chown mentat:mentat /data
USER mentat
EXPOSE 8090
ENTRYPOINT ["./mentat-chat", "serve", "-db", "/data/mentat-chat.db"]
