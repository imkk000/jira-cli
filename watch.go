package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"
)

const (
	defaultWatchInterval = 30 * time.Second
	minWatchInterval     = 5 * time.Second
	clearScreen          = "\x1b[H\x1b[2J"
	clockLayout          = "15:04:05"
)

type watchFlags struct {
	enabled  bool
	interval time.Duration
	json     *bool
}

func addWatchFlags(fs *flag.FlagSet, asJSON *bool) *watchFlags {
	wf := &watchFlags{json: asJSON}
	fs.BoolVar(&wf.enabled, "w", false, "shorthand for -watch")
	fs.BoolVar(&wf.enabled, "watch", false, "redraw the output every -interval until interrupted")
	fs.DurationVar(&wf.interval, "interval", defaultWatchInterval, "refresh interval for -watch")
	return wf
}

func (wf *watchFlags) run(ctx context.Context, out io.Writer, render func(io.Writer) error) error {
	if !wf.enabled {
		return render(out)
	}
	if *wf.json {
		return errors.New("-watch cannot be combined with -json")
	}
	if wf.interval < minWatchInterval {
		return fmt.Errorf("-interval must be at least %s", minWatchInterval)
	}
	err := watch(ctx, out, wf.interval, render)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func watch(ctx context.Context, out io.Writer, interval time.Duration, render func(io.Writer) error) error {
	var last string
	var updated time.Time
	for {
		var frame bytes.Buffer
		err := render(&frame)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil && updated.IsZero() {
			return err
		}
		if err == nil {
			last, updated = frame.String(), time.Now()
		}
		status := fmt.Sprintf("Every %s, updated %s (Ctrl-C to stop)\n", interval, updated.Format(clockLayout))
		if err != nil {
			status += fmt.Sprintf("Refresh failed at %s: %v\n", time.Now().Format(clockLayout), err)
		}
		if _, err := io.WriteString(out, clearScreen+status+"\n"+last); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}
