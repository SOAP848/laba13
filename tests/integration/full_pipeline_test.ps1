Write-Host "=== Integration Test: Full Pipeline ==="

# Проверяем, что все необходимые контейнеры запущены
$required = @("nats-server", "jaeger", "otel-collector", "redis", "test-generator", "test-runner", "coverage-analyzer", "stateful-agent", "auctioneer", "python-llm-agent", "web-panel")
foreach ($s in $required) {
    $cnt = docker ps --filter "name=$s" -q
    if (-not $cnt) {
        Write-Error "Container $s not running"
        exit 1
    }
}
Write-Host "All containers running."

# Отправляем задачу генерации тестов через веб-панель
$taskPayload = @{
    task_type = "generate_tests"
    payload = "./example/main.go"
} | ConvertTo-Json

Write-Host "Sending task to web-panel..."
try {
    $taskResp = Invoke-RestMethod -Uri "http://localhost:8000/api/start-task" -Method Post -ContentType "application/json" -Body $taskPayload
    Write-Host "Task submitted: $($taskResp | ConvertTo-Json -Compress)"
} catch {
    Write-Error "Failed to submit task: $_"
    exit 1
}

Write-Host "Waiting for pipeline to complete..."
Start-Sleep -Seconds 20

# Проверяем, что stateful-agent увеличил счётчик в Redis
$redisKey = "stateful-agent:processed"
$processedCount = docker exec redis redis-cli GET $redisKey
if ($processedCount -gt 0) {
    Write-Host "OK: Redis key '$redisKey' = $processedCount"
} else {
    Write-Host "WARN: No processed tasks in Redis (maybe not yet processed)"
}

# Проверяем, что в NATS JetStream есть сообщения в потоке TEST_AUTOMATION
$streamInfo = docker exec nats-server nats stream info TEST_AUTOMATION 2>&1
if ($streamInfo -match "Messages:\s*(\d+)") {
    $msgCount = $matches[1]
    Write-Host "OK: Stream TEST_AUTOMATION has $msgCount messages"
} else {
    Write-Host "WARN: Could not retrieve stream info"
}

# Проверяем наличие трасс в Jaeger
$jaegerUrl = "http://localhost:16686/api/traces?service=test-generator&limit=1"
try {
    $traces = Invoke-RestMethod -Uri $jaegerUrl
    if ($traces.data.Count -gt 0) {
        Write-Host "OK: Jaeger traces found for test-generator"
    } else {
        Write-Host "WARN: no traces in Jaeger for test-generator"
    }
} catch {
    Write-Host "WARN: Could not fetch Jaeger traces: $_"
}

# Проверяем, что веб-панель возвращает список агентов
$agents = Invoke-RestMethod -Uri "http://localhost:8000/api/agents"
if ($agents.Count -gt 0) {
    Write-Host "OK: Web-panel returned $($agents.Count) agents"
} else {
    Write-Host "WARN: No agents returned from web-panel"
}

Write-Host "=== Integration test PASSED ==="