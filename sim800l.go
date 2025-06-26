// Package sim800l implements a driver for the SIM800L GSM/GPRS module.
// It is optimized for TinyGo running on constrained environments like the Raspberry Pi Pico.
package sim800l

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"machine"
	"strconv"
	"strings"
	"time"

	"tinygo.org/x/drivers"
)

// Constants for the SIM800L module
const (
	DefaultTimeout = time.Second * 10 // Default timeout for AT commands
	ConnectTimeout = time.Second * 75 // Longer timeout for connection operations
	ResetTime      = time.Second * 3  // Time to hold reset pin high
	StartupTime    = time.Second * 15 // Time to wait after reset
	MaxConnections = 5                // SIM800L supports up to 6 connections (0-5)
	CRLF           = "\r\n"           // Line terminator for commands
	RecvBufSize    = 1024             // Buffer size for receiving data
	OKText         = "OK"             // OK response text
	ErrorText      = "ERROR"          // Error response text
)

// Common error types
var (
	ErrTimeout       = errors.New("AT command timeout")
	ErrError         = errors.New("AT command error")
	ErrNotConnected  = errors.New("not connected to network")
	ErrNoIP          = errors.New("no IP address")
	ErrBadParameter  = errors.New("invalid parameter")
	ErrMaxConn       = errors.New("maximum connections reached")
	ErrUnimplemented = errors.New("operation not implemented")
)

// ConnectionType represents different connection protocols
type ConnectionType uint8

func (ct ConnectionType) String() string {
	switch ct {
	case TCP:
		return "TCP"
	case UDP:
		return "UDP"
	default:
		return "Unknown"
	}
}

const (
	TCP ConnectionType = iota
	UDP
)

// ConnectionState represents the state of a connection
type ConnectionState uint8

const (
	StateInitial ConnectionState = iota
	StateConnecting
	StateConnected
	StateClosing
	StateClosed
	StateError
)

// Connection represents a single connection to a remote server
type Connection struct {
	ID         uint8           // Connection ID (0-5)
	Type       ConnectionType  // Connection type (TCP/UDP)
	State      ConnectionState // Current connection state
	RemoteIP   string          // Remote IP address
	RemotePort string          // Remote port
	LocalPort  uint16          // Local port (if any)
	Device     *Device         // Reference to parent device
}

// Device represents the SIM800L device itself
type Device struct {
	uart        drivers.UART                // UART interface for communication
	resetPin    machine.Pin                 // Pin for hardware reset
	logger      *slog.Logger                // Logger for debug output
	connections [MaxConnections]*Connection // Active connections
	IP          string                      // Current IP address
	buffer      []byte                      // Buffer for UART operations
	powerState  bool                        // Current power state
	IMEI        string                      // Module IMEI
	Operator    string                      // Network operator

	// Receive buffers for each connection
	recvBuffers    [MaxConnections][]byte // Data buffers for received data
	recvBufLengths [MaxConnections]int    // Length of data in each buffer
}

// New creates a new SIM800L device instance
func New(uart drivers.UART, resetPin machine.Pin, logger *slog.Logger) *Device {
	d := &Device{
		uart:     uart,
		resetPin: resetPin,
		logger:   logger,
		buffer:   make([]byte, 256), // Reasonable buffer size for constrained environments
	}
	// Initialize receive buffers for connections
	for i := 0; i < MaxConnections; i++ {
		d.recvBuffers[i] = make([]byte, RecvBufSize)
		d.recvBufLengths[i] = 0
	}

	return d
}

// Init initializes the SIM800L device
func (d *Device) Init() error {
	// Configure pins
	d.resetPin.Configure(machine.PinConfig{Mode: machine.PinOutput})

	// Perform hardware reset
	err := d.HardReset()
	if err != nil {
		return err
	}

	// Initial setup sequence optimized for SIM800L
	commands := []struct {
		cmd     string
		timeout time.Duration
	}{
		{"", DefaultTimeout},        // Basic AT check
		{"E0", DefaultTimeout},      // Disable echo
		{"+CMEE=2", DefaultTimeout}, // Enable verbose error messages
		{"+IPR=0", DefaultTimeout},  // Auto-baud rate
		{"+CFUN=1", DefaultTimeout}, // Full functionality
		{"+CPIN?", DefaultTimeout},  // Check if SIM is ready
		{"+COPS?", DefaultTimeout},  // Get operator info

		{"+CIPMUX=1", DefaultTimeout}, // Enable multi-connection mode
	}

	// Execute initialization sequence
	var resp *Response
	for _, cmd := range commands {
		resp, err = d.send(cmd.cmd, cmd.timeout)
		if err != nil {
			d.logger.Error("init failed on command", "command", cmd.cmd, "error", err)
			return err // For TinyGo, we'll just return the original error
		}

		// Process some responses to extract useful information
		switch {
		case strings.HasPrefix(cmd.cmd, "+COPS?"):
			if resp.Success() && len(resp.Lines) > 0 {
				d.parseOperator(resp.Lines[0])
			}
		case strings.HasPrefix(cmd.cmd, "+CSQ"):
			if resp.Success() && len(resp.Lines) > 0 {
				d.parseSignal(resp.Lines[0])
			}
		}

		// Small delay between commands for stability
		time.Sleep(100 * time.Millisecond)
	}

	// Get IMEI
	resp, err = d.send("+GSN", DefaultTimeout)
	if err == nil && resp.Success() && len(resp.Lines) > 0 {
		d.IMEI = strings.TrimSpace(resp.Lines[0])
	}

	return nil
}

