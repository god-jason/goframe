package trace

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// HeaderTraceID 是响应中回传的链路 ID，便于日志与前端关联。
const HeaderTraceID = "X-Trace-ID"

// Middleware 为每个 HTTP 请求创建服务端 Span，并解析上游 traceparent。
func Middleware() gin.HandlerFunc {
	tracer := Tracer()
	propagator := otel.GetTextMapPropagator()

	return func(c *gin.Context) {
		ctx := propagator.Extract(c.Request.Context(), propagation.HeaderCarrier(c.Request.Header))
		ctx, span := tracer.Start(ctx, c.Request.Method,
			oteltrace.WithSpanKind(oteltrace.SpanKindServer),
			oteltrace.WithAttributes(
				attribute.String("http.request.method", c.Request.Method),
				attribute.String("url.path", c.Request.URL.Path),
				attribute.String("url.scheme", scheme(c.Request)),
				attribute.String("server.address", c.Request.Host),
				attribute.String("user_agent.original", c.Request.UserAgent()),
				attribute.String("client.address", c.ClientIP()),
			),
		)
		defer span.End()

		c.Request = c.Request.WithContext(ctx)
		if traceID := TraceID(ctx); traceID != "" {
			// 响应头必须在写响应前设置。
			c.Header(HeaderTraceID, traceID)
		}

		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		span.SetName(c.Request.Method + " " + route)
		span.SetAttributes(
			attribute.String("http.route", route),
			attribute.Int("http.response.status_code", c.Writer.Status()),
		)
		if c.Writer.Status() >= http.StatusInternalServerError {
			span.SetStatus(codes.Error, strconv.Itoa(c.Writer.Status()))
		}
	}
}

// TraceID 返回 context 中的链路 ID，没有时返回空字符串。
func TraceID(ctx context.Context) string {
	spanContext := oteltrace.SpanContextFromContext(ctx)
	if !spanContext.HasTraceID() {
		return ""
	}
	return spanContext.TraceID().String()
}

// HTTPTransport 包装 http.RoundTripper，为出站请求创建 Span 并注入追踪头。
func HTTPTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &transport{base: base}
}

type transport struct {
	base http.RoundTripper
}

func (t *transport) RoundTrip(request *http.Request) (*http.Response, error) {
	ctx := request.Context()
	ctx, span := Tracer().Start(ctx, request.Method+" "+request.URL.Path,
		oteltrace.WithSpanKind(oteltrace.SpanKindClient),
		oteltrace.WithAttributes(
			attribute.String("http.request.method", request.Method),
			attribute.String("url.full", request.URL.String()),
			attribute.String("server.address", request.URL.Host),
		),
	)
	defer span.End()

	clone := request.Clone(ctx)
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(clone.Header))

	response, err := t.base.RoundTrip(clone)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	span.SetAttributes(attribute.Int("http.response.status_code", response.StatusCode))
	if response.StatusCode >= http.StatusInternalServerError {
		span.SetStatus(codes.Error, response.Status)
	}
	return response, nil
}

func scheme(request *http.Request) string {
	if request.TLS != nil {
		return "https"
	}
	if value := request.Header.Get("X-Forwarded-Proto"); value != "" {
		return value
	}
	return "http"
}
