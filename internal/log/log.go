package log

import (
	"fmt"
	"github.com/nemke/nagare-go/internal/paths"
	"os"
	"path/filepath"
	"time"
)

var file *os.File

// Init opens the log file. Call once at startup.
func Init() {
	dir := paths.Data()
	os.MkdirAll(dir, 0755)
	path := filepath.Join(dir, "nagare-go.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	file = f
}

// Close closes the log file.
func Close() {
	if file != nil {
		file.Close()
	}
}

func write(level, msg string) {
	if file == nil {
		return
	}
	ts := time.Now().Format("15:04:05.000")
	fmt.Fprintf(file, "%s [%s] %s\n", ts, level, msg)
}

// Debug logs a debug message.
func Debug(format string, args ...interface{}) {
	write("DBG", fmt.Sprintf(format, args...))
}

// Info logs an info message.
func Info(format string, args ...interface{}) {
	write("INF", fmt.Sprintf(format, args...))
}

// Error logs an error message.
func Error(format string, args ...interface{}) {
	write("ERR", fmt.Sprintf(format, args...))
}
