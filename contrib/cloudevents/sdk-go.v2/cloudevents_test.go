// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cloudevents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/cloudevents/sdk-go/v2/binding"
	"github.com/cloudevents/sdk-go/v2/client"
	"github.com/cloudevents/sdk-go/v2/protocol"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

type fakeSender struct {
	message binding.Message
	result  error
}

func (s *fakeSender) Send(_ context.Context, message binding.Message, _ ...binding.Transformer) error {
	s.message = message
	return s.result
}

func testEvent() cloudevents.Event {
	e := cloudevents.NewEvent()
	e.SetID("event-id")
	e.SetType("com.example.created")
	e.SetSource("https://example.test/source")
	e.SetSubject("orders/123")
	_ = e.SetData("application/json", map[string]string{"secret": "not-a-tag"})
	return e
}

func startMockTracer(t *testing.T) mocktracer.Tracer {
	t.Helper()
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	return mt
}

func requireSpan(t *testing.T, mt mocktracer.Tracer) *mocktracer.Span {
	t.Helper()
	spans := mt.FinishedSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	return spans[0]
}

func assertSpanTags(t *testing.T, span *mocktracer.Span, wantTags map[string]any) {
	t.Helper()
	for key, want := range wantTags {
		if got := span.Tag(key); got != want {
			t.Errorf("tag %q = %#v, want %#v", key, got, want)
		}
	}
}

