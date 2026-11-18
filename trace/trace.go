package trace

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/logger"
)

var (
	cfg    = config.Register("trace", DefaultConfig())
	global = struct {
		provider *sdktrace.TracerProvider
	}{}

	// 以下变量仅在包内测试中替换，用于覆盖配置读取与导出器构造的失败分支。
	currentConfig   = func() *Config { return cfg.Get() }
	newGRPCExporter = func(ctx context.Context, opts ...otlptracegrpc.Option) (sdktrace.SpanExporter, error) {
		return otlptracegrpc.New(ctx, opts...)
	}
	newHTTPExporter = func(ctx context.Context, opts ...otlptracehttp.Option) (sdktrace.SpanExporter, error) {
		return otlptracehttp.New(ctx, opts...)
	}
)

func init() {
	_ = boot.Register("trace", &boot.Module{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config", "logger"},
	})
}

// GetConfig 返回当前追踪配置快照。
func GetConfig() *Config { return currentConfig() }

// Get 返回全局 TracerProvider；未启用时返回 nil。
func Get() *sdktrace.TracerProvider {
	return global.provider
}

// Startup 初始化 OTLP 导出器与全局 TracerProvider。
func Startup(ctx context.Context) error {
	current := GetConfig()
	if !current.Enabled {
		return nil
	}
	if err := current.Validate(); err != nil {
		return err
	}

	exporter, err := newExporter(ctx, *current)
	if err != nil {
		return err
	}

	serviceName := current.ServiceName
	if serviceName == "" {
		serviceName = config.App().Name
	}
	res := resource.NewSchemaless(
		attribute.String("service.name", serviceName),
		attribute.String("service.version", config.App().Version),
		attribute.String("deployment.environment", config.App().Environment),
	)

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(current.BatchTimeout)),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(current.SampleRatio))),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	global.provider = provider

	logger.Get().Info("trace enabled",
		logger.String("service", serviceName),
		logger.String("endpoint", current.Endpoint),
		logger.String("protocol", current.Protocol),
		logger.Float64("sample_ratio", current.SampleRatio),
	)
	return nil
}

// Shutdown 刷新并关闭 TracerProvider。
func Shutdown(ctx context.Context) error {
	provider := global.provider
	global.provider = nil

	if provider == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, GetConfig().Timeout)
		defer cancel()
	}
	if err := provider.Shutdown(ctx); err != nil {
		return fmt.Errorf("trace: shutdown: %w", err)
	}
	otel.SetTracerProvider(noop.NewTracerProvider())
	return nil
}

// Validate 校验追踪配置。
func (c Config) Validate() error {
	switch c.Protocol {
	case "", "http", "grpc":
	default:
		return fmt.Errorf("trace: protocol must be http or grpc")
	}
	if c.SampleRatio < 0 || c.SampleRatio > 1 {
		return fmt.Errorf("trace: sample_ratio must be between 0 and 1")
	}
	if c.Endpoint == "" {
		return fmt.Errorf("trace: endpoint is required")
	}
	return nil
}

// newExporter 根据协议创建 OTLP 导出器。
func newExporter(ctx context.Context, c Config) (sdktrace.SpanExporter, error) {
	if c.Protocol == "grpc" {
		options := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(c.Endpoint),
			otlptracegrpc.WithTimeout(c.Timeout),
		}
		if c.Insecure {
			options = append(options, otlptracegrpc.WithInsecure())
		}
		if len(c.Headers) > 0 {
			options = append(options, otlptracegrpc.WithHeaders(c.Headers))
		}
		exporter, err := newGRPCExporter(ctx, options...)
		if err != nil {
			return nil, fmt.Errorf("trace: create grpc exporter: %w", err)
		}
		return exporter, nil
	}

	options := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(c.Endpoint),
		otlptracehttp.WithTimeout(c.Timeout),
	}
	if c.Insecure {
		options = append(options, otlptracehttp.WithInsecure())
	}
	if len(c.Headers) > 0 {
		options = append(options, otlptracehttp.WithHeaders(c.Headers))
	}
	exporter, err := newHTTPExporter(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("trace: create http exporter: %w", err)
	}
	return exporter, nil
}

// Tracer 返回框架 Tracer，未启用时返回 no-op Tracer。
func Tracer() oteltrace.Tracer {
	return otel.Tracer("github.com/god-jason/goframe")
}
