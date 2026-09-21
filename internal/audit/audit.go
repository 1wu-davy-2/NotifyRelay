// Package audit records delivery outcomes.
//
// M0 ships a structured-log recorder. M4 swaps in a database-backed recorder
// behind the same interface — auditing must happen in the application, never
// by parsing logs (see docs/00-research.md §2.6).
package audit

import (
	"context"
	"log/slog"

	"notifyrelay/internal/channel"
)

// Entry is one delivery record.
//
// It must never carry credentials: only identifiers, classifications and the
// peer's own response text.
type Entry struct {
	RequestID   string
	Target      string
	Channel     string // instance alias
	ChannelType string
	Class       channel.ResultClass
	Detail      string
	Err         string
	// SkipReason names why the channel was never called, and is empty for a
	// delivery that reached it. Without it a skipped delivery and an
	// unreachable one look identical in the log, because both are reported as
	// CONNECT_ERROR — and they call for opposite responses: one is a budget
	// that ran out, the other is a channel that is down.
	SkipReason string
	ElapsedMS  int64
	Recipients int
}

// Recorder records delivery outcomes.
type Recorder interface {
	Record(ctx context.Context, e Entry)
}

// SlogRecorder writes delivery records as structured log lines.
type SlogRecorder struct {
	log *slog.Logger
}

// NewSlogRecorder returns a Recorder backed by the given logger.
func NewSlogRecorder(log *slog.Logger) *SlogRecorder {
	return &SlogRecorder{log: log}
}

// Record implements Recorder.
func (r *SlogRecorder) Record(ctx context.Context, e Entry) {
	attrs := []slog.Attr{
		slog.String("request_id", e.RequestID),
		slog.String("target", e.Target),
		slog.String("channel", e.Channel),
		slog.String("channel_type", e.ChannelType),
		slog.String("result", e.Class.String()),
		slog.Int64("elapsed_ms", e.ElapsedMS),
	}
	if e.SkipReason != "" {
		attrs = append(attrs, slog.String("skip_reason", e.SkipReason))
	}
	if e.Detail != "" {
		attrs = append(attrs, slog.String("detail", e.Detail))
	}
	if e.Err != "" {
		attrs = append(attrs, slog.String("error", e.Err))
	}
	if e.Recipients > 0 {
		attrs = append(attrs, slog.Int("recipients", e.Recipients))
	}

	r.log.LogAttrs(ctx, levelFor(e.Class), "delivery", attrs...)
}

func levelFor(c channel.ResultClass) slog.Level {
	switch c {
	case channel.ClassSent:
		return slog.LevelInfo
	case channel.ClassNotAttempted, channel.ClassConnectError, channel.ClassTransient:
		// A skipped delivery is a warning rather than a routine line: the
		// queue is not draining, and on a quiet system that is the first sign
		// that a channel is out of service or an allowance has run out.
		return slog.LevelWarn
	case channel.ClassPermanent:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
