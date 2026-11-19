package platform

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Tracing follows one text through every service. HTTP carries the trace in the traceparent header, events carry it
// in the envelope, because the outbox stores only the event. Spans hold ids and timings, never the text.

// InitTracing sends spans to OTEL_EXPORTER_OTLP_ENDPOINT; without that variable tracing is off.
// A tracing problem must never stop a service. The returned func flushes the last spans.
func InitTracing(ctx context.Context, service string) func() {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return func() {}
	}
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		slog.Warn("tracing is off", "error", err)
		return func() {}
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", service))),
	)
	otel.SetTracerProvider(tp)
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		tp.Shutdown(ctx)
	}
}

func tracer() trace.Tracer { return otel.Tracer("braillebar") }

func traceparent(ctx context.Context) string {
	c := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, c)
	return c["traceparent"]
}

// startConsumeSpan continues the trace of the event; event.age_ms shows how long it waited in the outbox and queue.
func startConsumeSpan(ctx context.Context, e Envelope) (context.Context, trace.Span) {
	ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier{"traceparent": e.Traceparent})
	return tracer().Start(ctx, "consume "+e.Type, trace.WithSpanKind(trace.SpanKindConsumer), trace.WithAttributes(
		attribute.String("event.id", e.ID),
		attribute.Int64("event.age_ms", time.Since(e.OccurredAt).Milliseconds()),
	))
}

func endSpan(span trace.Span, err error) {
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

// TraceHTTP starts a span per request, continuing the caller's trace and passing it on to anything proxied.
func TraceHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || r.URL.Path == "/status" { // health checks are noise, a socket lasts for hours
			next.ServeHTTP(w, r)
			return
		}
		carrier := propagation.HeaderCarrier(r.Header)
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), carrier)
		ctx, span := tracer().Start(ctx, r.Method+" "+r.URL.Path, trace.WithSpanKind(trace.SpanKindServer))
		defer span.End()
		otel.GetTextMapPropagator().Inject(ctx, carrier)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
