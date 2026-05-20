package main

import (
	"context"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"time"

	"test-automation-agents/internal/common"
	"test-automation-agents/internal/natsclient"
	"test-automation-agents/pkg/models"

	natsio "github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
)

const (
	serviceName      = "web-panel"
	natsURL          = "nats://localhost:4222"
	otelCollectorURL = "otel-collector:4317"
	httpPort         = "8000"
)

type WebPanel struct {
	natsClient *natsio.Conn
	logger     *logrus.Logger
	templates  *template.Template
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
			ctx, span := common.StartSpan(context.Background(), "shutdown")
			defer span.End()
			if err := shutdown(ctx); err != nil {
				logger.Errorf("Failed to shutdown tracer: %v", err)
			}
		}()
	}

	// Подключение к NATS
	nc, err := natsio.Connect(natsURL)
	if err != nil {
		logger.Fatalf("Failed to connect to NATS: %v", err)
	}
	defer nc.Close()

	// Загрузка HTML шаблонов
	tmpl := template.Must(template.ParseGlob("templates/*.html"))
	if tmpl == nil {
		// Создаем минимальный шаблон в памяти, если файлов нет
		tmpl = template.Must(template.New("index").Parse(defaultHTML))
	}

	panel := &WebPanel{
		natsClient: nc,
		logger:     logger,
		templates:  tmpl,
	}

	// Настройка маршрутов HTTP
	http.HandleFunc("/", panel.handleIndex)
	http.HandleFunc("/api/agents", panel.handleAgents)
	http.HandleFunc("/api/queues", panel.handleQueues)
	http.HandleFunc("/api/tasks", panel.handleTasks)
	http.HandleFunc("/api/start-task", panel.handleStartTask)
	http.HandleFunc("/health", panel.handleHealth)

	// Статические файлы (если есть)
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	logger.Infof("Web panel listening on http://localhost:%s", httpPort)
	if err := http.ListenAndServe(":"+httpPort, nil); err != nil {
		logger.Fatal(err)
	}
}

func (w *WebPanel) handleIndex(wr http.ResponseWriter, r *http.Request) {
	data := map[string]interface{}{
		"Title":       "Test Automation Agents Dashboard",
		"CurrentTime": time.Now().Format("2006-01-02 15:04:05"),
		"Services":    []string{"test-generator", "test-runner", "coverage-analyzer", "stateful-agent", "auctioneer", "scaler"},
	}
	w.templates.ExecuteTemplate(wr, "index.html", data)
}

func (w *WebPanel) handleAgents(wr http.ResponseWriter, r *http.Request) {
	// Заглушка: возвращаем список агентов
	agents := []map[string]interface{}{
		{"id": "test-generator", "status": "active", "processed": 42},
		{"id": "test-runner", "status": "active", "processed": 38},
		{"id": "coverage-analyzer", "status": "active", "processed": 15},
		{"id": "stateful-agent", "status": "active", "processed": 7},
		{"id": "auctioneer", "status": "idle", "processed": 3},
		{"id": "scaler", "status": "monitoring", "processed": 0},
	}
	wr.Header().Set("Content-Type", "application/json")
	json.NewEncoder(wr).Encode(agents)
}

func (w *WebPanel) handleQueues(wr http.ResponseWriter, r *http.Request) {
	// Получаем информацию об очередях NATS JetStream
	js, err := w.natsClient.JetStream()
	if err != nil {
		http.Error(wr, "JetStream not available", http.StatusInternalServerError)
		return
	}

	streams := []string{"TEST_AUTOMATION", "AUCTION"}
	queueInfo := []map[string]interface{}{}
	for _, streamName := range streams {
		info, err := js.StreamInfo(streamName)
		if err != nil {
			continue
		}
		queueInfo = append(queueInfo, map[string]interface{}{
			"name":      streamName,
			"messages":  info.State.Msgs,
			"consumers": info.State.Consumers,
		})
	}

	wr.Header().Set("Content-Type", "application/json")
	json.NewEncoder(wr).Encode(queueInfo)
}

