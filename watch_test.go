package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestWatchRedrawsUntilCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var calls int
	var out strings.Builder
	err := watch(ctx, &out, time.Millisecond, func(w io.Writer) error {
		calls++
		switch calls {
		case 2:
			return errors.New("boom")
		case 3:
			cancel()
			return context.Canceled
		}
		_, err := io.WriteString(w, "frame\n")
		return err
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("watch = %v, want context.Canceled", err)
	}
	if calls != 3 {
		t.Errorf("render called %d times, want 3", calls)
	}
	frames := strings.Split(out.String(), clearScreen)[1:]
	if len(frames) != 2 {
		t.Fatalf("got %d frames, want 2: %q", len(frames), out.String())
	}
	if !strings.HasSuffix(frames[0], "\nframe\n") || strings.Contains(frames[0], "Refresh failed") {
		t.Errorf("first frame = %q", frames[0])
	}
	if !strings.Contains(frames[1], "Refresh failed at ") || !strings.Contains(frames[1], "boom") || !strings.HasSuffix(frames[1], "\nframe\n") {
		t.Errorf("failed frame = %q", frames[1])
	}
}

func TestWatchReturnsFirstError(t *testing.T) {
	want := errors.New("boom")
	var out strings.Builder
	err := watch(t.Context(), &out, time.Millisecond, func(io.Writer) error { return want })
	if !errors.Is(err, want) || out.Len() != 0 {
		t.Errorf("watch = %v, output %q", err, out.String())
	}
}

func TestWatchFlagsRun(t *testing.T) {
	render := func(w io.Writer) error {
		_, err := io.WriteString(w, "once\n")
		return err
	}
	var plain bool
	asJSON := true
	var out strings.Builder
	if err := (&watchFlags{interval: time.Millisecond, json: &plain}).run(t.Context(), &out, render); err != nil || out.String() != "once\n" {
		t.Errorf("run without watch = %q, %v", out.String(), err)
	}
	if err := (&watchFlags{enabled: true, interval: defaultWatchInterval, json: &asJSON}).run(t.Context(), &out, render); err == nil {
		t.Error("-watch with -json accepted")
	}
	if err := (&watchFlags{enabled: true, interval: time.Second, json: &plain}).run(t.Context(), &out, render); err == nil {
		t.Error("interval below minimum accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := (&watchFlags{enabled: true, interval: defaultWatchInterval, json: &plain}).run(ctx, &out, render); err != nil {
		t.Errorf("interrupted watch = %v, want nil", err)
	}
}
