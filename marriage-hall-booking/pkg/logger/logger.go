// Package logger is the application's levelled logger.
//
// ponytail: log/slog from the standard library, not zap or zerolog. slog gives
// levels, structured fields and a swappable handler, which is the whole
// requirement here.
package logger

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Component tags every line with its subsystem (api, worker, kafka, db...) so
// one stream can be filtered without guessing from the message text.
const Component = "component"

var levels = map[string]slog.Level{
	"DEBUG": slog.LevelDebug,
	"INFO":  slog.LevelInfo,
	"WARN":  slog.LevelWarn,
	"ERROR": slog.LevelError,
}

// Init configures the default slog logger. LOG_LEVEL selects the threshold
// (DEBUG/INFO/WARN/ERROR, default INFO) and LOG_FORMAT=json switches to JSON
// for log shippers; the default is a compact line meant to be read by a person.
func Init(component string) {
	level := slog.LevelInfo
	if l, ok := levels[strings.ToUpper(os.Getenv("LOG_LEVEL"))]; ok {
		level = l
	}

	var h slog.Handler
	if strings.EqualFold(os.Getenv("LOG_FORMAT"), "json") {
		h = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	} else {
		h = &textHandler{level: level, out: os.Stdout}
	}
	slog.SetDefault(slog.New(h).With(Component, component))
}

// textHandler prints "HH:MM:SS LEVEL component  message  key=value", which is
// greppable by level and short enough to scan. slog's own TextHandler emits
// logfmt with a full timestamp on every line, which is noisier to read.
type textHandler struct {
	level slog.Level
	out   *os.File
	attrs []slog.Attr
	group string
}

func (h *textHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func (h *textHandler) Handle(_ context.Context, r slog.Record) error {
	var sb strings.Builder
	sb.WriteString(r.Time.Format("15:04:05"))
	sb.WriteByte(' ')
	sb.WriteString(fmt.Sprintf("%-5s", r.Level.String()))
	sb.WriteByte(' ')

	component := ""
	fields := make([]string, 0, r.NumAttrs()+len(h.attrs))
	add := func(a slog.Attr) {
		if a.Key == Component {
			component = a.Value.String()
			return
		}
		fields = append(fields, a.Key+"="+a.Value.String())
	}
	for _, a := range h.attrs {
		add(a)
	}
	r.Attrs(func(a slog.Attr) bool { add(a); return true })

	if component != "" {
		sb.WriteString(fmt.Sprintf("%-7s ", component))
	}
	sb.WriteString(r.Message)
	for _, f := range fields {
		sb.WriteString("  ")
		sb.WriteString(f)
	}
	sb.WriteByte('\n')
	_, err := h.out.WriteString(sb.String())
	return err
}

func (h *textHandler) WithAttrs(as []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append(append([]slog.Attr{}, h.attrs...), as...)
	return &c
}

func (h *textHandler) WithGroup(name string) slog.Handler {
	c := *h
	c.group = name
	return &c
}

// Convenience wrappers so call sites stay short.
func Debug(msg string, args ...any) { slog.Debug(msg, args...) }
func Info(msg string, args ...any)  { slog.Info(msg, args...) }
func Warn(msg string, args ...any)  { slog.Warn(msg, args...) }
func Error(msg string, args ...any) { slog.Error(msg, args...) }

// Fatal logs at ERROR and exits. For start-up failures only - nothing serving
// traffic should ever call it.
func Fatal(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}

// With returns a logger carrying extra fields, for a subsystem that repeats them.
func With(args ...any) *slog.Logger { return slog.Default().With(args...) }

// Err wraps an error as a field, so every site spells it the same way.
func Err(err error) slog.Attr { return slog.String("error", err.Error()) }

// Dur formats a duration compactly.
func Dur(d time.Duration) slog.Attr { return slog.String("took", d.Round(time.Millisecond).String()) }