func (d *Device) Signal() int {
	resp, err := d.send("+CSQ", DefaultTimeout)
	if err != nil {
		return 0
	}

	if resp.Success() && len(resp.Lines) > 0 {
		return d.parseSignal(resp.Lines[0])
	}
	return 0
}

// HardReset performs a hardware reset of the SIM800L device
func (d *Device) HardReset() error {
	// Reset sequence
	d.resetPin.High()
	time.Sleep(ResetTime)
	d.resetPin.Low()

	// Wait for device to boot and stabilize
	time.Sleep(StartupTime)

	// Clear UART buffer
	d.clearBuffer()

	// Check if device is responsive
	resp, err := d.send("AT", DefaultTimeout)
	if err != nil || !resp.Success() {
		return errors.New("module not responding after reset")
	}

	return nil
}

// Response represents a parsed AT command response
type Response struct {
	Command       string            // Original command
	Lines         []string          // Response lines excluding status indicators
	Values        map[string]string // Parsed name-value pairs
	Raw           []byte            // Raw response bytes
	Timeout       bool              // Whether a timeout occurred
	WaitOKPattern bool              // Whether to wait for "OK" response
}

// Success returns true if the response is successful (no timeout)
func (r *Response) Success() bool {
	return !r.Timeout
}

// send is a simplified version of sendWithOptions that always waits for OK pattern
func (d *Device) send(cmd string, timeout time.Duration) (*Response, error) {
	return d.sendWithOptions(cmd, true, timeout)
}

// sendWithOptions sends a command with customizable waiting behavior
func (d *Device) sendWithOptions(cmd string, waitForOk bool, timeout time.Duration) (*Response, error) {
	resp := &Response{
		Command:       cmd,
		Values:        make(map[string]string),
		WaitOKPattern: waitForOk,
	}

	// Clear UART buffer before sending
	d.clearBuffer()

	// Add AT prefix if needed
	fullCmd := cmd
	if !strings.HasPrefix(strings.ToUpper(cmd), "AT") {
		fullCmd = "AT" + cmd
	}

	// Add line termination
	fullCmd += CRLF

	// Log the command
	d.logger.Debug("sending command", slog.String("command", fullCmd))

	// Send the command
	_, err := d.uart.Write([]byte(fullCmd))
	if err != nil {
		return resp, err // For TinyGo, we'll just return the original error
	}

	// Read and parse the response
	err = d.readResponse(resp, timeout)
	if err != nil {
		d.logger.Debug("command error", slog.String("command", cmd), slog.String("error", err.Error()))
		return resp, err
	}
	d.logger.Debug("received response", slog.String("command", cmd), slog.Any("response", resp))
	return resp, nil
}

// readResponse reads and parses the device response
func (d *Device) readResponse(resp *Response, timeout time.Duration) error {
	var buffer bytes.Buffer
	deadline := time.Now().Add(timeout)
	tempBuf := make([]byte, 1)
	var rerr error

	for time.Now().Before(deadline) {
		// Check for available data
		if d.uart.Buffered() == 0 {
			time.Sleep(1 * time.Millisecond)
			continue
		}

		// Read a byte
		n, err := d.uart.Read(tempBuf)
		if err != nil || n == 0 {
			continue
		}

		// Add to buffer
		buffer.WriteByte(tempBuf[0])

		// Check for OK or ERROR
		if buffer.Len() >= 2 && tempBuf[0] == '\n' {
			if bytes.Contains(buffer.Bytes(), []byte(ErrorText)) {
				rerr = &ATError{
					Command: resp.Command,
					Message: ErrorText,
				}
				break
			}
			if !resp.WaitOKPattern || bytes.Contains(buffer.Bytes(), []byte(OKText)) {
				break
			}
		}

		// Check for CME/CMS errors (like "+CME ERROR: 10")
		if buffer.Len() > 12 && (bytes.Contains(buffer.Bytes(), []byte("+CME ERROR:")) ||
			bytes.Contains(buffer.Bytes(), []byte("+CMS ERROR:"))) {
			msg := parseErrorMessage(buffer.Bytes())
			rerr = &ATError{
				Command: resp.Command,
				Message: msg,
			}
			break
		}
	}

	// Check for timeout
	if !time.Now().Before(deadline) {
		resp.Timeout = true
		return ErrTimeout
	}

	// Store raw response
	resp.Raw = buffer.Bytes()

	// Parse the response
	d.parseResponse(resp)

	// Return any error found during response reading
	return rerr
}

