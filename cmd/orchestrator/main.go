package main

import (
	"encoding/json"
	"log"
	"time"

	"test-automation-agents/internal/natsclient"
	"test-automation-agents/pkg/models"
)

func main() {
	log.Println("Starting orchestrator...")

	client, err := natsclient.NewClient("nats://localhost:4222")
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	// Ждем немного, чтобы сервисы запустились
	time.Sleep(2 * time.Second)

	// 1. Запрос на генерацию тестов
	genReq := models.GenerateTestsRequest{
		BaseMessage: natsclient.CreateBaseMessage(
			models.MessageTypeGenerateTests,
			"orchestrator",
			"test-generator",
		),
		CodePath:      "./example/main.go",
		Language:      "go",
		TestFramework: "testing",
		Options:       map[string]interface{}{"count": 5},
	}

	log.Println("Sending generate request...")
	msg, err := client.Request("test.generate", genReq, 10*time.Second)
	if err != nil {
		log.Printf("Request failed: %v", err)
		return
	}

	var genResp models.TestsGeneratedResponse
	if err := json.Unmarshal(msg.Data, &genResp); err != nil {
		log.Printf("Failed to unmarshal response: %v", err)
		return
	}
	log.Printf("Tests generated: %d files, %d total tests", len(genResp.TestFiles), genResp.TotalTests)

	// 2. Запрос на запуск тестов
	runReq := models.RunTestsRequest{
		BaseMessage: natsclient.CreateBaseMessage(
			models.MessageTypeRunTests,
			"orchestrator",
			"test-runner",
		),
		TestFiles:   genResp.TestFiles,
		Environment: "local",
		Parallel:    true,
		Timeout:     30,
	}

	log.Println("Sending run request...")
	msg, err = client.Request("test.run", runReq, 30*time.Second)
	if err != nil {
		log.Printf("Request failed: %v", err)
		return
	}

	var runResp models.TestsCompletedResponse
	if err := json.Unmarshal(msg.Data, &runResp); err != nil {
		log.Printf("Failed to unmarshal response: %v", err)
		return
	}
	log.Printf("Tests completed: %d passed, %d failed, %d skipped", runResp.Passed, runResp.Failed, runResp.Skipped)

	// 3. Запрос на анализ покрытия
	covReq := models.AnalyzeCoverageRequest{
		BaseMessage: natsclient.CreateBaseMessage(
			models.MessageTypeAnalyzeCoverage,
			"orchestrator",
			"coverage-analyzer",
		),
		TestResults:  runResp,
		CoverageData: "coverage.out",
		Threshold:    80.0,
	}

	log.Println("Sending coverage analysis request...")
	msg, err = client.Request("coverage.analyze", covReq, 10*time.Second)
	if err != nil {
		log.Printf("Request failed: %v", err)
		return
	}

	var covResp models.CoverageAnalyzedResponse
	if err := json.Unmarshal(msg.Data, &covResp); err != nil {
		log.Printf("Failed to unmarshal response: %v", err)
		return
	}
	log.Printf("Coverage analysis: %.2f%%, meets threshold: %v", covResp.CoveragePercent, covResp.MeetsThreshold)
	for _, rec := range covResp.Recommendations {
		log.Printf("Recommendation: %s", rec)
	}

	log.Println("Orchestration completed successfully!")
}
