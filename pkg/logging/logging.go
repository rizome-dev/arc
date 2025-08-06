// Package logging provides structured logging for ARC
package logging

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/rizome-dev/arc/pkg/config"
)

// LogLevel represents the severity of a log message
type LogLevel int

const (
	DebugLevel LogLevel = iota
	InfoLevel
	WarnLevel
	ErrorLevel
)

// String returns the string representation of the log level
func (l LogLevel) String() string {
	switch l {
	case DebugLevel:
		return "debug"
	case InfoLevel:
		return "info"
	case WarnLevel:
		return "warn"
	case ErrorLevel:
		return "error"
	default:
		return "unknown"
	}
}

// ParseLogLevel parses a string into a LogLevel
func ParseLogLevel(level string) LogLevel {
	switch strings.ToLower(level) {
	case "debug":
		return DebugLevel
	case "info":
		return InfoLevel
	case "warn", "warning":
		return WarnLevel
	case "error":
		return ErrorLevel
	default:
		return InfoLevel
	}
}

// LogEntry represents a structured log entry
type LogEntry struct {
	Timestamp  time.Time              `json:"timestamp"`
	Level      string                 `json:"level"`
	Message    string                 `json:"message"`
	Component  string                 `json:"component,omitempty"`
	Operation  string                 `json:"operation,omitempty"`
	TraceID    string                 `json:"trace_id,omitempty"`
	SpanID     string                 `json:"span_id,omitempty"`
	UserID     string                 `json:"user_id,omitempty"`
	RequestID  string                 `json:"request_id,omitempty"`
	Fields     map[string]interface{} `json:"fields,omitempty"`
	Error      string                 `json:"error,omitempty"`
	Stack      string                 `json:"stack,omitempty"`
	SourceFile string                 `json:"source_file,omitempty"`
	SourceLine int                    `json:"source_line,omitempty"`
}

// Logger provides structured logging functionality
type Logger struct {
	config    *config.LoggingConfig
	level     LogLevel
	writer    io.Writer
	formatter Formatter
	mu        sync.RWMutex
	fields    map[string]interface{} // Default fields for all log entries
}

// Formatter interface for log formatting
type Formatter interface {
	Format(entry *LogEntry) ([]byte, error)
}

// JSONFormatter formats log entries as JSON
type JSONFormatter struct{}

// TextFormatter formats log entries as plain text
type TextFormatter struct{}

// Format formats a log entry as JSON
func (f *JSONFormatter) Format(entry *LogEntry) ([]byte, error) {
	data, err := json.Marshal(entry)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// Format formats a log entry as plain text
func (f *TextFormatter) Format(entry *LogEntry) ([]byte, error) {
	var builder strings.Builder

	// Timestamp
	builder.WriteString(entry.Timestamp.Format("2006-01-02 15:04:05"))
	builder.WriteString(" ")

	// Level
	builder.WriteString(fmt.Sprintf("[%s]", strings.ToUpper(entry.Level)))
	builder.WriteString(" ")

	// Component
	if entry.Component != "" {
		builder.WriteString(fmt.Sprintf("[%s]", entry.Component))
		builder.WriteString(" ")
	}

	// Message
	builder.WriteString(entry.Message)

	// Fields
	if len(entry.Fields) > 0 {
		builder.WriteString(" ")
		for key, value := range entry.Fields {
			builder.WriteString(fmt.Sprintf("%s=%v ", key, value))
		}
	}

	// Error
	if entry.Error != "" {
		builder.WriteString(fmt.Sprintf(" error=%s", entry.Error))
	}

	// Request ID
	if entry.RequestID != "" {
		builder.WriteString(fmt.Sprintf(" request_id=%s", entry.RequestID))
	}

	// Trace ID
	if entry.TraceID != "" {
		builder.WriteString(fmt.Sprintf(" trace_id=%s", entry.TraceID))
	}

	builder.WriteString("\n")
	return []byte(builder.String()), nil
}

// NewLogger creates a new structured logger
func NewLogger(config *config.LoggingConfig) (*Logger, error) {
	logger := &Logger{
		config: config,
		level:  ParseLogLevel(config.Level),
		fields: make(map[string]interface{}),
	}

	// Setup output writer
	if err := logger.setupWriter(); err != nil {
		return nil, fmt.Errorf("failed to setup writer: %w", err)
	}

	// Setup formatter
	logger.setupFormatter()

	return logger, nil
}

// setupWriter configures the output writer
func (l *Logger) setupWriter() error {
	switch strings.ToLower(l.config.Output) {
	case "stdout":
		l.writer = os.Stdout
	case "stderr":
		l.writer = os.Stderr
	case "file":
		if l.config.FilePath == "" {
			return fmt.Errorf("file path must be specified for file output")
		}

		// Create directory if it doesn't exist
		dir := filepath.Dir(l.config.FilePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create log directory: %w", err)
		}

		// Open or create log file
		file, err := os.OpenFile(l.config.FilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return fmt.Errorf("failed to open log file: %w", err)
		}

		l.writer = file
	default:
		return fmt.Errorf("unsupported output type: %s", l.config.Output)
	}

	return nil
}

// setupFormatter configures the log formatter
func (l *Logger) setupFormatter() {
	switch strings.ToLower(l.config.Format) {
	case "json":
		l.formatter = &JSONFormatter{}
	case "text":
		l.formatter = &TextFormatter{}
	default:
		l.formatter = &JSONFormatter{} // Default to JSON
	}
}

// SetLevel sets the minimum log level
func (l *Logger) SetLevel(level LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// GetLevel returns the current log level
func (l *Logger) GetLevel() LogLevel {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.level
}

// WithField adds a field to the logger context
func (l *Logger) WithField(key string, value interface{}) *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()

	newLogger := *l
	newLogger.fields = make(map[string]interface{})
	for k, v := range l.fields {
		newLogger.fields[k] = v
	}
	newLogger.fields[key] = value

	return &newLogger
}

// WithFields adds multiple fields to the logger context
func (l *Logger) WithFields(fields map[string]interface{}) *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()

	newLogger := *l
	newLogger.fields = make(map[string]interface{})
	for k, v := range l.fields {
		newLogger.fields[k] = v
	}
	for k, v := range fields {
		newLogger.fields[k] = v
	}

	return &newLogger
}

