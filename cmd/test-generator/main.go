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
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const (
	serviceName      = "test-generator"
	natsURL          = "nats://localhost:4222"
	subjectRequest   = "test.generate"
	subjectResponse  = "test.generated"
	otelCollectorURL = "otel-collector:4317"
)

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

	// Подключение к NATS
	client, err := natsclient.NewClient(natsURL)
	if err != nil {
		logger.Fatalf("Failed to connect to NATS: %v", err)
	}
	defer client.Close()

	// Создаем stream для JetStream (опционально)
	client.CreateStream("TEST_AUTOMATION", []string{"test.>"})

	// Подписка на запросы генерации тестов
	sub, err := client.Subscribe(subjectRequest, func(msg *natsio.Msg) {
		// Извлекаем контекст из заголовков сообщения (распространение трассировки)
		carrier := propagation.HeaderCarrier{}
		// В NATS можно использовать заголовки, но для простоты создаем новый span
		ctx := otel.GetTextMapPropagator().Extract(context.Background(), carrier)

		ctx, span := common.StartSpan(ctx, "test-generator.handle-request")
		defer span.End()

		span.SetAttributes(
			attribute.String("message.id", string(msg.Data)[:min(20, len(msg.Data))]),
			attribute.String("subject", msg.Subject),
		)

		logger.Infof("Received generation request: %s", string(msg.Data))

		var req models.GenerateTestsRequest
		if err := json.Unmarshal(msg.Data, &req); err != nil {
			logger.Errorf("Failed to unmarshal request: %v", err)
			span.RecordError(err)
			return
		}

		// Обработка запроса
		resp := handleGenerateTests(ctx, req, logger)

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
		} else {
			logger.Infof("Response sent to %s", subjectResponse)
			span.AddEvent("response.published", trace.WithAttributes(
				attribute.String("response.subject", subjectResponse),
			))
		}
	})
	if err != nil {
		logger.Fatalf("Failed to subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	logger.Infof("Service %s is listening on subject %s", serviceName, subjectRequest)

	// Ожидание сигнала завершения
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan
	logger.Info("Shutting down...")
}

// Обработчик генерации тестов с трассировкой
func handleGenerateTests(ctx context.Context, req models.GenerateTestsRequest, logger *logrus.Logger) models.TestsGeneratedResponse {
	_, span := common.StartSpan(ctx, "test-generator.generate-tests")
	defer span.End()

	span.SetAttributes(
		attribute.String("code_path", req.CodePath),
		attribute.String("language", req.Language),
		attribute.String("test_framework", req.TestFramework),
	)

	start := time.Now()
	logger.Infof("Generating tests for %s (language: %s)", req.CodePath, req.Language)

	// Имитация генерации тестов
	testFiles := []models.TestFile{
		{
			Path:     req.CodePath + "_test.go",
			Content:  generateExampleTest(req.Language),
			Language: req.Language,
		},
	}

	// Дополнительные файлы, если нужно
	if req.Language == "go" {
		testFiles = append(testFiles, models.TestFile{
			Path:     "integration_test.go",
			Content:  "package main\n\nimport \"testing\"\n\nfunc TestIntegration(t *testing.T) {\n\t// integration test\n}",
			Language: "go",
		})
	}

	duration := time.Since(start)
	span.SetAttributes(
		attribute.Int("test_files.count", len(testFiles)),
		attribute.Int64("duration_ms", duration.Milliseconds()),
	)

	baseMsg := natsclient.CreateBaseMessage(models.MessageTypeTestsGenerated, serviceName, req.Source)
	return models.TestsGeneratedResponse{
		BaseMessage:    baseMsg,
		TestFiles:      testFiles,
		TotalTests:     len(testFiles) * 3,
		GenerationTime: duration,
	}
}

func generateExampleTest(lang string) string {
	switch lang {
	case "go":
		return `package main

import "testing"

func TestAddition(t *testing.T) {
	result := 2 + 2
	expected := 4
	if result != expected {
		t.Errorf("Expected %d, got %d", expected, result)
	}
}

func TestSubtraction(t *testing.T) {
	result := 5 - 3
	expected := 2
	if result != expected {
		t.Errorf("Expected %d, got %d", expected, result)
	}
}

func TestMultiplication(t *testing.T) {
	result := 3 * 4
	expected := 12
	if result != expected {
		t.Errorf("Expected %d, got %d", expected, result)
	}
}`
	case "python":
		return `import unittest

class TestMath(unittest.TestCase):
    def test_addition(self):
        self.assertEqual(2 + 2, 4)
    
    def test_subtraction(self):
        self.assertEqual(5 - 3, 2)
    
    def test_multiplication(self):
        self.assertEqual(3 * 4, 12)

if __name__ == '__main__':
    unittest.main()`
	default:
		return fmt.Sprintf("// Tests for %s language\n// Implement test generation logic", lang)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
