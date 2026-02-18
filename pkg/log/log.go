// Package log provides human-friendly logging for prisn.
//
// Design: Show what matters. Hide what doesn't. No corporate bullshit.
package log

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Level represents log verbosity.
type Level int

const (
	LevelQuiet   Level = iota // Errors only
	LevelNormal               // Default - key events
	LevelVerbose              // Extra detail
	LevelDebug                // Everything
)

// ANSI color codes
const (
	reset   = "\033[0m"
	dim     = "\033[2m"
	bold    = "\033[1m"
	red     = "\033[31m"
	green   = "\033[32m"
	yellow  = "\033[33m"
	blue    = "\033[34m"
	magenta = "\033[35m"
	cyan    = "\033[36m"
)

// Symbols for quick visual scanning
const (
	symOK      = "+"
	symFail    = "!"
	symWarn    = "~"
	symInfo    = "-"
	symStart   = ">"
	symDone    = "<"
	symWait    = "."
	symSkip    = "/"
)

// Logger is the main logging interface.
type Logger struct {
	mu       sync.Mutex
	out      io.Writer
	level    Level
	color    bool
	prefix   string
	start    time.Time
}

var (
	std     = New(os.Stderr)
	stdMu   sync.RWMutex
)

// New creates a logger.
func New(out io.Writer) *Logger {
	return &Logger{
		out:   out,
		level: LevelNormal,
		color: isTTY(out),
		start: time.Now(),
	}
}

// SetLevel sets the global log level.
func SetLevel(l Level) {
	stdMu.Lock()
	std.level = l
	stdMu.Unlock()
}

// SetOutput sets the global output.
func SetOutput(w io.Writer) {
	stdMu.Lock()
	std.out = w
	std.color = isTTY(w)
	stdMu.Unlock()
}

// SetColor forces color on/off.
func SetColor(on bool) {
	stdMu.Lock()
	std.color = on
	stdMu.Unlock()
}

// WithPrefix returns a logger with a prefix.
func WithPrefix(prefix string) *Logger {
	stdMu.RLock()
	l := &Logger{
		out:    std.out,
		level:  std.level,
		color:  std.color,
		prefix: prefix,
		start:  std.start,
	}
	stdMu.RUnlock()
	return l
}

// --- Core logging methods ---

// OK logs a success. Something worked.
func OK(format string, args ...any) {
	std.log(LevelNormal, symOK, green, format, args...)
}

// Fail logs a failure. Something broke.
func Fail(format string, args ...any) {
	std.log(LevelQuiet, symFail, red, format, args...)
}

// Warn logs a warning. Heads up, but not fatal.
func Warn(format string, args ...any) {
	std.log(LevelNormal, symWarn, yellow, format, args...)
}

// Info logs information. FYI.
func Info(format string, args ...any) {
	std.log(LevelNormal, symInfo, blue, format, args...)
}

// Start logs the beginning of something.
func Start(format string, args ...any) {
	std.log(LevelNormal, symStart, cyan, format, args...)
}

// Done logs completion.
func Done(format string, args ...any) {
	std.log(LevelNormal, symDone, green, format, args...)
}

// Wait logs waiting/in-progress.
func Wait(format string, args ...any) {
	std.log(LevelVerbose, symWait, dim, format, args...)
}

// Skip logs something skipped.
func Skip(format string, args ...any) {
	std.log(LevelVerbose, symSkip, dim, format, args...)
}

// Debug logs debug info. Only when you're hunting bugs.
func Debug(format string, args ...any) {
	std.log(LevelDebug, " ", dim, format, args...)
}

// --- Verbose variants ---

// V returns true if verbose logging is enabled.
func V() bool {
	stdMu.RLock()
	v := std.level >= LevelVerbose
	stdMu.RUnlock()
	return v
}

// VInfo logs info only in verbose mode.
func VInfo(format string, args ...any) {
	std.log(LevelVerbose, symInfo, blue, format, args...)
}

// --- Execution logging ---

// Exec logs an execution event with timing.
type ExecEvent struct {
	Name     string
	Duration time.Duration
	ExitCode int
	Error    error
}

