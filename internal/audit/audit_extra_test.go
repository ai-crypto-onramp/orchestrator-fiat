package audit

import (
	"testing"
)

func TestRecorderEmitSetsAt(t *testing.T) {
	r := NewRecorder()
	r.Emit(Event{IntentID: "i", FromState: "intent", ToState: "authorized"})
	got := r.Events()
	if len(got) != 1 || got[0].At.IsZero() {
		t.Fatalf("At should be set when zero; got %+v", got)
	}
}

func TestNopSinkEmit(t *testing.T) {
	NopSink{}.Emit(Event{IntentID: "x"})
}

func TestNewKafkaSinkEmptyBrokers(t *testing.T) {
	if _, err := NewKafkaSink(nil); err == nil {
		t.Fatal("expected error for no brokers")
	}
}

func TestKafkaSinkCloseNilWriter(t *testing.T) {
	s := &KafkaSink{}
	if err := s.Close(); err != nil {
		t.Fatalf("Close on nil writer = %v", err)
	}
}

func TestKafkaSinkEmitNilWriterNoop(t *testing.T) {
	s := &KafkaSink{}
	s.Emit(Event{IntentID: "x", FromState: "intent", ToState: "authorized"})
}