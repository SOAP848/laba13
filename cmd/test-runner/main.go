package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"test-automation-agents/internal/natsclient"
	"test-automation-agents/pkg/models"

	natsio "github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
)

const (
	serviceName      = "test-runner"
	natsURL          = "nats://localhost:4222"
	subjectRequest   = "test.run"
	subjectResponse  = "test.completed"
	subjectGenerated = "test.generated" // слушаем сообщения от генератора
)

func main() {
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	logger.Infof("Starting %s service", serviceName)

	// Подключение к NATS
	client, err := natsclient.NewClient(natsURL)
	if err != nil {
		logger.Fatalf("Failed to connect to NATS: %v", err)
	}
	defer client.Close()

	// Создаем stream для JetStream (опционально)
	client.CreateStream("TEST_AUTOMATION", []string{"test.>"})

	// Подписка на запросы запуска тестов
	subRun, err := client.Subscribe(subjectRequest, func(msg *natsio.Msg) {
		logger.Infof("Received run request: %s", string(msg.Data))
		var req models.RunTestsRequest
		if err := json.Unmarshal(msg.Data, &req); err != nil {
			logger.Errorf("Failed to unmarshal request: %v", err)
			return
		}
		resp := handleRunTests(req, logger)
		sendResponse(client, msg, subjectResponse, resp, logger)
	})
	if err != nil {
		logger.Fatalf("Failed to subscribe to %s: %v", subjectRequest, err)
	}
	defer subRun.Unsubscribe()

	// Также можно подписаться на уведомления о сгенерированных тестах для автоматического запуска
	subGenerated, err := client.Subscribe(subjectGenerated, func(msg *natsio.Msg) {
		logger.Infof("Received generated tests, auto-running...")
		var genResp models.TestsGeneratedResponse
		if err := json.Unmarshal(msg.Data, &genResp); err != nil {
			logger.Errorf("Failed to unmarshal generated tests: %v", err)
			return
		}
		// Автоматически запускаем тесты
		req := models.RunTestsRequest{
			BaseMessage: natsclient.CreateBaseMessage(
				models.MessageTypeRunTests,
				serviceName,
				genResp.Source,
			),
			TestFiles:   genResp.TestFiles,
			Environment: "local",
			Parallel:    true,
			Timeout:     30,
		}
		resp := handleRunTests(req, logger)
		sendResponse(client, msg, subjectResponse, resp, logger)
	})
	if err != nil {
		logger.Errorf("Failed to subscribe to %s: %v", subjectGenerated, err)
	}
	defer subGenerated.Unsubscribe()

	logger.Infof("Service %s is listening on subjects %s, %s", serviceName, subjectRequest, subjectGenerated)

	// Ожидание сигнала завершения
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan
	logger.Info("Shutting down...")
}

func handleRunTests(req models.RunTestsRequest, logger *logrus.Logger) models.TestsCompletedResponse {
	start := time.Now()
	logger.Infof("Running %d test files", len(req.TestFiles))

	// Имитация запуска тестов
	passed := 0
	failed := 0
	skipped := 0
	var failures []models.TestFailure

	for _, file := range req.TestFiles {
		logger.Debugf("Running tests from %s", file.Path)
		// В реальности здесь был бы вызов go test, pytest и т.д.
		// Для демо просто симулируем результат
		if file.Language == "go" {
			// Запускаем go test
			cmd := exec.Command("go", "test", "-v", "./...")
			output, err := cmd.CombinedOutput()
			if err != nil {
				failed++
				failures = append(failures, models.TestFailure{
					TestName:   "TestSuite",
					File:       file.Path,
					Error:      err.Error(),
					StackTrace: string(output),
				})
			} else {
				passed += 3 // предположим 3 теста в файле
			}
		} else {
			// Для других языков просто симулируем успех
			passed += 2
			skipped += 1
		}
	}

	duration := time.Since(start)

	baseMsg := natsclient.CreateBaseMessage(models.MessageTypeTestsCompleted, serviceName, req.Source)
	return models.TestsCompletedResponse{
		BaseMessage:   baseMsg,
		Passed:        passed,
		Failed:        failed,
		Skipped:       skipped,
		Duration:      duration,
		Logs:          fmt.Sprintf("Ran %d files, %d passed, %d failed", len(req.TestFiles), passed, failed),
		FailedDetails: failures,
	}
}

func sendResponse(client *natsclient.Client, msg *natsio.Msg, subject string, resp interface{}, logger *logrus.Logger) {
	data, err := json.Marshal(resp)
	if err != nil {
		logger.Errorf("Failed to marshal response: %v", err)
		return
	}
	if err := client.PublishResponse(msg, subject, data); err != nil {
		logger.Errorf("Failed to publish response: %v", err)
	}
}
