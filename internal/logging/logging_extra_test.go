package logging

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestNewDefault(t *testing.T) {
	old, had := os.LookupEnv("LOG_LEVEL")
	os.Setenv("LOG_LEVEL", "debug")
	defer func() {
		if had {
			os.Setenv("LOG_LEVEL", old)
		} else {
			os.Unsetenv("LOG_LEVEL")
		}
	}()
	l := NewDefault()
	if l == nil {
		t.Fatal("expected logger")
	}
	if l.level != LevelDebug {
		t.Fatalf("level = %q, want debug", l.level)
	}
}

func TestNewNilWriterDiscard(t *testing.T) {
	l := New(nil, LevelInfo)
	l.Info("no-output", nil)
}

func TestLoggerWithInheritsParentFields(t *testing.T) {
	var buf bytes.Buffer
	parent := New(&buf, LevelInfo).With(map[string]interface{}{"svc": "po", "env": "test"})
	child := parent.With(map[string]interface{}{"req": "r1"})
	child.Info("hi", nil)
	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	for _, k := range []string{"svc", "env", "req"} {
		if _, ok := entry[k]; !ok {
			t.Fatalf("missing inherited field %q: %v", k, entry)
		}
	}
}

func TestParseLevelDefaults(t *testing.T) {
	if got := ParseLevel("DEBUG"); got != LevelDebug {
		t.Fatalf("DEBUG = %q", got)
	}
	if got := ParseLevel("WARN"); got != LevelWarn {
		t.Fatalf("WARN = %q", got)
	}
	if got := ParseLevel("ERROR"); got != LevelError {
		t.Fatalf("ERROR = %q", got)
	}
}

func TestLoggerAllLevels(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, LevelDebug)
	l.Debug("d", map[string]interface{}{"k": "v"})
	l.Info("i", nil)
	l.Warn("w", nil)
	l.Error("e", nil)
	out := buf.String()
	for _, want := range []string{"d", "i", "w", "e"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in: %s", want, out)
		}
	}
}