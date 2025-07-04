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
	buffer      [256]byte                   // Fixed buffer for UART operations
	end         int                         // Current end index in the buffer
	powerState  bool                        // Current power state
	IMEI        string                      // Module IMEI
	Operator    string                      // Network operator

	// Receive buffers for each connection (fixed size arrays)
	recvBuffers    [MaxConnections][RecvBufSize]byte // Data buffers for received data
	recvBufLengths [MaxConnections]int               // Length of data in each buffer
}

// New creates a new SIM800L device instance
func New(uart drivers.UART, resetPin machine.Pin, logger *slog.Logger) *Device {
	d := &Device{
		uart:     uart,
		resetPin: resetPin,
		logger:   logger,
	}

	// Initialize connection state
	for i := 0; i < MaxConnections; i++ {
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
	commands := []string{
		"AT",        // Basic AT check
		"E0",        // Disable echo
		"+CMEE=2",   // Enable verbose error messages
		"+IPR=0",    // Auto-baud rate
		"+CFUN=1",   // Full functionality
		"+CPIN?",    // Check if SIM is ready
		"+COPS?",    // Get operator info
		"+CIPMUX=1", // Enable multi-connection mode
	}

	// Execute initialization sequence

	for _, cmd := range commands {
		err = d.send(cmd, DefaultTimeout)
		if err != nil {
			d.logger.Error("init failed on command", "command", cmd, "error", err)
			return err // For TinyGo, we'll just return the original error
		}

		// Small delay between commands for stability
		time.Sleep(100 * time.Millisecond)
	}

	// Get IMEI
	err = d.send("+GSN", DefaultTimeout)
	if err == nil {
		d.IMEI = strings.TrimSpace(string(d.buffer[:d.end]))
	}

	return nil
}

func (d *Device) Signal() int {
	err := d.send("+CSQ", DefaultTimeout)
	if err != nil {
		return 0
	}
	return d.parseSignal(string(d.buffer[:d.end]))
}

// HardReset performs a hardware reset of the SIM800L device
func (d *Device) HardReset() error {
	// Reset sequence
	d.resetPin.High()
	time.Sleep(ResetTime)
	d.resetPin.Low()

	// Wait for device to boot and stabilize
	time.Sleep(StartupTime)

	// Check if device is responsive
	err := d.send("AT", DefaultTimeout)
	if err != nil {
		return errors.New("module not responding after reset")
	}

	return nil
}

// ResponseCheckFunc is a callback function type that can be used to check if
// we have received a complete response and should stop reading
type ResponseCheckFunc func(buffer []byte) bool

// send is a simplified version of sendWithOptions that always waits for OK pattern
func (d *Device) send(cmd string, timeout time.Duration) error {
	return d.sendWithOptions(cmd, nil, timeout)
}

// sendWithOptions sends a command with customizable waiting behavior
func (d *Device) sendWithOptions(cmd string, checkFunc ResponseCheckFunc, timeout time.Duration) error {

	// Clear UART buffer before sending
	d.clearBuffer()

	// Add AT prefix if needed
	if !strings.HasPrefix(strings.ToUpper(cmd), "AT") {
		cmd = "AT" + cmd
	}

	// Log the command
	//d.logger.Debug("sending command", slog.String("command", fullCmd))

	// Send the command
	_, err := d.uart.Write([]byte(cmd + CRLF))
	if err != nil {
		return err // For TinyGo, we'll just return the original error
	}

	// Read and parse the response
	err = d.readResponse(cmd, checkFunc, timeout)
	if err != nil {
		d.logger.Debug("command error", slog.Any("command", cmd), "ERROR", err)
		return err
	}

	return nil
}

// readResponse reads and parses the device response
func (d *Device) readResponse(cmd string, checkResponse ResponseCheckFunc, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	var rerr error
	// Reset the raw length counter and clear the buffer
	d.end = 0

	for time.Now().Before(deadline) {
		// Check for available data
		if d.uart.Buffered() == 0 {
			time.Sleep(1 * time.Millisecond)
			continue
		}

		// Read a byte
		n, err := d.uart.Read(d.buffer[d.end:])
		if err != nil && n == 0 {
			continue
		}
		d.end += n
		b := d.buffer[:d.end]
		if checkResponse != nil && checkResponse(b) {
			// Custom check function is satisfied
			break
		} else if checkResponse == nil {
			// Check for OK or ERROR responses
			if bytes.Contains(b, []byte(ErrorText)) {
				rerr = &ATError{
					Command: cmd,
					//Message: ErrorText,
				}
				break
			}
			if bytes.Contains(b, []byte(OKText)) {
				break
			}

		}

		//Check for CME/CMS errors (like "+CME ERROR: 10")
		if d.end > 12 && (bytes.Contains(b, []byte("+CME ERROR:")) ||
			bytes.Contains(b, []byte("+CMS ERROR:"))) {
			msg := parseErrorMessage(b)
			rerr = &ATError{
				Command: cmd,
			}
			d.logger.Error("command error",
				slog.String("command", cmd),
				slog.String("error", msg),
			)
			break
		}
	}

	// Check for timeout or buffer overflow
	if !time.Now().Before(deadline) {
		return ErrTimeout
	}

	// Return any error found during response reading
	return rerr
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
	// Format: +COPS: 0,0,"Operator"
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

func (d *Device) parseValue(k string) (value string, ok bool) {
	// Find the key in the buffer
	start := strings.Index(string(d.buffer[:d.end]), k+":")
	if start < 0 {
		return "", false
	}
	end := strings.Index(string(d.buffer[start:d.end]), "\r\n")
	if end < 0 {
		end = d.end
	} else {
		end += start // Adjust end index relative to the start
	}
	start += len(k) + 1 // Move past the key and the colon
	if start >= end {
		return "", false
	}
	// Extract the value
	value = strings.TrimSpace(string(d.buffer[start:end]))
	if value == "" {
		return "", false
	}
	return value, true
}

// ATError represents an error returned by an AT command
type ATError struct {
	Command string // The AT command that caused the error
}

// Error returns the error message, implementing the error interface
func (e *ATError) Error() string {
	if len(e.Command) == 0 {
		return "AT command error"
	}
	return fmt.Sprintf("%s command error", e.Command)
}
