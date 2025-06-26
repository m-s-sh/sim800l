// This file contains test code that would normally be included in a testing environment
// but may not be compatible with TinyGo. It's provided as a reference for how to test
// the custom response feature.

// Package sim800l contains implementation for SIM800L module
package sim800l

// NOTE: This test code is provided as a guide for testing in a full Go environment
// It would need modifications to work with TinyGo due to limited testing support
// and package availability in TinyGo.

import (
	"bytes"
	"machine"
	"strings"
	"testing"
)

// MockUART implements a simple mock for testing
type MockUART struct {
	tx           bytes.Buffer
	rx           bytes.Buffer
	baud         uint32
	bufferedData int
	invalidCount int
}

func (m *MockUART) Configure(_ interface{}) error {
	// Simple mock version
	return nil
}

func (m *MockUART) Buffered() int {
	return m.rx.Len()
}

func (m *MockUART) ReadByte() (byte, error) {
	if m.rx.Len() == 0 {
		return 0, nil
	}
	return m.rx.ReadByte()
}

func (m *MockUART) Write(data []byte) (n int, err error) {
	return m.tx.Write(data)
}

func (m *MockUART) Read(data []byte) (n int, err error) {
	return m.rx.Read(data)
}

func (m *MockUART) SetBaudrate(br uint32) error {
	m.baud = br
	return nil
}

// SetupCommandResponse sets up a mock response for a specific command
func (m *MockUART) SetupCommandResponse(response string) {
	m.tx.Reset()
	m.rx.Reset()
	m.rx.WriteString(response)
}

// TestCustomResponseMode tests the custom response handling mode
func TestCustomResponseMode(t *testing.T) {
	// Create a mock UART
	mockUART := &MockUART{}

	// Create a simple logger
	logger := &MockLogger{}

	// Create a device with the mock UART
	device := New(mockUART, MockPin{}, logger)

	// Test cases
	tests := []struct {
		name             string
		command          string
		mockResponse     string
		customMode       bool
		expectedSuccess  bool
		expectedLines    int
		expectedContains string
	}{
		{
			name:             "Standard AT command",
			command:          "AT",
			mockResponse:     "OK\r\n",
			customMode:       false,
			expectedSuccess:  true,
			expectedLines:    0,
			expectedContains: "",
		},
		{
			name:             "Standard command with error",
			command:          "AT+UNKNOWN",
			mockResponse:     "ERROR\r\n",
			customMode:       false,
			expectedSuccess:  false,
			expectedLines:    0,
			expectedContains: "",
		},
		{
			name:             "Get IP custom response",
			command:          "AT+CIFSR",
			mockResponse:     "192.168.1.100\r\n\r\n",
			customMode:       true,
			expectedSuccess:  true,
			expectedLines:    1,
			expectedContains: "192.168.1.100",
		},
		{
			name:             "Complex custom response",
			command:          "AT+CIPSTATUS",
			mockResponse:     "OK\r\n\r\nSTATE: IP STATUS\r\n\r\n+CIPSTATUS: 0,\"TCP\",\"example.com\",80,0,0\r\n\r\n",
			customMode:       true,
			expectedSuccess:  true,
			expectedLines:    2,
			expectedContains: "STATE: IP STATUS",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Setup the mock response
			mockUART.SetupCommandResponse(tc.mockResponse)

			// Send the command with or without custom mode
			var resp *Response
			var err error
			if tc.customMode {
				resp, err = device.sendWithOptions(tc.command, DefaultTimeout, true)
			} else {
				resp, err = device.send(tc.command, DefaultTimeout)
			}

			// Check for errors
			if tc.expectedSuccess {
				if err != nil {
					t.Errorf("Expected success but got error: %v", err)
				}
				if !resp.Success() {
					t.Errorf("Expected response success but got failure: %v", resp.ErrorMsg)
				}
			} else {
				if err == nil && resp.Success() {
					t.Errorf("Expected failure but got success")
				}
			}

			// Check response lines
			if len(resp.Lines) != tc.expectedLines {
				t.Errorf("Expected %d lines, got %d: %v", tc.expectedLines, len(resp.Lines), resp.Lines)
			}

			// Check content if expected
			if tc.expectedContains != "" {
				found := false
				for _, line := range resp.Lines {
					if strings.Contains(line, tc.expectedContains) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected response to contain '%s' but it didn't: %v",
						tc.expectedContains, resp.Lines)
				}
			}
		})
	}
}

// MockLogger is a simple logger for testing
type MockLogger struct{}

// Debug logs at debug level
func (l *MockLogger) Debug(msg string, args ...interface{}) {}

// Info logs at info level
func (l *MockLogger) Info(msg string, args ...interface{}) {}

// Error logs at error level
func (l *MockLogger) Error(msg string, args ...interface{}) {}

// MockPin is a simple pin for testing
type MockPin struct{}

func (p MockPin) Configure(_ interface{}) {}
func (p MockPin) Set(_ bool)              {}
func (p MockPin) Get() bool               { return false }
func (p MockPin) High()                   {}
func (p MockPin) Low()                    {}

// Fake pin for testing
type fakePin struct{}

func (p fakePin) Configure(config machine.PinConfig) {}
func (p fakePin) Set(value bool)                     {}
func (p fakePin) Get() bool                          { return false }
func (p fakePin) High()                              {}
func (p fakePin) Low()                               {}

// Discard writer for logger
type discard struct{}

func (d discard) Write(p []byte) (n int, err error) {
	return len(p), nil
}
