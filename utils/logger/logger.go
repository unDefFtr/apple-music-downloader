package logger

import (
	"fmt"
	"os"

	"github.com/fatih/color"
)

type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
)

type Logger struct {
	level LogLevel
}

var (
	GlobalLogger = &Logger{level: LevelInfo} // Default to Info
	LogHook      func(level string, msg string)
)

func Init(debug bool) {
	if debug {
		GlobalLogger.level = LevelDebug
	} else {
		GlobalLogger.level = LevelInfo
	}
}

// Debug prints debug messages in gray
func Debug(a ...interface{}) {
	if GlobalLogger.level <= LevelDebug {
		if LogHook != nil {
			LogHook("DEBUG", fmt.Sprint(a...))
			return
		}
		color.Set(color.FgHiBlack)
		fmt.Print("[DEBUG] ")
		fmt.Println(a...)
		color.Unset()
	}
}

// Debugf prints formatted debug messages in gray
func Debugf(format string, a ...interface{}) {
	if GlobalLogger.level <= LevelDebug {
		if LogHook != nil {
			LogHook("DEBUG", fmt.Sprintf(format, a...))
			return
		}
		color.Set(color.FgHiBlack)
		fmt.Print("[DEBUG] ")
		fmt.Printf(format, a...)
		if format[len(format)-1] != '\n' {
			fmt.Println()
		}
		color.Unset()
	}
}

// Info prints standard messages
func Info(a ...interface{}) {
	if GlobalLogger.level <= LevelInfo {
		if LogHook != nil {
			LogHook("INFO", fmt.Sprint(a...))
			return
		}
		fmt.Println(a...)
	}
}

// Infof prints formatted standard messages
func Infof(format string, a ...interface{}) {
	if GlobalLogger.level <= LevelInfo {
		if LogHook != nil {
			LogHook("INFO", fmt.Sprintf(format, a...))
			return
		}
		fmt.Printf(format, a...)
	}
}

// Warn prints warning messages in yellow
func Warn(a ...interface{}) {
	if GlobalLogger.level <= LevelWarn {
		if LogHook != nil {
			LogHook("WARN", fmt.Sprint(a...))
			return
		}
		color.Set(color.FgYellow)
		fmt.Print("⚠️  ")
		fmt.Println(a...)
		color.Unset()
	}
}

// Warnf prints formatted warning messages in yellow
func Warnf(format string, a ...interface{}) {
	if GlobalLogger.level <= LevelWarn {
		if LogHook != nil {
			LogHook("WARN", fmt.Sprintf(format, a...))
			return
		}
		color.Set(color.FgYellow)
		fmt.Print("⚠️  ")
		fmt.Printf(format, a...)
		if len(format) > 0 && format[len(format)-1] != '\n' {
			fmt.Println()
		}
		color.Unset()
	}
}

// Error prints error messages in red
func Error(a ...interface{}) {
	if GlobalLogger.level <= LevelError {
		if LogHook != nil {
			LogHook("ERROR", fmt.Sprint(a...))
			return
		}
		color.Set(color.FgRed)
		fmt.Print("❌ ")
		fmt.Println(a...)
		color.Unset()
	}
}

// Errorf prints formatted error messages in red
func Errorf(format string, a ...interface{}) {
	if GlobalLogger.level <= LevelError {
		if LogHook != nil {
			LogHook("ERROR", fmt.Sprintf(format, a...))
			return
		}
		color.Set(color.FgRed)
		fmt.Print("❌ ")
		fmt.Printf(format, a...)
		if len(format) > 0 && format[len(format)-1] != '\n' {
			fmt.Println()
		}
		color.Unset()
	}
}

// Fatal prints error message and exits
func Fatal(a ...interface{}) {
	Error(a...)
	os.Exit(1)
}

// Fatalf prints formatted error message and exits
func Fatalf(format string, a ...interface{}) {
	Errorf(format, a...)
	os.Exit(1)
}