// WithComponent adds a component field to the logger
func (l *Logger) WithComponent(component string) *Logger {
	return l.WithField("component", component)
}

// WithOperation adds an operation field to the logger
func (l *Logger) WithOperation(operation string) *Logger {
	return l.WithField("operation", operation)
}

// WithError adds an error field to the logger
func (l *Logger) WithError(err error) *Logger {
	if err == nil {
		return l
	}
	return l.WithField("error", err.Error())
}

// WithContext extracts relevant fields from context
func (l *Logger) WithContext(ctx context.Context) *Logger {
	fields := make(map[string]interface{})

	// Extract trace information if available
	if traceID := getTraceIDFromContext(ctx); traceID != "" {
		fields["trace_id"] = traceID
	}

	if spanID := getSpanIDFromContext(ctx); spanID != "" {
		fields["span_id"] = spanID
	}

	// Extract user information if available
	if userID := getUserIDFromContext(ctx); userID != "" {
		fields["user_id"] = userID
	}

	// Extract request ID if available
	if requestID := getRequestIDFromContext(ctx); requestID != "" {
		fields["request_id"] = requestID
	}

	if len(fields) == 0 {
		return l
	}

	return l.WithFields(fields)
}

// Debug logs a debug message
func (l *Logger) Debug(msg string, args ...interface{}) {
	if l.isLevelEnabled(DebugLevel) {
		l.log(DebugLevel, fmt.Sprintf(msg, args...), nil)
	}
}

// Info logs an info message
func (l *Logger) Info(msg string, args ...interface{}) {
	if l.isLevelEnabled(InfoLevel) {
		l.log(InfoLevel, fmt.Sprintf(msg, args...), nil)
	}
}

// Warn logs a warning message
func (l *Logger) Warn(msg string, args ...interface{}) {
	if l.isLevelEnabled(WarnLevel) {
		l.log(WarnLevel, fmt.Sprintf(msg, args...), nil)
	}
}

// Error logs an error message
func (l *Logger) Error(msg string, args ...interface{}) {
	if l.isLevelEnabled(ErrorLevel) {
		l.log(ErrorLevel, fmt.Sprintf(msg, args...), nil)
	}
}

// ErrorWithStack logs an error message with stack trace
func (l *Logger) ErrorWithStack(msg string, err error, args ...interface{}) {
	if l.isLevelEnabled(ErrorLevel) {
		stack := getStackTrace(2) // Skip this function and the caller
		entry := &LogEntry{
			Timestamp: time.Now().UTC(),
			Level:     ErrorLevel.String(),
			Message:   fmt.Sprintf(msg, args...),
			Fields:    l.copyFields(),
			Stack:     stack,
		}
		if err != nil {
			entry.Error = err.Error()
		}
		l.writeEntry(entry)
	}
}

// log is the internal logging method
func (l *Logger) log(level LogLevel, message string, err error) {
	if !l.isLevelEnabled(level) {
		return
	}

	entry := &LogEntry{
		Timestamp: time.Now().UTC(),
		Level:     level.String(),
		Message:   message,
		Fields:    l.copyFields(),
	}

	// Add error if provided
	if err != nil {
		entry.Error = err.Error()
	}

	// Add source information for error and warn levels
	if level >= WarnLevel {
		file, line := getCallerInfo(3) // Skip this function, the level function, and the actual caller
		entry.SourceFile = file
		entry.SourceLine = line
	}

	l.writeEntry(entry)
}