// parseResponse parses the raw response into structured data
func (d *Device) parseResponse(resp *Response) {
	// Split into lines
	lines := strings.Split(string(resp.Raw), CRLF)

	// Process each line
	for _, line := range lines {
		// Skip empty lines and status indicators
		line = strings.TrimSpace(line)
		if line == "" || line == OKText || line == ErrorText || strings.HasPrefix(line, "AT") {
			continue
		}

		// Add to response lines
		resp.Lines = append(resp.Lines, line)

		// Parse info lines (e.g., +CSQ: 21,0)
		if strings.Contains(line, ":") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])
				resp.Values[key] = value
			}
		}
	}
}

// parseErrorMessage extracts the error message from response containing CME/CMS errors
func parseErrorMessage(data []byte) string {
	s := string(data)
	if idx := strings.Index(s, "+CME ERROR:"); idx >= 0 {
		errMsg := s[idx:]
		if eol := strings.Index(errMsg, "\r\n"); eol > 0 {
			return strings.TrimSpace(errMsg[:eol])
		}
		return strings.TrimSpace(errMsg)
	}
	if idx := strings.Index(s, "+CMS ERROR:"); idx >= 0 {
		errMsg := s[idx:]
		if eol := strings.Index(errMsg, "\r\n"); eol > 0 {
			return strings.TrimSpace(errMsg[:eol])
		}
		return strings.TrimSpace(errMsg)
	}
	return ErrorText
}

// clearBuffer clears any data in the UART buffer
func (d *Device) clearBuffer() {
	// Read all available data
	for d.uart.Buffered() > 0 {
		_, _ = d.uart.Read(d.buffer[:min(len(d.buffer), d.uart.Buffered())])
	}
}

// min returns the smaller of a or b
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// parseOperator extracts network operator information
func (d *Device) parseOperator(line string) {
	// Format: +COPS: 0,0,"Operator",0
	if strings.Contains(line, "\"") {
		start := strings.Index(line, "\"")
		end := strings.LastIndex(line, "\"")
		if start >= 0 && end > start {
			d.Operator = line[start+1 : end]
		}
	}
}

// parseSignal extracts signal strength information
func (d *Device) parseSignal(line string) int {
	// Format: +CSQ: 21,0
	if strings.Contains(line, "+CSQ:") {
		parts := strings.Split(line, ":")
		if len(parts) == 2 {
			signalParts := strings.Split(strings.TrimSpace(parts[1]), ",")
			if len(signalParts) > 0 {
				// Parse signal strength (0-31, 99=unknown)
				signal, err := strconv.Atoi(strings.TrimSpace(signalParts[0]))
				if err == nil {
					return signal
				}
			}
		}
	}
	return 0
}

// Poll checks for pending data and processes it
// Call this method periodically (e.g., in a ticker or main loop)
// to process incoming data from the SIM800L
func (d *Device) Poll() error {
	// Process any UART data (especially RECEIVE notifications)
	if d.uart.Buffered() > 0 {
		// Check for data notifications
		err := d.checkForReceivedData()
		if err != nil {
			d.logger.Debug("error in poll", "error", err)
			return err
		}
	}

	return nil
}

// ATError represents an error returned by an AT command
type ATError struct {
	Command string // The AT command that caused the error
	Message string // The error message
}

// Error returns the error message, implementing the error interface
func (e *ATError) Error() string {
	if e.Message == "" {
		return "AT command error"
	}
	return fmt.Sprintf("AT command error: %s [command: %s]", e.Message, e.Command)
}

// IsErrorType checks if an error is of type ATError and if its message contains a specific string
func IsErrorType(err error, errType string) bool {
	if atErr, ok := err.(*ATError); ok {
		return strings.Contains(atErr.Message, errType)
	}
	return false
}
