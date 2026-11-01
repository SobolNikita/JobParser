FROM golang:1.25-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o jobparser-linux .

# Final stage with Chromium
FROM alpine:3.20

RUN apk add --no-cache \
    chromium \
    ca-certificates \
    tzdata

WORKDIR /app

COPY --from=builder /app/jobparser-linux /app/jobparser
COPY config.json /app/config.json

ENV HEADLESS_BROWSER=true
ENV CHROME_PROFILE_DIR=/app/data/chrome_profile

VOLUME ["/app/data"]

ENTRYPOINT ["/app/jobparser"]
