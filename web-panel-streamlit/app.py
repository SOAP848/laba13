"""
Streamlit веб-панель для мониторинга системы автоматизации тестирования.
Интегрируется с Go веб-панелью (порт 8000) для получения данных.
"""

import streamlit as st
import requests
import pandas as pd
import plotly.express as px
import plotly.graph_objects as go
from datetime import datetime
import time

# Конфигурация
GO_PANEL_URL = "http://web-panel:8000"  # Внутри Docker сети
LOCAL_URL = "http://localhost:8000"     # Для локального запуска
USE_DOCKER_NETWORK = True

API_BASE = GO_PANEL_URL if USE_DOCKER_NETWORK else LOCAL_URL

# Настройка страницы Streamlit
st.set_page_config(
    page_title="Test Automation Agents Dashboard",
    page_icon="🤖",
    layout="wide",
    initial_sidebar_state="expanded"
)

# Заголовок
st.title("🤖 Test Automation Agents Dashboard")
st.markdown("Мониторинг системы автоматизации тестирования на основе микросервисов с NATS, OpenTelemetry и LLM")

# Функции для получения данных
@st.cache_data(ttl=10)
def fetch_agents():
    """Получить список агентов"""
    try:
        response = requests.get(f"{API_BASE}/api/agents", timeout=5)
        if response.status_code == 200:
            return response.json()
    except Exception as e:
        st.error(f"Ошибка при получении данных агентов: {e}")
    return []

@st.cache_data(ttl=10)
def fetch_queues():
    """Получить информацию об очередях NATS"""
    try:
        response = requests.get(f"{API_BASE}/api/queues", timeout=5)
        if response.status_code == 200:
            return response.json()
    except Exception as e:
        st.error(f"Ошибка при получении данных очередей: {e}")
    return []

@st.cache_data(ttl=10)
def fetch_tasks():
    """Получить список задач"""
    try:
        response = requests.get(f"{API_BASE}/api/tasks", timeout=5)
        if response.status_code == 200:
            return response.json()
    except Exception as e:
        st.error(f"Ошибка при получении данных задач: {e}")
    return []

@st.cache_data(ttl=30)
def fetch_health():
    """Проверить здоровье сервисов"""
    try:
        response = requests.get(f"{API_BASE}/health", timeout=5)
        if response.status_code == 200:
            return response.json()
    except:
        pass
    return {"status": "unknown"}

# Сайдбар
with st.sidebar:
    st.header("Управление")
    
    # Переключатель для обновления данных
    auto_refresh = st.checkbox("Автообновление", value=True)
    refresh_interval = st.slider("Интервал обновления (сек)", 5, 60, 10)
    
    # Кнопка ручного обновления
    if st.button("🔄 Обновить данные"):
        st.cache_data.clear()
        st.rerun()
    
    # Информация о системе
    st.divider()
    st.subheader("Информация о системе")
    
    health = fetch_health()
    status_color = "🟢" if health.get("status") == "ok" else "🔴"
    st.write(f"{status_color} Веб-панель Go: {health.get('status', 'unknown')}")
    
    # Статистика
    agents = fetch_agents()
    active_agents = sum(1 for a in agents if a.get("status") == "active")
    total_tasks = sum(a.get("processed", 0) for a in agents)
    
    st.metric("Активные агенты", active_agents)
    st.metric("Всего обработано задач", total_tasks)
    
    # Ссылки
    st.divider()
    st.subheader("Ссылки")
    st.markdown("- [Jaeger Tracing](http://localhost:16686)")
    st.markdown("- [NATS Monitoring](http://localhost:8222)")
    st.markdown("- [Go веб-панель](http://localhost:8000)")

# Основное содержимое
tab1, tab2, tab3, tab4 = st.tabs(["📊 Обзор", "🤖 Агенты", "📨 Очереди", "🚀 Задачи"])

with tab1:
    col1, col2 = st.columns(2)
    
    with col1:
        st.subheader("Статус агентов")
        agents = fetch_agents()
        if agents:
            df_agents = pd.DataFrame(agents)
            # Визуализация статусов
            status_counts = df_agents["status"].value_counts()
            fig_status = px.pie(
                values=status_counts.values,
                names=status_counts.index,
                title="Распределение статусов агентов",
                color_discrete_sequence=px.colors.qualitative.Set3
            )
            st.plotly_chart(fig_status, use_container_width=True)
        else:
            st.info("Данные об агентах недоступны")
    
    with col2:
        st.subheader("Очереди NATS")
        queues = fetch_queues()
        if queues:
            df_queues = pd.DataFrame(queues)
            fig_queues = px.bar(
                df_queues,
                x="name",
                y="messages",
                title="Сообщения в очередях",
                color="consumers",
                color_continuous_scale="Viridis"
            )
            st.plotly_chart(fig_queues, use_container_width=True)
        else:
            st.info("Данные об очередях недоступны")
    
    # График активности задач
    st.subheader("Активность задач")
    tasks = fetch_tasks()
    if tasks:
        df_tasks = pd.DataFrame(tasks)
        if "timestamp" in df_tasks.columns:
            df_tasks["timestamp"] = pd.to_datetime(df_tasks["timestamp"])
            df_tasks["hour"] = df_tasks["timestamp"].dt.hour
            hourly_counts = df_tasks.groupby("hour").size().reset_index(name="count")
            fig_timeline = px.line(
                hourly_counts,
                x="hour",
                y="count",
                title="Задачи по часам",
                markers=True
            )
            st.plotly_chart(fig_timeline, use_container_width=True)