// writeEntry writes a log entry using the configured formatter
func (l *Logger) writeEntry(entry *LogEntry) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	data, err := l.formatter.Format(entry)
	if err != nil {
		// Fallback to standard log if formatting fails
		log.Printf("Failed to format log entry: %v", err)
		return
	}

	if _, err := l.writer.Write(data); err != nil {
		// Fallback to standard log if writing fails
		log.Printf("Failed to write log entry: %v", err)
	}
}

// isLevelEnabled checks if a log level is enabled
func (l *Logger) isLevelEnabled(level LogLevel) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return level >= l.level
}

// copyFields creates a copy of the logger's fields
func (l *Logger) copyFields() map[string]interface{} {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if len(l.fields) == 0 {
		return nil
	}

	fields := make(map[string]interface{}, len(l.fields))
	for k, v := range l.fields {
		fields[k] = v
	}
	return fields
}

// getCallerInfo gets the file and line number of the caller
func getCallerInfo(skip int) (string, int) {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		return "unknown", 0
	}
	return filepath.Base(file), line
}

// getStackTrace gets the current stack trace
func getStackTrace(skip int) string {
	var builder strings.Builder
	for i := skip; i < skip+10; i++ { // Limit to 10 frames
		pc, file, line, ok := runtime.Caller(i)
		if !ok {
			break
		}
		fn := runtime.FuncForPC(pc)
		if fn == nil {
			continue
		}
		builder.WriteString(fmt.Sprintf("%s:%d %s\n", filepath.Base(file), line, fn.Name()))
	}
	return builder.String()
}

// Context extraction functions
// These would be enhanced to work with actual tracing libraries like OpenTelemetry

func getTraceIDFromContext(ctx context.Context) string {
	// This would extract trace ID from OpenTelemetry context
	// For now, return empty string
	return ""
}

func getSpanIDFromContext(ctx context.Context) string {
	// This would extract span ID from OpenTelemetry context
	// For now, return empty string
	return ""
}

func getUserIDFromContext(ctx context.Context) string {
	// This would extract user ID from auth context
	if userInfo := ctx.Value("user_claims"); userInfo != nil {
		if claims, ok := userInfo.(*Claims); ok {
			return claims.UserID
		}
	}
	return ""
}

func getRequestIDFromContext(ctx context.Context) string {
	// This would extract request ID from context
	if reqID := ctx.Value("request_id"); reqID != nil {
		if id, ok := reqID.(string); ok {
			return id
		}
	}
	return ""
}

// Claims represents user claims (imported from middleware package conceptually)
type Claims struct {
	UserID   string   `json:"user_id"`
	Username string   `json:"username"`
	Roles    []string `json:"roles"`
}

// Global logger instance
var defaultLogger *Logger

// InitializeGlobalLogger initializes the global logger
func InitializeGlobalLogger(config *config.LoggingConfig) error {
	logger, err := NewLogger(config)
	if err != nil {
		return err
	}
	defaultLogger = logger
	return nil
}

// GetLogger returns the global logger
func GetLogger() *Logger {
	if defaultLogger == nil {
		// Create a basic logger as fallback
		config := &config.LoggingConfig{
			Level:  "info",
			Format: "json",
			Output: "stdout",
		}
		logger, _ := NewLogger(config)
		defaultLogger = logger
	}
	return defaultLogger
}

// Convenience functions for global logger

// Debug logs a debug message using the global logger
func Debug(msg string, args ...interface{}) {
	GetLogger().Debug(msg, args...)
}

// Info logs an info message using the global logger
func Info(msg string, args ...interface{}) {
	GetLogger().Info(msg, args...)
}

// Warn logs a warning message using the global logger
func Warn(msg string, args ...interface{}) {
	GetLogger().Warn(msg, args...)
}

// Error logs an error message using the global logger
func Error(msg string, args ...interface{}) {
	GetLogger().Error(msg, args...)
}

// WithField returns a logger with a field using the global logger
func WithField(key string, value interface{}) *Logger {
	return GetLogger().WithField(key, value)
}

// WithFields returns a logger with fields using the global logger
func WithFields(fields map[string]interface{}) *Logger {
	return GetLogger().WithFields(fields)
}

// WithComponent returns a logger with component using the global logger
func WithComponent(component string) *Logger {
	return GetLogger().WithComponent(component)
}

// WithError returns a logger with error using the global logger
func WithError(err error) *Logger {
	return GetLogger().WithError(err)
}

// WithContext returns a logger with context using the global logger
func WithContext(ctx context.Context) *Logger {
	return GetLogger().WithContext(ctx)
}