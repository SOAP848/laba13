package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"test-automation-agents/internal/common"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	natsio "github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
)

const (
	serviceName        = "scaler"
	natsURL            = "nats://localhost:4222"
	otelCollectorURL   = "otel-collector:4317"
	monitorSubject     = "test.generate" // мониторим этот топик для нагрузки
	scaleUpThreshold   = 5               // если сообщений в очереди больше этого числа
	scaleDownThreshold = 1               // если меньше этого числа
	maxInstances       = 5
	agentImage         = "test-automation-agents:latest" // образ агента (наш собственный)
	agentServiceName   = "test-generator"                // масштабируемый агент
)

type Scaler struct {
	dockerClient  *client.Client
	natsClient    *natsio.Conn
	logger        *logrus.Logger
	instanceCount int
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

	// Подключение к Docker
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		logger.Fatalf("Failed to create Docker client: %v", err)
	}
	defer dockerClient.Close()

	// Подключение к NATS
	natsConn, err := natsio.Connect(natsURL)
	if err != nil {
		logger.Fatalf("Failed to connect to NATS: %v", err)
	}
	defer natsConn.Close()

	scaler := &Scaler{
		dockerClient:  dockerClient,
		natsClient:    natsConn,
		logger:        logger,
		instanceCount: 1, // начальное количество экземпляров
	}

	// Проверяем текущее количество контейнеров
	scaler.updateInstanceCount(context.Background())

	logger.Infof("Initial instance count: %d", scaler.instanceCount)

	// Подписка на мониторинг очереди (просто для активации)
	_, err = natsConn.Subscribe(monitorSubject, func(msg *natsio.Msg) {})
	if err != nil {
		logger.Errorf("Failed to subscribe to monitor subject: %v", err)
	}

	// Запуск периодического мониторинга
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Обработка сигналов завершения
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			scaler.monitorAndScale(context.Background())
		case <-sigChan:
			logger.Info("Shutting down scaler...")
			return
		}
	}
}

func (s *Scaler) monitorAndScale(ctx context.Context) {
	ctx, span := common.StartSpan(ctx, "scaler.monitor-and-scale")
	defer span.End()

	// Получаем статистику очереди (пример: количество сообщений в JetStream)
	js, err := s.natsClient.JetStream()
	if err != nil {
		s.logger.Errorf("Failed to get JetStream context: %v", err)
		span.RecordError(err)
		return
	}

	streamInfo, err := js.StreamInfo("TEST_AUTOMATION")
	if err != nil {
		s.logger.Errorf("Failed to get stream info: %v", err)
		span.RecordError(err)
		return
	}

	// Оцениваем нагрузку по количеству сообщений в stream
	msgCount := int64(streamInfo.State.Msgs)
	span.SetAttributes(
		attribute.Int64("queue.messages", msgCount),
		attribute.Int("current.instances", s.instanceCount),
	)

	s.logger.Debugf("Queue messages: %d, Instances: %d", msgCount, s.instanceCount)

	// Логика масштабирования
	if msgCount > int64(scaleUpThreshold) && s.instanceCount < maxInstances {
		s.logger.Infof("High load detected (%d messages), scaling up", msgCount)
		s.scaleUp(ctx)
	} else if msgCount < int64(scaleDownThreshold) && s.instanceCount > 1 {
		s.logger.Infof("Low load (%d messages), scaling down", msgCount)
		s.scaleDown(ctx)
	}
}

func (s *Scaler) scaleUp(ctx context.Context) {
	ctx, span := common.StartSpan(ctx, "scaler.scale-up")
	defer span.End()

	newInstanceName := fmt.Sprintf("%s-%d", agentServiceName, s.instanceCount+1)
	s.logger.Infof("Starting new instance: %s", newInstanceName)

	// Конфигурация контейнера
	config := &container.Config{
		Image: agentImage,
		Cmd:   []string{"./" + agentServiceName},
		Env: []string{
			"NATS_URL=nats://nats:4222",
			"OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317",
			"OTEL_SERVICE_NAME=" + agentServiceName,
		},
	}
	hostConfig := &container.HostConfig{
		NetworkMode: "test-automation-net",
	}

	resp, err := s.dockerClient.ContainerCreate(ctx, config, hostConfig, nil, nil, newInstanceName)
	if err != nil {
		s.logger.Errorf("Failed to create container: %v", err)
		span.RecordError(err)
		return
	}

	if err := s.dockerClient.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		s.logger.Errorf("Failed to start container: %v", err)
		span.RecordError(err)
		return
	}

	s.instanceCount++
	s.logger.Infof("Container %s started successfully", newInstanceName)
	span.SetAttributes(attribute.String("container.id", resp.ID))
}

func (s *Scaler) scaleDown(ctx context.Context) {
	ctx, span := common.StartSpan(ctx, "scaler.scale-down")
	defer span.End()

	// Получаем список контейнеров
	filter := filters.NewArgs()
	filter.Add("name", agentServiceName)
	containers, err := s.dockerClient.ContainerList(ctx, types.ContainerListOptions{
		Filters: filter,
	})
	if err != nil {
		s.logger.Errorf("Failed to list containers: %v", err)
		span.RecordError(err)
		return
	}

	if len(containers) <= 1 {
		s.logger.Info("Only one instance left, cannot scale down")
		return
	}

	// Останавливаем последний контейнер
	containerToRemove := containers[len(containers)-1]
	s.logger.Infof("Stopping container %s", containerToRemove.Names[0])

	timeout := 5
	if err := s.dockerClient.ContainerStop(ctx, containerToRemove.ID, container.StopOptions{Timeout: &timeout}); err != nil {
		s.logger.Errorf("Failed to stop container: %v", err)
		span.RecordError(err)
		return
	}

	if err := s.dockerClient.ContainerRemove(ctx, containerToRemove.ID, types.ContainerRemoveOptions{}); err != nil {
		s.logger.Errorf("Failed to remove container: %v", err)
		span.RecordError(err)
		return
	}

	s.instanceCount--
	s.logger.Infof("Container %s removed", containerToRemove.Names[0])
	span.SetAttributes(attribute.String("container.id", containerToRemove.ID))
}

func (s *Scaler) updateInstanceCount(ctx context.Context) {
	filter := filters.NewArgs()
	filter.Add("name", agentServiceName)
	containers, err := s.dockerClient.ContainerList(ctx, types.ContainerListOptions{
		Filters: filter,
	})
	if err != nil {
		s.logger.Errorf("Failed to list containers: %v", err)
		return
	}
	s.instanceCount = len(containers)
	s.logger.Debugf("Updated instance count: %d", s.instanceCount)
}