with tab2:
    st.subheader("Детальная информация об агентах")
    agents = fetch_agents()
    if agents:
        df_agents = pd.DataFrame(agents)
        
        # Фильтры
        col1, col2 = st.columns(2)
        with col1:
            status_filter = st.multiselect(
                "Фильтр по статусу",
                options=df_agents["status"].unique(),
                default=df_agents["status"].unique()
            )
        with col2:
            min_processed = st.slider(
                "Минимум обработанных задач",
                min_value=0,
                max_value=int(df_agents["processed"].max()) if len(df_agents) > 0 else 100,
                value=0
            )
        
        # Применение фильтров
        filtered_df = df_agents[
            (df_agents["status"].isin(status_filter)) &
            (df_agents["processed"] >= min_processed)
        ]
        
        # Отображение таблицы
        st.dataframe(
            filtered_df,
            use_container_width=True,
            column_config={
                "id": "ID агента",
                "status": st.column_config.TextColumn(
                    "Статус",
                    help="Текущий статус агента"
                ),
                "processed": st.column_config.NumberColumn(
                    "Обработано задач",
                    help="Количество обработанных задач"
                )
            }
        )
        
        # Гистограмма обработанных задач
        fig_hist = px.histogram(
            filtered_df,
            x="id",
            y="processed",
            color="status",
            title="Распределение задач по агентам"
        )
        st.plotly_chart(fig_hist, use_container_width=True)
    else:
        st.warning("Нет данных об агентах")

with tab3:
    st.subheader("Очереди NATS JetStream")
    queues = fetch_queues()
    if queues:
        df_queues = pd.DataFrame(queues)
        
        # Показатели
        col1, col2, col3 = st.columns(3)
        with col1:
            st.metric("Всего очередей", len(df_queues))
        with col2:
            total_messages = df_queues["messages"].sum()
            st.metric("Всего сообщений", total_messages)
        with col3:
            total_consumers = df_queues["consumers"].sum()
            st.metric("Всего потребителей", total_consumers)
        
        # Детальная таблица
        st.dataframe(df_queues, use_container_width=True)
        
        # График потребления
        fig_consumers = px.scatter(
            df_queues,
            x="messages",
            y="consumers",
            size="messages",
            color="name",
            title="Соотношение сообщений и потребителей",
            hover_name="name"
        )
        st.plotly_chart(fig_consumers, use_container_width=True)
    else:
        st.info("Очереди не найдены или JetStream не активирован")

with tab4:
    st.subheader("Управление задачами")
    
    col1, col2 = st.columns([2, 1])
    
    with col1:
        # Список задач
        tasks = fetch_tasks()
        if tasks:
            df_tasks = pd.DataFrame(tasks)
            
            # Конвертация timestamp
            if "timestamp" in df_tasks.columns:
                df_tasks["timestamp"] = pd.to_datetime(df_tasks["timestamp"])
                df_tasks["time_str"] = df_tasks["timestamp"].dt.strftime("%H:%M:%S")
            
            # Отображение
            for _, task in df_tasks.iterrows():
                with st.container():
                    status_icon = {
                        "completed": "✅",
                        "running": "🔄",
                        "pending": "⏳",
                        "failed": "❌"
                    }.get(task.get("status", "pending"), "❓")
                    
                    st.write(f"**{status_icon} {task.get('id', 'N/A')}**")
                    cols = st.columns(4)
                    cols[0].write(f"Тип: {task.get('type', 'N/A')}")
                    cols[1].write(f"Статус: {task.get('status', 'N/A')}")
                    cols[2].write(f"Время: {task.get('time_str', 'N/A')}")
                    
                    with cols[3]:
                        if st.button("Детали", key=task.get("id")):
                            st.json(task.to_dict())
                    st.divider()
        else:
            st.info("Нет активных задач")
    
    with col2:
        # Создание новой задачи
        st.subheader("Создать задачу")
        
        task_type = st.selectbox(
            "Тип задачи",
            ["generate_tests", "run_tests", "analyze_coverage", "llm_analysis"]
        )
        
        payload = st.text_area(
            "Полезная нагрузка",
            value="example/main.go",
            help="Путь к файлу или код для анализа"
        )
        
        if st.button("🚀 Отправить задачу", type="primary"):
            with st.spinner("Отправка задачи..."):
                try:
                    response = requests.post(
                        f"{API_BASE}/api/start-task",
                        json={
                            "task_type": task_type,
                            "payload": payload
                        },
                        timeout=10
                    )
                    
                    if response.status_code == 200:
                        st.success("Задача успешно отправлена!")
                        st.balloons()
                        # Очистка кэша для обновления данных
                        st.cache_data.clear()
                    else:
                        st.error(f"Ошибка: {response.status_code} - {response.text}")
                except Exception as e:
                    st.error(f"Ошибка при отправке: {e}")

# Нижний колонтитул
st.divider()
footer_col1, footer_col2, footer_col3 = st.columns(3)
with footer_col1:
    st.caption(f"Последнее обновление: {datetime.now().strftime('%H:%M:%S')}")
with footer_col2:
    st.caption(f"Всего агентов: {len(agents)}")
with footer_col3:
    if auto_refresh:
        time.sleep(refresh_interval)
        st.rerun()
        st.caption(f"Автообновление каждые {refresh_interval} сек")

# Отображение сырых данных для отладки
with st.expander("📋 Отладочная информация"):
    st.subheader("Сырые данные API")
    col1, col2 = st.columns(2)
    with col1:
        st.json(agents)
    with col2:
        st.json(queues)