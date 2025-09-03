package logger

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
)

// LogLevel represents the level of logging that should be performed
type LogLevel int

const (
	// LevelDebug logs all information
	LevelDebug LogLevel = iota
	// LevelWarn only logs warnings and errors
	LevelWarn LogLevel = iota
)

var (
	logLevel           = LevelWarn
	output   io.Writer = os.Stdout
)

// SetLogLevel set the level of logging for the ddlambda
func SetLogLevel(ll LogLevel) {
	logLevel = ll
}

// SetOutput changes the writer for the logger
func SetOutput(w io.Writer) {
	log.SetOutput(w)
	output = w
}

// Error logs a structured error message to stdout
func Error(err error) {
	finalMessage := logStructure{
		Status:  "error",
		Message: fmt.Sprintf("datadog: %s", err.Error()),
	}
	result, _ := json.Marshal(finalMessage)

	log.Println(string(result))
}

// Debug logs a structured log message to stdout
func Debug(message string) {
	if logLevel > LevelDebug {
		return
	}
	finalMessage := logStructure{
		Status:  "debug",
		Message: fmt.Sprintf("datadog: %s", message),
	}

	result, _ := json.Marshal(finalMessage)

	log.Println(string(result))
}

// Warn logs a structured log message to stdout
func Warn(message string) {
	if logLevel > LevelWarn {
		return
	}
	finalMessage := logStructure{
		Status:  "warning",
		Message: fmt.Sprintf("datadog: %s", message),
	}

	result, _ := json.Marshal(finalMessage)

	log.Println(string(result))
}

// Raw prints a raw message to the logs.
func Raw(message string) {
	fmt.Fprintln(output, message)
}

type logStructure struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}
