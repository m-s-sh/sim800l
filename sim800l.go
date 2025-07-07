// Package sim800l implements a driver for the SIM800L GSM/GPRS module.
// It is optimized for TinyGo running on constrained environments like the Raspberry Pi Pico.
package sim800l

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// Constants for the SIM800L module
const (
	DefaultTimeout = time.Second * 10 // Default timeout for AT commands
	ConnectTimeout = time.Second * 75 // Longer timeout for connection operations
	ResetTime      = time.Second * 3  // Time to hold reset pin high
	StartupTime    = time.Second * 15 // Time to wait after reset
	MaxBufferSize  = 256              // Maximum buffer size for UART operations
	MaxConnections = 5                // SIM800L supports up to 6 connections (0-5)
	RecvBufSize    = 1024             // Buffer size for receiving data
	OKToken        = "OK"             // OK response text
	ErrorToken     = "ERROR"          // Error response text
)

// Common error types
var (
	ErrError              = errors.New("AT command error")
	ErrTimeout            = errors.New("command timed out")
	ErrUnexpectedResponse = errors.New("unexpected response")
	ErrNoIP               = errors.New("no IP address")
	ErrBadParameter       = errors.New("invalid parameter")
	ErrMaxConn            = errors.New("maximum connections reached")
	ErrUnimplemented      = errors.New("operation not implemented")
	ErrNotReady           = errors.New("device not ready or not responding, after reset")
)

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

// TokenType represents the type of token parsed from AT command responses
type TokenType int

const (
	TokenInvalid TokenType = iota
	TokenLine
	TokenPrompt // > prompt for data input
	TokenEmpty  // Empty line
)

// Device represents the SIM800L device itself
type Device struct {
	uart        UART                        // UART interface for communication
	resetPin    Pin                         // Pin for hardware reset
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

// New creates a new SIM800L device instance.
// For now we accept that resetPin is always configured as output.
func New(uart UART, resetPin Pin, logger *slog.Logger) *Device {
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
		err = d.send(cmd)
		if err != nil {
			d.logger.Error("init failed on command", "command", cmd, "error", err)
			return err // For TinyGo, we'll just return the original error
		}

		// Small delay between commands for stability
		time.Sleep(100 * time.Millisecond)
	}

	// Get IMEI
	err = d.send("+GSN")
	if err == nil {
		d.IMEI = strings.TrimSpace(string(d.buffer[:d.end]))
	}

	return nil
}

func (d *Device) Signal() int {
	err := d.send("+CSQ")
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
	err := d.send("AT")
	if err != nil {
		return ErrNotReady
	}

	return nil
}

// ResponseCheckFunc is a callback function type that can be used to check if
// we have received a complete response and should stop reading
type ResponseCheckFunc func(buffer []byte) error

func defaultResponseCheck(buffer []byte) error {
	// Default response check function that checks for OK or ERROR tokens
	response := string(buffer)
	if strings.Contains(response, OKToken) {
		return nil // OK response
	}
	if strings.Contains(response, ErrorToken) {
		return &ATError{Command: response} // Error response
	}
	return fmt.Errorf("unexpected response: %s", response) // Unexpected response
}

func (d *Device) send(cmd string) error {
	// Use a default response check function that checks for OK or ERROR
	return d.sendWithOptions(cmd, defaultResponseCheck, DefaultTimeout)
}

// send is a simplified version of sendWithOptions that always waits for OK pattern
func (d *Device) sendWithOptions(cmd string, checkFunc ResponseCheckFunc, timeout time.Duration) error {

	if err := d.sendRaw(cmd); err != nil {
		return err
	}

	// Read and parse the response
	if err := d.readResponse(cmd, checkFunc, timeout); err != nil {
		d.logger.Error("command error", slog.Any("command", cmd), "ERROR", err)
		return err
	}

	return nil
}

func (d *Device) sendRaw(cmd string) error {
	// Clear UART buffer before sending
	d.clearBuffer()

	// Add AT prefix if needed
	if !strings.HasPrefix(strings.ToUpper(cmd), "AT") {
		cmd = "AT" + cmd
	}

	// Send the command
	_, err := d.uart.Write([]byte(cmd + "\r\n"))
	if err != nil {
		return &ATError{Command: cmd}
	}
	return nil
}

// readResponse reads and parses the device response
func (d *Device) readResponse(cmd string, checkFunc ResponseCheckFunc, timeout time.Duration) error {
	// Reset the raw length counter and clear the buffer
	t, err := d.readLine(timeout)
	if err != nil {
		return err
	}
	if t != TokenLine {
		return &ATError{Command: cmd}
	}
	if checkFunc != nil {
		return checkFunc(d.buffer[:d.end])
	}
	return nil // No custom check function provided, return nil
}

// parseErrorMessage extracts the error message from response containing CME/CMS errors
func parseErrorMessage(data []byte) string {
	s := string(data)
	idx := strings.Index(s, ":")
	if idx < 0 {
		return s // No colon found, return the whole string
	}
	// Extract the part after the colon
	s = strings.TrimSpace(s[idx+1:])
	return s
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

	start += len(k) + 1 // Move to the start of the value (after ":")
	if start >= d.end {
		return "", false // No value found after the key
	}

	// Extract the value
	value = strings.TrimSpace(string(d.buffer[start:d.end]))
	if value == "" {
		return "", false
	}
	return value, true
}

func (d *Device) readLine(t time.Duration) (TokenType, error) {
	deadline := time.Now().Add(t)
	d.end = 0 // Reset the end index of the buffer

	var b [1]byte // single-byte read buffer
	const (
		stateStart   = 0
		stateEndLine = 1
	)
	state := stateStart

	for time.Now().Before(deadline) {
		if d.uart.Buffered() == 0 {
			time.Sleep(1 * time.Millisecond)
			continue
		}

		n, err := d.uart.Read(b[:]) // directly read one byte
		if err != nil {
			break // or handle errors like io.EOF
		}

		if n == 0 {
			time.Sleep(10 * time.Millisecond) // avoid busy waiting
			continue                          // no data read, skip
		}

		switch state {
		case stateStart:
			if b[0] == '\r' {
				state = stateEndLine
				continue
			}
			if b[0] == '>' {
				return TokenPrompt, nil // special prompt character
			}
			if err := d.append(b[0]); err != nil {
				return TokenInvalid, err
			}
		case stateEndLine:
			if b[0] == '\n' {
				// Escape empty lines
				if d.end <= 2 {
					state = stateStart // reset state for next line
					continue
				}
				return TokenLine, nil
			} else {
				d.end = 0 // Reset buffer if we receive a character after \r
				// If we receive a character after \r, treat it as normal data
				if err := d.append(b[0]); err != nil {
					return TokenInvalid, err
				}
				state = stateStart // reset state for next line
			}
		}
	}

	// Check for timeout or buffer overflow
	if !time.Now().Before(deadline) {
		return TokenInvalid, ErrTimeout
	}

	return TokenInvalid, nil // no complete line found
}

func (d *Device) append(b byte) error {
	if d.end >= len(d.buffer) {
		return errors.New("buffer overflow") // or handle as needed
	}

	d.buffer[d.end] = b
	d.end++
	return nil
}
