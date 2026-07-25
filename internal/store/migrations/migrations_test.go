package migrations

import (
	"context"
	"errors"
	"testing"
)

func TestAllReturnsMigrations(t *testing.T) {
	migs, err := All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(migs) == 0 {
		t.Fatal("expected at least one migration")
	}
	for _, m := range migs {
		if m.Up == "" || m.Down == "" {
			t.Fatalf("migration %s missing up/down", m.Version)
		}
	}
}

func TestNewRunner(t *testing.T) {
	r := NewRunner(nil, nil)
	if r == nil {
		t.Fatal("expected runner")
	}
}

func TestRunnerUpHappy(t *testing.T) {
	var execCalls []string
	var queryCalls []string
	r := NewRunner(
		func(ctx context.Context, q string, args ...any) error {
			execCalls = append(execCalls, q)
			return nil
		},
		func(ctx context.Context, v string) (bool, error) {
			queryCalls = append(queryCalls, v)
			return false, nil
		},
	)
	if err := r.Up(context.Background()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if len(queryCalls) == 0 {
		t.Fatal("expected query calls")
	}
	if len(execCalls) == 0 {
		t.Fatal("expected exec calls")
	}
}

func TestRunnerUpCreateSchemaFails(t *testing.T) {
	r := NewRunner(
		func(ctx context.Context, q string, args ...any) error {
			return errors.New("create schema failed")
		},
		nil,
	)
	if err := r.Up(context.Background()); err == nil {
		t.Fatal("expected error on create schema")
	}
}

func TestRunnerUpQueryFails(t *testing.T) {
	first := true
	r := NewRunner(
		func(ctx context.Context, q string, args ...any) error {
			if first {
				first = false
				return nil // create schema_migrations
			}
			return nil
		},
		func(ctx context.Context, v string) (bool, error) {
			return false, errors.New("query boom")
		},
	)
	if err := r.Up(context.Background()); err == nil {
		t.Fatal("expected error on query failure")
	}
}

func TestRunnerUpSkipApplied(t *testing.T) {
	r := NewRunner(
		func(ctx context.Context, q string, args ...any) error { return nil },
		func(ctx context.Context, v string) (bool, error) { return true, nil },
	)
	if err := r.Up(context.Background()); err != nil {
		t.Fatalf("Up: %v", err)
	}
}

func TestRunnerUpApplyFails(t *testing.T) {
	skipSchema := true
	r := NewRunner(
		func(ctx context.Context, q string, args ...any) error {
			if skipSchema {
				skipSchema = false
				return nil
			}
			return errors.New("apply failed")
		},
		func(ctx context.Context, v string) (bool, error) { return false, nil },
	)
	if err := r.Up(context.Background()); err == nil {
		t.Fatal("expected apply error")
	}
}

func TestRunnerUpRecordFails(t *testing.T) {
	call := 0
	r := NewRunner(
		func(ctx context.Context, q string, args ...any) error {
			call++
			// 0: schema, 1: apply up, 2: record insert -> fail
			if call == 2 {
				return errors.New("record failed")
			}
			return nil
		},
		func(ctx context.Context, v string) (bool, error) { return false, nil },
	)
	if err := r.Up(context.Background()); err == nil {
		t.Fatal("expected record error")
	}
}

func TestRunnerUpWithoutQueryFunc(t *testing.T) {
	r := NewRunner(
		func(ctx context.Context, q string, args ...any) error { return nil },
		nil,
	)
	if err := r.Up(context.Background()); err != nil {
		t.Fatalf("Up without query func: %v", err)
	}
}

func TestRunnerDownHappy(t *testing.T) {
	var execCalls []string
	r := NewRunner(
		func(ctx context.Context, q string, args ...any) error {
			execCalls = append(execCalls, q)
			return nil
		},
		func(ctx context.Context, v string) (bool, error) { return true, nil },
	)
	if err := r.Down(context.Background()); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if len(execCalls) == 0 {
		t.Fatal("expected exec calls during down")
	}
}

func TestRunnerDownQueryFails(t *testing.T) {
	r := NewRunner(
		func(ctx context.Context, q string, args ...any) error { return nil },
		func(ctx context.Context, v string) (bool, error) { return false, errors.New("boom") },
	)
	if err := r.Down(context.Background()); err == nil {
		t.Fatal("expected query error")
	}
}

func TestRunnerDownSkipNotApplied(t *testing.T) {
	r := NewRunner(
		func(ctx context.Context, q string, args ...any) error { return nil },
		func(ctx context.Context, v string) (bool, error) { return false, nil },
	)
	if err := r.Down(context.Background()); err != nil {
		t.Fatalf("Down: %v", err)
	}
}

func TestRunnerDownRevertFails(t *testing.T) {
	call := 0
	r := NewRunner(
		func(ctx context.Context, q string, args ...any) error {
			call++
			// call 1 = m.Down revert; make it fail.
			if call == 1 {
				return errors.New("revert failed")
			}
			return nil
		},
		func(ctx context.Context, v string) (bool, error) { return true, nil },
	)
	if err := r.Down(context.Background()); err == nil {
		t.Fatal("expected revert error")
	}
}

func TestRunnerDownDeleteFails(t *testing.T) {
	call := 0
	r := NewRunner(
		func(ctx context.Context, q string, args ...any) error {
			call++
			if call == 2 {
				return errors.New("delete failed")
			}
			return nil
		},
		func(ctx context.Context, v string) (bool, error) { return true, nil },
	)
	if err := r.Down(context.Background()); err == nil {
		t.Fatal("expected delete error")
	}
}

func TestRunnerDownWithoutQueryFunc(t *testing.T) {
	r := NewRunner(
		func(ctx context.Context, q string, args ...any) error { return nil },
		nil,
	)
	if err := r.Down(context.Background()); err != nil {
		t.Fatalf("Down without query func: %v", err)
	}
}