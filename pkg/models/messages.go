package models

import "time"

// Типы сообщений для обмена между агентами
const (
	MessageTypeGenerateTests    = "generate_tests"
	MessageTypeTestsGenerated   = "tests_generated"
	MessageTypeRunTests         = "run_tests"
	MessageTypeTestsCompleted   = "tests_completed"
	MessageTypeAnalyzeCoverage  = "analyze_coverage"
	MessageTypeCoverageAnalyzed = "coverage_analyzed"
)

// Базовое сообщение
type BaseMessage struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"`
	Target    string    `json:"target"`
}

// Запрос на генерацию тестов
type GenerateTestsRequest struct {
	BaseMessage
	CodePath      string                 `json:"code_path"`
	Language      string                 `json:"language"` // go, python, javascript
	TestFramework string                 `json:"test_framework"`
	Options       map[string]interface{} `json:"options"`
}

// Ответ с сгенерированными тестами
type TestsGeneratedResponse struct {
	BaseMessage
	TestFiles      []TestFile    `json:"test_files"`
	TotalTests     int           `json:"total_tests"`
	GenerationTime time.Duration `json:"generation_time"`
}

// Файл теста
type TestFile struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Language string `json:"language"`
}

// Запрос на запуск тестов
type RunTestsRequest struct {
	BaseMessage
	TestFiles   []TestFile `json:"test_files"`
	Environment string     `json:"environment"` // local, docker, ci
	Parallel    bool       `json:"parallel"`
	Timeout     int        `json:"timeout"` // seconds
}

// Результат выполнения тестов
type TestsCompletedResponse struct {
	BaseMessage
	Passed        int           `json:"passed"`
	Failed        int           `json:"failed"`
	Skipped       int           `json:"skipped"`
	Duration      time.Duration `json:"duration"`
	Logs          string        `json:"logs"`
	FailedDetails []TestFailure `json:"failed_details,omitempty"`
}

// Информация о неудачном тесте
type TestFailure struct {
	TestName   string `json:"test_name"`
	File       string `json:"file"`
	Error      string `json:"error"`
	StackTrace string `json:"stack_trace,omitempty"`
}

// Запрос на анализ покрытия
type AnalyzeCoverageRequest struct {
	BaseMessage
	TestResults  TestsCompletedResponse `json:"test_results"`
	CoverageData string                 `json:"coverage_data"` // путь к файлу coverage.out
	Threshold    float64                `json:"threshold"`     // минимальный процент покрытия
}

// Результат анализа покрытия
type CoverageAnalyzedResponse struct {
	BaseMessage
	CoveragePercent float64        `json:"coverage_percent"`
	LinesCovered    int            `json:"lines_covered"`
	LinesTotal      int            `json:"lines_total"`
	Files           []FileCoverage `json:"files"`
	MeetsThreshold  bool           `json:"meets_threshold"`
	Recommendations []string       `json:"recommendations,omitempty"`
}

// Покрытие по файлу
type FileCoverage struct {
	FilePath        string  `json:"file_path"`
	CoveragePercent float64 `json:"coverage_percent"`
	LinesCovered    int     `json:"lines_covered"`
	LinesTotal      int     `json:"lines_total"`
}