func TestCarriers(t *testing.T) {
	t.Run("write propagation extensions", func(t *testing.T) {
		e := testEvent()
		carrier := eventCarrier{writer: &e}
		values := map[string]string{
			"traceparent": "parent",
			"tracestate":  "state",
			"baggage":     "bag",
			"unsupported": "drop",
		}
		for key, value := range values {
			carrier.Set(key, value)
		}
		for _, key := range propagationKeys {
			if got := e.Extensions()[key]; got == nil {
				t.Errorf("extension %q was not written", key)
			}
		}
		if got := e.Extensions()["unsupported"]; got != nil {
			t.Errorf("unsupported extension was written: %v", got)
		}
	})

	t.Run("read propagation extensions", func(t *testing.T) {
		e := testEvent()
		e.SetExtension("traceparent", "parent")
		e.SetExtension("tracestate", "state")
		e.SetExtension("baggage", 42)
		e.SetExtension("other", "ignored")
		carrier := messageCarrier{reader: (*binding.EventMessage)(&e)}

		var keys []string
		wantErr := errors.New("stop")
		err := carrier.ForeachKey(func(key, value string) error {
			keys = append(keys, key+"="+value)
			if key == "tracestate" {
				return wantErr
			}
			return nil
		})
		if !errors.Is(err, wantErr) {
			t.Fatalf("ForeachKey error = %v, want %v", err, wantErr)
		}
		if got, want := strings.Join(keys, ","), "traceparent=parent,tracestate=state"; got != want {
			t.Fatalf("visited keys = %q, want %q", got, want)
		}

		keys = nil
		if err := carrier.ForeachKey(func(key, value string) error {
			keys = append(keys, key+"="+value)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if got, want := strings.Join(keys, ","), "traceparent=parent,tracestate=state"; got != want {
			t.Fatalf("non-string or unsupported values were visited: got %q, want %q", got, want)
		}
	})
}

func TestSend(t *testing.T) {
	results := []struct {
		name    string
		result  error
		isError bool
	}{
		{name: "nil", result: nil, isError: false},
		{name: "ack", result: protocol.ResultACK, isError: false},
		{name: "wrapped ACK", result: fmt.Errorf("wrapped: %w", protocol.ResultACK), isError: false},
		{name: "nack", result: protocol.ResultNACK, isError: true},
		{name: "transport error", result: errors.New("transport failed"), isError: true},
	}
	for _, tc := range results {
		t.Run(tc.name, func(t *testing.T) {
			mt := startMockTracer(t)
			sender := &fakeSender{result: tc.result}
			ce, err := client.New(sender, client.WithObservabilityService(New()))
			if err != nil {
				t.Fatal(err)
			}
			caller, ctx := tracer.StartSpanFromContext(context.Background(), "caller")
			result := ce.Send(ctx, testEvent())
			caller.Finish()
			if result != tc.result {
				t.Fatalf("Send result = %v, want %v", result, tc.result)
			}
			spans := mt.FinishedSpans()
			if len(spans) != 2 {
				t.Fatalf("got %d spans, want 2", len(spans))
			}
			send, parent := spans[0], spans[1]
			if send.ParentID() != parent.SpanID() || send.TraceID() != parent.TraceID() {
				t.Fatalf("send span is not a child of caller: send=%s caller=%s", send, parent)
			}
			metadata := sender.message.(binding.MessageMetadataReader)
			traceparent, _ := metadata.GetExtension("traceparent").(string)
			if !strings.Contains(traceparent, fmt.Sprintf("-%016x-", send.SpanID())) {
				t.Errorf("traceparent %q does not identify send span %016x", traceparent, send.SpanID())
			}
			wantTags := map[string]any{
				ext.SpanType:                  ext.SpanTypeMessageProducer,
				ext.SpanKind:                  ext.SpanKindProducer,
				ext.Component:                 "cloudevents/sdk-go.v2",
				ext.MessagingOperationName:    "send",
				ext.ResourceName:              "com.example.created",
				"cloudevents.id":              "event-id",
				"cloudevents.type":            "com.example.created",
				"cloudevents.source":          "https://example.test/source",
				"cloudevents.specversion":     "1.0",
				"cloudevents.datacontenttype": "application/json",
			}
			assertSpanTags(t, send, wantTags)
			if send.Tag("cloudevents.subject") != nil || send.Tag("cloudevents.data") != nil {
				t.Errorf("opt-in or payload tag unexpectedly present: %#v", send.Tags())
			}
			if got := send.Tag(ext.ErrorMsg); (got != nil) != tc.isError {
				t.Errorf("error tag = %#v, want error=%v", got, tc.isError)
			}
		})
	}
}

func TestRequestCallbackResults(t *testing.T) {
	response := testEvent()
	tests := []struct {
		name      string
		result    error
		response  *cloudevents.Event
		isError   bool
		errorText string
	}{
		{name: "ACK response", result: protocol.ResultACK, response: &response},
		{name: "NACK", result: protocol.ResultNACK, isError: true},
		{name: "transport error", result: errors.New("request failed"), isError: true},
		// An ACK promises delivery, so a nil converted response is a request conversion failure.
		{
			name:      "ACK nil response",
			result:    protocol.ResultACK,
			isError:   true,
			errorText: "response conversion returned nil event",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mt := startMockTracer(t)
			service := New().(*observabilityService)
			_, finish := service.RecordRequestEvent(context.Background(), testEvent())
			finish(tc.result, tc.response)
			span := requireSpan(t, mt)
			if got := span.Tag(ext.ErrorMsg); (got != nil) != tc.isError {
				t.Errorf("error tag = %#v, want error=%v", got, tc.isError)
			}
			if tc.errorText != "" && !strings.Contains(fmt.Sprint(span.Tag(ext.ErrorMsg)), tc.errorText) {
				t.Errorf("conversion error = %q", span.Tag(ext.ErrorMsg))
			}
		})
	}
}

func TestConsumer(t *testing.T) {
	producerMT := mocktracer.Start()
	service := New().(*observabilityService)
	e := testEvent()
	_, finishProducer := service.RecordSendingEvent(context.Background(), e)
	finishProducer(nil)
	producer := requireSpan(t, producerMT)
	producerMT.Stop()
	e.SetExtension("baggage", "tenant=coop")
	message := (*binding.EventMessage)(&e)
	withoutContext := testEvent()

	tests := []struct {
		name    string
		message binding.Message
		event   *cloudevents.Event
		result  error
		linked  bool
	}{
		{name: "linked nil", message: message, event: &e, linked: true},
		{name: "linked ACK", message: message, event: &e, result: protocol.ResultACK, linked: true},
		{name: "linked NACK", message: message, event: &e, result: protocol.ResultNACK, linked: true},
		{
			name:    "linked error",
			message: message,
			event:   &e,
			result:  errors.New("handler failed"),
			linked:  true,
		},
		{name: "nil event", message: message, linked: true},
		{name: "decoded event fallback", event: &e, linked: true},
		{
			name:    "missing context",
			message: (*binding.EventMessage)(&withoutContext),
			event:   &withoutContext,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mt := startMockTracer(t)
			ctx := service.InboundContextDecorators()[0](context.Background(), tc.message)
			handlerCtx, finish := service.RecordCallingInvoker(ctx, tc.event)
			handler, _ := tracer.StartSpanFromContext(handlerCtx, "handler")
			handler.Finish()
			finish(tc.result)
			spans := mt.FinishedSpans()
			if len(spans) != 2 {
				t.Fatalf("got %d spans, want 2", len(spans))
			}
			consumer := spans[1]
			if consumer.ParentID() != 0 || spans[0].ParentID() != consumer.SpanID() {
				t.Errorf("consumer/handler hierarchy incorrect: consumer=%s handler=%s", consumer, spans[0])
			}
			links := consumer.Links()
			wantLinks := 0
			if tc.linked {
				wantLinks = 1
			}
			if len(links) != wantLinks || tc.linked && links[0].SpanID != producer.SpanID() {
				t.Errorf("links = %#v, linked=%v producer=%d", links, tc.linked, producer.SpanID())
			}
			var baggage string
			consumer.Context().ForeachBaggageItem(func(key, value string) bool {
				if key == "tenant" {
					baggage = value
				}
				return true
			})
			if gotBaggage := baggage == "coop"; gotBaggage != tc.linked {
				t.Errorf("baggage tenant = %q, linked=%v", baggage, tc.linked)
			}
			if consumer.Tag(ext.SpanType) != ext.SpanTypeMessageConsumer || consumer.Tag(ext.SpanKind) != ext.SpanKindConsumer {
				t.Errorf("consumer tags = %#v", consumer.Tags())
			}
			wantError := tc.result != nil && !protocol.IsACK(tc.result)
			if got := consumer.Tag(ext.ErrorMsg); (got != nil) != wantError {
				t.Errorf("error tag = %#v, want error=%v", got, wantError)
			}
		})
	}

	t.Run("malformed context", func(t *testing.T) {
		mt := startMockTracer(t)
		bad := testEvent()
		bad.SetExtension("traceparent", "not-a-traceparent")
		ctx := service.InboundContextDecorators()[0](context.Background(), (*binding.EventMessage)(&bad))
		_, finish := service.RecordCallingInvoker(ctx, &bad)
		finish(nil)
		if links := requireSpan(t, mt).Links(); len(links) != 0 {
			t.Fatalf("malformed context created links: %#v", links)
		}
	})
}

