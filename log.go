package log

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var loggerSingleton *logger

// Level defined the type for a log level.
type Level int

const (
	// LogLevelDebug debug log level.
	LogLevelDebug Level = 0

	// LogLevelInfo info log level.
	LogLevelInfo Level = 1

	// LogLevelWarn warn log level.
	LogLevelWarn Level = 2

	// LogLevelError error log level.
	LogLevelError Level = 3

	// LogLevelFatal fatal log level.
	LogLevelFatal Level = 4
)

type logger struct {
	token         string
	Level         Level
	url           string
	bulk          bool
	bufferSize    int
	flushInterval time.Duration
	buffer        []*logMessage
	sync.Mutex
	tags      []string
	debugMode bool
}

type logMessage struct {
	Timestamp string      `json:"timestamp"`
	Level     string      `json:"level"`
	Message   string      `json:"message"`
	Metadata  interface{} `json:"metadata"`
}

// SetupLogger creates a new loggly logger.
func SetupLogger(token string, level Level, tags []string, bulk bool, debugMode bool) {
	if loggerSingleton != nil {
		return
	}

	// Setup logger with options.
	loggerSingleton = &logger{
		token:         token,
		Level:         level,
		url:           "",
		bulk:          bulk,
		bufferSize:    1000,
		flushInterval: 10 * time.Second,
		buffer:        nil,
		tags:          tags,
		debugMode:     debugMode,
	}

	// If the bulk option is set make sure we set the url to the bulk endpoint.
	if bulk {
		loggerSingleton.url = "https://logs-01.loggly.com/bulk/" + token + "/tag/" + tagList() + "/"

		// Start flush interval
		go start()
	} else {
		loggerSingleton.url = "https://logs-01.loggly.com/inputs/" + token + "/tag/" + tagList() + "/"
	}

}

// Stdln prints the output.
func Stdln(output string) {
	fmt.Println(output)
}

// Stdf prints the formatted output.
func Stdf(format string, a ...interface{}) {
	fmt.Printf(format, a...)
}

// Debugln prints the output.
func Debugln(output string) {
	Debugd(output, nil)
}

// Debugd prints output string and data.
func Debugd(output string, d interface{}) {
	buildAndShipMessage(output, "DEBUG", false, d)
}

// Debugf prints the formatted output.
func Debugf(format string, a ...interface{}) {
	Debugln(fmt.Sprintf(format, a...))
}

// Infoln prints the output.
func Infoln(output string) {
	Infod(output, nil)
}

// Infof prints the formatted output.
func Infof(format string, a ...interface{}) {
	Infoln(fmt.Sprintf(format, a...))
}

// Infod prints output string and data.
func Infod(output string, d interface{}) {
	buildAndShipMessage(output, "INFO", false, d)
}

// Warnln prints the output.
func Warnln(output string) {
	Warnd(output, nil)
}

// Warnf prints the formatted output.
func Warnf(format string, a ...interface{}) {
	Warnln(fmt.Sprintf(format, a...))
}

// Warnd prints output string and data.
func Warnd(output string, d interface{}) {
	buildAndShipMessage(output, "WARN", false, d)
}

// Errorln prints the output.
func Errorln(output string) {
	Errord(output, nil)
}

// Errorf prints the formatted output.
func Errorf(format string, a ...interface{}) {
	Errorln(fmt.Sprintf(format, a...))
}

// Errord prints output string and data.
func Errord(output string, d interface{}) {
	buildAndShipMessage(output, "ERROR", false, d)
}

// Fatalln prints the output.
func Fatalln(output string) {
	Fatald(output, nil)
}

// Fatalf prints the formatted output.
func Fatalf(format string, a ...interface{}) {
	Fatalln(fmt.Sprintf(format, a...))
}

// Fatald prints output string and data.
func Fatald(output string, d interface{}) {
	buildAndShipMessage(output, "FATAL", true, d)

}

// MARK: Private

func buildAndShipMessage(output string, messageType string, exit bool, d interface{}) {
	if loggerSingleton.Level > LogLevelDebug {
		return
	}

	var formattedOutput string

	if d == nil {
		// Format message.
		formattedOutput = fmt.Sprintf("%v [%s] %s", time.Now().Format("2006-01-02T15:04:05.000Z"), messageType, output)
	} else {
		// Format message.
		formattedOutput = fmt.Sprintf("%v [%s] %s %+v", time.Now().Format("2006-01-02T15:04:05.000Z"), messageType, output, d)
	}

	fmt.Println(formattedOutput)

	message := newMessage(time.Now().Format("2006-01-02T15:04:05.000Z"), messageType, output, nil)

	// Send message to loggly.
	ship(message)

	if exit {
		os.Exit(1)
	}
}

func newMessage(timestamp string, level string, message string, data ...interface{}) *logMessage {
	formatedMessage := &logMessage{
		Timestamp: timestamp,
		Level:     level,
		Message:   message,
		Metadata:  data,
	}

	return formatedMessage
}

func ship(message *logMessage) {
	// If bulk is set to true then ship on interval else ship the single log event.
	if loggerSingleton.bulk {
		go handleBulkLogMessage(message)
	} else {
		go handleLogMessage(message)
	}
}

func handleLogMessage(message *logMessage) {
	requestBody, err := json.Marshal(message)

	if err != nil {
		fmt.Printf("There was an error marshalling log message: %s", err)
		return
	}

	resp, err := http.Post(loggerSingleton.url, "text/plain", bytes.NewBuffer(requestBody))
	
	if err != nil {
		if loggerSingleton.debugMode {
			fmt.Printf("There was an error shipping the logs to loggy: %s", err)
		}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == 403 {
		if loggerSingleton.debugMode {
			fmt.Println("Token is invalid", resp.Status)
		}

	}

	if resp.StatusCode == 200 {
		if loggerSingleton.debugMode {
			fmt.Println("Log was shipped successfully", resp.Status)
		}
	}

}

func handleBulkLogMessage(message *logMessage) {
	var count int

	// Lock buffer from outside manipulation.
	loggerSingleton.Lock()

	loggerSingleton.buffer = append(loggerSingleton.buffer, message)

	count = len(loggerSingleton.buffer)

	// Unlock buffer from outside manipulation.
	loggerSingleton.Unlock()

	// Send buffer to loggly if the buffer size has been met.
	if count >= loggerSingleton.bufferSize {
		go flush()
	}

}

func flush() {
	body := formatBulkMessage()

	loggerSingleton.buffer = nil

	resp, err := http.Post(loggerSingleton.url, "text/plain", bytes.NewBuffer([]byte(body)))

	if resp.StatusCode == 403 {
		if loggerSingleton.debugMode {
			fmt.Println("Token is invalid", resp.Status)
		}
	}

	if resp.StatusCode == 200 {
		if loggerSingleton.debugMode {
			fmt.Println("Logs were shipped successfully", resp.Status)
		}
	}

	if err != nil {
		if loggerSingleton.debugMode {
			fmt.Printf("There was an error shipping the bulk logs to loggy: %s", err)
		}

	}

	defer resp.Body.Close()
}

func start() {
	for {
		time.Sleep(loggerSingleton.flushInterval)
		go flush()
	}
}

func tagList() string {
	return strings.Join(loggerSingleton.tags, ",")
}

func formatBulkMessage() string {
	var output string

	loggerSingleton.Lock()
	defer loggerSingleton.Unlock()

	for _, m := range loggerSingleton.buffer {
		b, err := json.Marshal(m)

		if err != nil {
			fmt.Printf("There was an error marshalling buffer message: %s", err)
			continue
		}

		output += string(b) + "\n"
	}

	return output
}
