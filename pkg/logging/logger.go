package logging

import (
"os"
"strings"
"time"

"github.com/rs/zerolog"
"github.com/rs/zerolog/log"
)

// Logger wraps zerolog.Logger
type Logger struct {
logger zerolog.Logger
}

// Init initializes the global logger based on configuration
func Init(level, format, output string) (*Logger, error) {
// Set log level
lvl, err := zerolog.ParseLevel(strings.ToLower(level))
if err != nil {
lvl = zerolog.InfoLevel
}
zerolog.SetGlobalLevel(lvl)

// Configure output
var writer = os.Stdout
if output == "stderr" {
writer = os.Stderr
} else if output != "stdout" && output != "" {
file, err := os.OpenFile(output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
if err != nil {
return nil, err
}
writer = file
}

// Configure format
var logger zerolog.Logger
if strings.ToLower(format) == "text" {
logger = zerolog.New(zerolog.ConsoleWriter{
Out:        writer,
TimeFormat: time.RFC3339,
}).With().Timestamp().Logger()
} else {
logger = zerolog.New(writer).With().Timestamp().Logger()
}

// Set global logger
log.Logger = logger

return &Logger{logger: logger}, nil
}

// Debug logs a debug message
func (l *Logger) Debug(msg string, fields ...interface{}) {
event := l.logger.Debug()
addFields(event, fields...)
event.Msg(msg)
}

// Info logs an info message
func (l *Logger) Info(msg string, fields ...interface{}) {
event := l.logger.Info()
addFields(event, fields...)
event.Msg(msg)
}

// Warn logs a warning message
func (l *Logger) Warn(msg string, fields ...interface{}) {
event := l.logger.Warn()
addFields(event, fields...)
event.Msg(msg)
}

// Error logs an error message
func (l *Logger) Error(msg string, fields ...interface{}) {
event := l.logger.Error()
addFields(event, fields...)
event.Msg(msg)
}

// Fatal logs a fatal message and exits
func (l *Logger) Fatal(msg string, fields ...interface{}) {
	event := l.logger.Fatal()
	addFields(event, fields...)
	event.Msg(msg)
}

// ZerologLogger returns the underlying zerolog.Logger
func (l *Logger) ZerologLogger() zerolog.Logger {
	return l.logger
}

// addFields adds key-value pairs to a log event
func addFields(event *zerolog.Event, fields ...interface{}) {
for i := 0; i < len(fields)-1; i += 2 {
key, ok := fields[i].(string)
if !ok {
continue
}
event.Interface(key, fields[i+1])
}
}

// Global logger instance
var globalLogger *Logger

// SetGlobal sets the global logger instance
func SetGlobal(l *Logger) {
globalLogger = l
}

// Debug logs a debug message using global logger
func Debug(msg string, fields ...interface{}) {
if globalLogger != nil {
globalLogger.Debug(msg, fields...)
}
}

// Info logs an info message using global logger
func Info(msg string, fields ...interface{}) {
if globalLogger != nil {
globalLogger.Info(msg, fields...)
}
}

// Warn logs a warning message using global logger
func Warn(msg string, fields ...interface{}) {
if globalLogger != nil {
globalLogger.Warn(msg, fields...)
}
}

// Error logs an error message using global logger
func Error(msg string, fields ...interface{}) {
if globalLogger != nil {
globalLogger.Error(msg, fields...)
}
}

// Fatal logs a fatal message using global logger and exits
func Fatal(msg string, fields ...interface{}) {
if globalLogger != nil {
globalLogger.Fatal(msg, fields...)
}
}