func TestOptions(t *testing.T) {
	mt := startMockTracer(t)
	service := New(
		WithService("custom-service"),
		WithMessagingSystem("kafka"),
		WithDestinationName("orders"),
		WithSubject(),
	).(*observabilityService)

	service.RecordReceivedMalformedEvent(context.Background(), errors.New("bad event"))
	_, finish := service.RecordSendingEvent(context.Background(), testEvent())
	finish(nil)

	spans := mt.FinishedSpans()
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2", len(spans))
	}
	for _, span := range spans {
		assertSpanTags(t, span, map[string]any{
			ext.ServiceName:              "custom-service",
			ext.MessagingSystem:          "kafka",
			ext.MessagingDestinationName: "orders",
		})
	}

	send := spans[1]
	if got := send.Tag("cloudevents.subject"); got != "orders/123" {
		t.Errorf("subject = %#v, want orders/123", got)
	}
}

func TestMalformedEvent(t *testing.T) {
	mt := startMockTracer(t)
	New().(*observabilityService).RecordReceivedMalformedEvent(
		context.Background(),
		errors.New("bad event"),
	)

	span := requireSpan(t, mt)
	assertSpanTags(t, span, map[string]any{
		ext.ResourceName: "malformed",
		ext.SpanType:     ext.SpanTypeMessageConsumer,
	})
	if span.OperationName() != "cloudevents.consume" {
		t.Errorf("operation name = %q, want cloudevents.consume", span.OperationName())
	}
	if span.ParentID() != 0 {
		t.Errorf("parent ID = %d, want 0", span.ParentID())
	}
	if span.Tag(ext.ErrorMsg) == nil {
		t.Error("malformed span does not contain an error")
	}
	for _, key := range []string{"cloudevents.id", "cloudevents.type", "cloudevents.source"} {
		if value := span.Tag(key); value != nil {
			t.Errorf("malformed span tag %q = %#v, want nil", key, value)
		}
	}
}

func TestAnalytics(t *testing.T) {
	t.Setenv("DD_TRACE_CLOUDEVENTS_ANALYTICS_ENABLED", "true")
	mt := startMockTracer(t)
	service := New().(*observabilityService)
	_, finish := service.RecordSendingEvent(context.Background(), testEvent())
	finish(nil)
	if got := requireSpan(t, mt).Tag(ext.EventSampleRate); got != 1.0 {
		t.Errorf("analytics rate = %#v, want 1", got)
	}
}
