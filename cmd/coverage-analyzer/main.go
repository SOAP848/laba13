package main

import (
	"encoding/json"
	"os"
	"os/signal"
	"syscall"
	"time"

	"test-automation-agents/internal/natsclient"
	"test-automation-agents/pkg/models"

	natsio "github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
)

const (
	serviceName      = "coverage-analyzer"
	natsURL          = "nats://localhost:4222"
	subjectRequest   = "coverage.analyze"
	subjectResponse  = "coverage.analyzed"
	subjectCompleted = "test.completed" // слушаем завершение тестов
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
	client.CreateStream("TEST_AUTOMATION", []string{"coverage.>", "test.>"})

	// Подписка на запросы анализа покрытия
	subAnalyze, err := client.Subscribe(subjectRequest, func(msg *natsio.Msg) {
		logger.Infof("Received coverage analysis request: %s", string(msg.Data))
		var req models.AnalyzeCoverageRequest
		if err := json.Unmarshal(msg.Data, &req); err != nil {
			logger.Errorf("Failed to unmarshal request: %v", err)
			return
		}
		resp := handleAnalyzeCoverage(req, logger)
		sendResponse(client, msg, subjectResponse, resp, logger)
	})
	if err != nil {
		logger.Fatalf("Failed to subscribe to %s: %v", subjectRequest, err)
	}
	defer subAnalyze.Unsubscribe()

	// Автоматический анализ после завершения тестов
	subCompleted, err := client.Subscribe(subjectCompleted, func(msg *natsio.Msg) {
		logger.Infof("Received test completion, auto-analyzing coverage...")
		var testResp models.TestsCompletedResponse
		if err := json.Unmarshal(msg.Data, &testResp); err != nil {
			logger.Errorf("Failed to unmarshal test results: %v", err)
			return
		}
		// Автоматически запускаем анализ покрытия
		req := models.AnalyzeCoverageRequest{
			BaseMessage: natsclient.CreateBaseMessage(
				models.MessageTypeAnalyzeCoverage,
				serviceName,
				testResp.Source,
			),
			TestResults:  testResp,
			CoverageData: "coverage.out", // пример файла
			Threshold:    80.0,
		}
		resp := handleAnalyzeCoverage(req, logger)
		sendResponse(client, msg, subjectResponse, resp, logger)
	})
	if err != nil {
		logger.Errorf("Failed to subscribe to %s: %v", subjectCompleted, err)
	}
	defer subCompleted.Unsubscribe()

	logger.Infof("Service %s is listening on subjects %s, %s", serviceName, subjectRequest, subjectCompleted)

	// Ожидание сигнала завершения
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan
	logger.Info("Shutting down...")
}

func handleAnalyzeCoverage(req models.AnalyzeCoverageRequest, logger *logrus.Logger) models.CoverageAnalyzedResponse {
	start := time.Now()
	logger.Infof("Analyzing coverage with threshold %.2f%%", req.Threshold)

	// Имитация анализа покрытия
	// В реальности здесь парсился бы файл coverage.out, использовался бы go tool cover и т.д.
	files := []models.FileCoverage{
		{
			FilePath:        "main.go",
			CoveragePercent: 85.5,
			LinesCovered:    47,
			LinesTotal:      55,
		},
		{
			FilePath:        "utils.go",
			CoveragePercent: 72.0,
			LinesCovered:    36,
			LinesTotal:      50,
		},
		{
			FilePath:        "handlers.go",
			CoveragePercent: 95.0,
			LinesCovered:    57,
			LinesTotal:      60,
		},
	}

	totalLinesCovered := 0
	totalLinesTotal := 0
	for _, f := range files {
		totalLinesCovered += f.LinesCovered
		totalLinesTotal += f.LinesTotal
	}
	coveragePercent := 0.0
	if totalLinesTotal > 0 {
		coveragePercent = float64(totalLinesCovered) / float64(totalLinesTotal) * 100
	}

	meetsThreshold := coveragePercent >= req.Threshold
	var recommendations []string
	if !meetsThreshold {
		recommendations = []string{
			"Увеличьте покрытие тестами для utils.go (сейчас 72%)",
			"Добавьте тесты для edge cases в main.go",
			"Рассмотрите использование инструментов генерации тестов",
		}
	} else {
		recommendations = []string{"Покрытие соответствует требованиям. Отличная работа!"}
	}

	duration := time.Since(start)
	logger.Infof("Coverage analysis completed in %v, coverage: %.2f%%", duration, coveragePercent)

	baseMsg := natsclient.CreateBaseMessage(models.MessageTypeCoverageAnalyzed, serviceName, req.Source)
	return models.CoverageAnalyzedResponse{
		BaseMessage:     baseMsg,
		CoveragePercent: coveragePercent,
		LinesCovered:    totalLinesCovered,
		LinesTotal:      totalLinesTotal,
		Files:           files,
		MeetsThreshold:  meetsThreshold,
		Recommendations: recommendations,
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
