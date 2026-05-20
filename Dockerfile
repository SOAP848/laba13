# Базовый образ Go
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Копируем go.mod и go.sum
COPY go.mod go.sum ./
RUN go mod download

# Копируем исходный код
COPY . .

# Сборка всех агентов
RUN go build -o /app/bin/test-generator ./cmd/test-generator
RUN go build -o /app/bin/test-runner ./cmd/test-runner
RUN go build -o /app/bin/coverage-analyzer ./cmd/coverage-analyzer
RUN go build -o /app/bin/orchestrator ./cmd/orchestrator
RUN go build -o /app/bin/stateful-agent ./cmd/stateful-agent
RUN go build -o /app/bin/scaler ./cmd/scaler
RUN go build -o /app/bin/auctioneer ./cmd/auctioneer
RUN go build -o /app/bin/web-panel ./cmd/web-panel

# Финальный образ
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Копируем бинарники
COPY --from=builder /app/bin/test-generator .
COPY --from=builder /app/bin/test-runner .
COPY --from=builder /app/bin/coverage-analyzer .
COPY --from=builder /app/bin/orchestrator .
COPY --from=builder /app/bin/stateful-agent .
COPY --from=builder /app/bin/scaler .
COPY --from=builder /app/bin/auctioneer .
COPY --from=builder /app/bin/web-panel .

# Экспортируем порты (если нужно)
EXPOSE 8080 8000

# Команда по умолчанию (будет переопределена в docker-compose)
CMD ["./test-generator"]