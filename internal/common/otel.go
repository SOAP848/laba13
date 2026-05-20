package common

import (
	"context"
	"fmt"
	"log"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	Tracer trace.Tracer
)

// InitTracer инициализирует OpenTelemetry tracer и возвращает функцию для завершения.
func InitTracer(serviceName, collectorEndpoint string) (func(context.Context) error, error) {
	ctx := context.Background()

	// Создаем ресурс с атрибутами сервиса
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion("1.0.0"),
			semconv.DeploymentEnvironment("development"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Подключаемся к коллектору (Jaeger)
	conn, err := grpc.NewClient(collectorEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithTimeout(5*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection: %w", err)
	}

	// Создаем экспортер OTLP gRPC
	traceExporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithGRPCConn(conn),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	// Создаем провайдер трассировок
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	// Устанавливаем глобальный провайдер
	otel.SetTracerProvider(tracerProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	Tracer = tracerProvider.Tracer(serviceName)

	log.Printf("OpenTelemetry tracer initialized for service %s", serviceName)

	// Функция для завершения
	shutdown := func(ctx context.Context) error {
		return tracerProvider.Shutdown(ctx)
	}
	return shutdown, nil
}

// StartSpan создает новый span с указанным именем.
func StartSpan(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if Tracer == nil {
		// Если tracer не инициализирован, возвращаем noop span
		return ctx, trace.SpanFromContext(ctx)
	}
	return Tracer.Start(ctx, spanName, opts...)
}
