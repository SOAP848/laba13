FROM python:3.11-slim

WORKDIR /app

# Установка системных зависимостей
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    && rm -rf /var/lib/apt/lists/*

# Копирование requirements и установка Python пакетов
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Копирование исходного кода
COPY . .

# Переменные окружения
ENV PYTHONUNBUFFERED=1
ENV NATS_URL=nats://nats:4222
ENV GROQ_API_KEY=""
ENV LLM_MODEL=llama3-8b-8192

# Запуск агента
CMD ["python", "main.py"]