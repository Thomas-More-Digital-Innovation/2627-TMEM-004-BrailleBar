package platform

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestTraceFollowsHTTPAndEvents(t *testing.T) {
	otel.SetTracerProvider(sdktrace.NewTracerProvider())
	otel.SetTextMapPropagator(propagation.TraceContext{})

	// a request through two services, the way the gateway proxies to a service
	var inService trace.SpanContext
	service := TraceHTTP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		inService = trace.SpanContextFromContext(r.Context())
	}))
	var atGateway trace.SpanContext
	gateway := TraceHTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atGateway = trace.SpanContextFromContext(r.Context())
		service.ServeHTTP(w, r)
	}))
	gateway.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/documents/text", nil))
	if !atGateway.IsValid() || inService.TraceID() != atGateway.TraceID() || inService.SpanID() == atGateway.SpanID() {
		t.Fatalf("service did not continue the gateway's trace: %v %v", atGateway, inService)
	}

	// an event made in that trace continues it at the consumer
	ctx, span := tracer().Start(context.Background(), "publish")
	defer span.End()
	e := Envelope{ID: "e1", Type: "x", Traceparent: traceparent(ctx)}
	consumed, consumeSpan := startConsumeSpan(context.Background(), e)
	defer consumeSpan.End()
	if trace.SpanContextFromContext(consumed).TraceID() != span.SpanContext().TraceID() {
		t.Fatal("consumer did not continue the event's trace")
	}
}