// LogExec logs an execution result nicely.
func LogExec(e ExecEvent) {
	dur := formatDuration(e.Duration)
	if e.Error != nil {
		Fail("%s failed: %v %s", e.Name, e.Error, dim+dur+reset)
	} else if e.ExitCode != 0 {
		Fail("%s exited %d %s", e.Name, e.ExitCode, dim+dur+reset)
	} else {
		OK("%s %s", e.Name, dim+dur+reset)
	}
}

// --- HTTP request logging ---

// HTTPEvent represents an HTTP request.
type HTTPEvent struct {
	Method   string
	Path     string
	Status   int
	Duration time.Duration
	Error    error
}

// LogHTTP logs an HTTP request.
func LogHTTP(e HTTPEvent) {
	dur := formatDuration(e.Duration)
	path := truncate(e.Path, 40)

	if e.Error != nil {
		Fail("%s %s: %v", e.Method, path, e.Error)
	} else if e.Status >= 500 {
		Fail("%s %s %d %s", e.Method, path, e.Status, dur)
	} else if e.Status >= 400 {
		Warn("%s %s %d %s", e.Method, path, e.Status, dur)
	} else {
		std.log(LevelVerbose, symInfo, dim, "%s %s %d %s", e.Method, path, e.Status, dur)
	}
}

// --- Internal ---

func (l *Logger) log(minLevel Level, sym, color, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.level < minLevel {
		return
	}

	msg := fmt.Sprintf(format, args...)

	var line string
	if l.color {
		prefix := ""
		if l.prefix != "" {
			prefix = dim + l.prefix + " " + reset
		}
		line = fmt.Sprintf("%s%s%s%s %s%s\n", prefix, color, sym, reset, msg, reset)
	} else {
		prefix := ""
		if l.prefix != "" {
			prefix = l.prefix + " "
		}
		line = fmt.Sprintf("%s%s %s\n", prefix, sym, msg)
	}

	l.out.Write([]byte(line))
}

func formatDuration(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return fmt.Sprintf("%.0fus", float64(d.Microseconds()))
	case d < time.Second:
		return fmt.Sprintf("%.0fms", float64(d.Milliseconds()))
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return fmt.Sprintf("%.1fm", d.Minutes())
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func isTTY(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		fi, err := f.Stat()
		if err != nil {
			return false
		}
		return fi.Mode()&os.ModeCharDevice != 0
	}
	return false
}

// --- Progress for long operations ---

// Progress tracks progress of a multi-step operation.
type Progress struct {
	name    string
	total   int
	current int
	logger  *Logger
}

// NewProgress creates a progress tracker.
func NewProgress(name string, total int) *Progress {
	return &Progress{
		name:   name,
		total:  total,
		logger: std,
	}
}

// Step advances progress.
func (p *Progress) Step(msg string) {
	p.current++
	if p.logger.level >= LevelVerbose {
		p.logger.log(LevelVerbose, symWait, dim, "%s [%d/%d] %s", p.name, p.current, p.total, msg)
	}
}

// Done completes the progress.
func (p *Progress) Done() {
	OK("%s completed", p.name)
}

// Fail marks progress as failed.
func (p *Progress) Fail(err error) {
	Fail("%s failed at step %d/%d: %v", p.name, p.current, p.total, err)
}

// --- Table output for lists ---

// Table prints a simple aligned table.
func Table(headers []string, rows [][]string) {
	if len(rows) == 0 {
		Info("(none)")
		return
	}

	// Calculate column widths
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	// Print header
	stdMu.RLock()
	out := std.out
	color := std.color
	stdMu.RUnlock()

	var header strings.Builder
	for i, h := range headers {
		if color {
			header.WriteString(dim)
		}
		header.WriteString(fmt.Sprintf("%-*s", widths[i]+2, h))
		if color {
			header.WriteString(reset)
		}
	}
	fmt.Fprintln(out, header.String())

	// Print rows
	for _, row := range rows {
		var line strings.Builder
		for i, cell := range row {
			if i < len(widths) {
				line.WriteString(fmt.Sprintf("%-*s", widths[i]+2, cell))
			}
		}
		fmt.Fprintln(out, line.String())
	}
}