func (w *WebPanel) handleTasks(wr http.ResponseWriter, r *http.Request) {
	// Заглушка: возвращаем последние задачи
	tasks := []map[string]interface{}{
		{"id": "task_001", "type": "generate_tests", "status": "completed", "timestamp": time.Now().Add(-5 * time.Minute)},
		{"id": "task_002", "type": "run_tests", "status": "running", "timestamp": time.Now().Add(-2 * time.Minute)},
		{"id": "task_003", "type": "analyze_coverage", "status": "pending", "timestamp": time.Now().Add(-1 * time.Minute)},
	}
	wr.Header().Set("Content-Type", "application/json")
	json.NewEncoder(wr).Encode(tasks)
}

func (w *WebPanel) handleStartTask(wr http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(wr, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(wr, "Bad request", http.StatusBadRequest)
		return
	}

	var req struct {
		TaskType string `json:"task_type"`
		Payload  string `json:"payload"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(wr, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Отправляем задачу в NATS
	msg := models.GenerateTestsRequest{
		BaseMessage: natsclient.CreateBaseMessage(
			models.MessageTypeGenerateTests,
			"web-panel",
			"test-generator",
		),
		CodePath:      req.Payload,
		Language:      "go",
		TestFramework: "testing",
	}

	data, _ := json.Marshal(msg)
	if err := w.natsClient.Publish("test.generate", data); err != nil {
		http.Error(wr, "Failed to publish task", http.StatusInternalServerError)
		return
	}

	wr.Header().Set("Content-Type", "application/json")
	json.NewEncoder(wr).Encode(map[string]string{"status": "task submitted"})
}

func (w *WebPanel) handleHealth(wr http.ResponseWriter, r *http.Request) {
	wr.Header().Set("Content-Type", "application/json")
	json.NewEncoder(wr).Encode(map[string]string{"status": "ok", "service": serviceName})
}

// Минимальный HTML шаблон по умолчанию
const defaultHTML = `
<!DOCTYPE html>
<html>
<head>
    <title>{{.Title}}</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; }
        h1 { color: #333; }
        .card { border: 1px solid #ccc; padding: 20px; margin: 10px 0; border-radius: 8px; }
        .grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 20px; }
        button { background: #4CAF50; color: white; border: none; padding: 10px 20px; cursor: pointer; }
    </style>
</head>
<body>
    <h1>{{.Title}}</h1>
    <p>Current time: {{.CurrentTime}}</p>
    <div class="grid">
        <div class="card">
            <h3>Agents</h3>
            <div id="agents">Loading...</div>
        </div>
        <div class="card">
            <h3>Queues</h3>
            <div id="queues">Loading...</div>
        </div>
        <div class="card">
            <h3>Tasks</h3>
            <div id="tasks">Loading...</div>
        </div>
    </div>
    <div class="card">
        <h3>Start New Task</h3>
        <input type="text" id="taskPayload" placeholder="Code path">
        <button onclick="startTask()">Generate Tests</button>
    </div>
    <script>
        async function loadData() {
            const agents = await fetch('/api/agents').then(r => r.json());
            document.getElementById('agents').innerHTML = agents.map(a =>
                '<div><strong>' + a.id + '</strong>: ' + a.status + ' (' + a.processed + ')</div>'
            ).join('');
            const queues = await fetch('/api/queues').then(r => r.json());
            document.getElementById('queues').innerHTML = queues.map(q =>
                '<div>' + q.name + ': ' + q.messages + ' messages</div>'
            ).join('');
            const tasks = await fetch('/api/tasks').then(r => r.json());
            document.getElementById('tasks').innerHTML = tasks.map(t =>
                '<div>' + t.id + ': ' + t.type + ' - ' + t.status + '</div>'
            ).join('');
        }
        async function startTask() {
            const payload = document.getElementById('taskPayload').value;
            const res = await fetch('/api/start-task', {
                method: 'POST',
                headers: {'Content-Type': 'application/json'},
                body: JSON.stringify({task_type: 'generate_tests', payload})
            });
            alert('Task submitted');
        }
        setInterval(loadData, 5000);
        loadData();
    </script>
</body>
</html>
`
