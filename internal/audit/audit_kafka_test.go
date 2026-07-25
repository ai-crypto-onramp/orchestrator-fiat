package audit

import (
	"testing"

	"github.com/segmentio/kafka-go"
)

func TestKafkaSinkEmitWithWriter(t *testing.T) {
	s := &KafkaSink{
		writer: &kafka.Writer{
			Addr:         kafka.TCP("localhost:1"),
			Topic:        AuditTopic,
			Balancer:     &kafka.LeastBytes{},
			BatchTimeout: 1,
			RequiredAcks: kafka.RequireNone,
			Async:        true,
		},
	}
	defer func() { _ = s.Close() }()
	s.Emit(Event{IntentID: "x", FromState: "intent", ToState: "authorized"})
}

func TestKafkaSinkEmitMarshalErrors(t *testing.T) {
	s := &KafkaSink{
		writer: &kafka.Writer{
			Addr:         kafka.TCP("localhost:1"),
			Topic:        AuditTopic,
			Async:        true,
			RequiredAcks: kafka.RequireNone,
		},
	}
	defer func() { _ = s.Close() }()
	// Normal event should marshal fine; this exercises the json.Marshal success path.
	s.Emit(Event{IntentID: "x", FromState: "intent", ToState: "authorized"})
	// Event with zero At: should be set to now internally.
	s.Emit(Event{IntentID: "y"})
}