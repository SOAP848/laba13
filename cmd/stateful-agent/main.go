package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"test-automation-agents/internal/common"
	"test-automation-agents/internal/natsclient"
	"test-automation-agents/pkg/models"

	natsio "github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
)

const (
	serviceName      = "stateful-agent"
	natsURL          = "nats://localhost:4222"
	redisURL         = "localhost:6379"
	subjectRequest   = "stateful.process"
	subjectResponse  = "stateful.processed"
	otelCollectorURL = "otel-collector:4317"
)

type StatefulAgent struct {
	redisClient *redis.Client
	logger      *logrus.Logger
	agentID     string
}

func main() {
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	logger.Infof("Starting %s service", serviceName)

	// Инициализация OpenTelemetry
	shutdown, err := common.InitTracer(serviceName, otelCollectorURL)
	if err != nil {
		logger.Warnf("Failed to initialize OpenTelemetry: %v", err)
	} else {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := shutdown(ctx); err != nil {
				logger.Errorf("Failed to shutdown tracer: %v", err)
			}
		}()
	}

	// Подключение к Redis
	redisClient := redis.NewClient(&redis.Options{
		Addr:     redisURL,
		Password: "", // нет пароля
		DB:       0,
	})
	defer redisClient.Close()

	// Проверка подключения к Redis
	ctx := context.Background()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		logger.Fatalf("Failed to connect to Redis: %v", err)
	}
	logger.Info("Connected to Redis")

	// Восстановление состояния
	agentID := getAgentID()
	agent := &StatefulAgent{
		redisClient: redisClient,
		logger:      logger,
		agentID:     agentID,
	}
	agent.restoreState(ctx)

	// Подключение к NATS
	client, err := natsclient.NewClient(natsURL)
	if err != nil {
		logger.Fatalf("Failed to connect to NATS: %v", err)
	}
	defer client.Close()

	// Создаем stream для JetStream (опционально)
	client.CreateStream("TEST_AUTOMATION", []string{"stateful.>"})

	// Подписка на запросы
	sub, err := client.Subscribe(subjectRequest, func(msg *natsio.Msg) {
		ctx, span := common.StartSpan(context.Background(), "stateful-agent.handle-request")
		defer span.End()

		span.SetAttributes(
			attribute.String("agent.id", agentID),
			attribute.String("subject", msg.Subject),
		)

		logger.Infof("Received stateful request: %s", string(msg.Data))

		var req models.BaseMessage
		if err := json.Unmarshal(msg.Data, &req); err != nil {
			logger.Errorf("Failed to unmarshal request: %v", err)
			span.RecordError(err)
			return
		}

		// Обработка запроса с сохранением состояния
		resp := agent.processRequest(ctx, req)

		// Отправка ответа
		respData, err := json.Marshal(resp)
		if err != nil {
			logger.Errorf("Failed to marshal response: %v", err)
			span.RecordError(err)
			return
		}

		if err := client.PublishResponse(msg, subjectResponse, respData); err != nil {
			logger.Errorf("Failed to publish response: %v", err)
			span.RecordError(err)
		}
	})
	if err != nil {
		logger.Fatalf("Failed to subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	logger.Infof("Service %s (ID: %s) is listening on subject %s", serviceName, agentID, subjectRequest)

	// Периодическое сохранение состояния (каждые 30 секунд)
	go agent.periodicSaveState(ctx, 30*time.Second)

	// Ожидание сигнала завершения
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan
	logger.Info("Shutting down...")
	agent.saveState(ctx) // финальное сохранение
}

func getAgentID() string {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	return fmt.Sprintf("%s-%d", hostname, time.Now().Unix())
}

func (a *StatefulAgent) processRequest(ctx context.Context, req models.BaseMessage) models.BaseMessage {
	_, span := common.StartSpan(ctx, "stateful-agent.process")
	defer span.End()

	// Увеличиваем счётчик обработанных задач
	processedKey := fmt.Sprintf("agent:%s:processed", a.agentID)
	processed, err := a.redisClient.Incr(ctx, processedKey).Result()
	if err != nil {
		a.logger.Errorf("Failed to increment processed counter: %v", err)
		span.RecordError(err)
	} else {
		span.SetAttributes(attribute.Int64("processed.count", processed))
	}

	// Сохраняем последнюю задачу
	lastTaskKey := fmt.Sprintf("agent:%s:last_task", a.agentID)
	a.redisClient.Set(ctx, lastTaskKey, req.ID, 24*time.Hour)

	// Возвращаем ответ
	return natsclient.CreateBaseMessage("stateful_processed", serviceName, req.Source)
}

func (a *StatefulAgent) saveState(ctx context.Context) {
	processedKey := fmt.Sprintf("agent:%s:processed", a.agentID)
	processed, err := a.redisClient.Get(ctx, processedKey).Result()
	if err != nil && err != redis.Nil {
		a.logger.Errorf("Failed to get processed count: %v", err)
		return
	}

	stateKey := fmt.Sprintf("agent:%s:state", a.agentID)
	state := map[string]interface{}{
		"agent_id":        a.agentID,
		"processed_tasks": processed,
		"last_saved":      time.Now().Format(time.RFC3339),
		"status":          "idle",
	}
	data, _ := json.Marshal(state)
	a.redisClient.Set(ctx, stateKey, data, 24*time.Hour)
	a.logger.Debugf("State saved: %s", string(data))
}

func (a *StatefulAgent) restoreState(ctx context.Context) {
	stateKey := fmt.Sprintf("agent:%s:state", a.agentID)
	data, err := a.redisClient.Get(ctx, stateKey).Result()
	if err != nil {
		if err == redis.Nil {
			a.logger.Info("No previous state found, starting fresh")
			return
		}
		a.logger.Errorf("Failed to restore state: %v", err)
		return
	}

	var state map[string]interface{}
	if err := json.Unmarshal([]byte(data), &state); err != nil {
		a.logger.Errorf("Failed to unmarshal state: %v", err)
		return
	}
	a.logger.Infof("State restored: %v", state)
}

func (a *StatefulAgent) periodicSaveState(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			a.saveState(ctx)
		case <-ctx.Done():
			return
		}
	}
}

