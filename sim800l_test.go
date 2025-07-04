// This file contains test code that would normally be included in a testing environment
// but may not be compatible with TinyGo. It's provided as a reference for how to test
// the custom response feature.

// Package sim800l contains implementation for SIM800L module
package sim800l

import (
	"bytes"
	"errors"
	"log/slog"
	"testing"
)

// MockUART implements drivers.UART for testing
type MockUART struct {
	data        *bytes.Buffer
	returnData  *bytes.Buffer
	bufferCount int
}

func (m *MockUART) Buffered() int {
	// If we have a specific buffer count set, use that
	if m.bufferCount > 0 {
		return m.bufferCount
	}
	return m.returnData.Len()
}

func (m *MockUART) ReadByte() (byte, error) {
	if m.returnData.Len() == 0 {
		return 0, errors.New("no data")
	}
	return m.returnData.ReadByte()
}

func (m *MockUART) Write(data []byte) (n int, err error) {
	return m.data.Write(data)
}

func (m *MockUART) Read(data []byte) (n int, err error) {
	return m.returnData.Read(data)
}

func (m *MockUART) Flush() error {
	return nil
}

func (m *MockUART) SetBaudRate(br uint32) error {
	return nil
}

// MockPin implements machine.Pin for testing
type MockPin struct {
	state bool
}

func (p *MockPin) Configure(config interface{}) {
}

func (p *MockPin) High() {
	p.state = true
}

func (p *MockPin) Low() {
	p.state = false
}

func (p *MockPin) Get() bool {
	return p.state
}

func (p *MockPin) Set(value bool) {
	p.state = value
}

func TestReadResponse(t *testing.T) {
	tests := []struct {
		name            string
		responseData    []byte
		expectCommand   string
		expectValue     string
		shouldLogBuffer bool
	}{
		{
			name:          "CGATT response",
			responseData:  []byte("\r+CGATT: 1\r\nOK\r\n"),
			expectCommand: "+CGATT",
			expectValue:   "1",
		},
		{
			name:            "COPS response",
			responseData:    []byte("\r\n+COPS: 0,0,\"28403\"\r\n\r\nOK\r\n"),
			expectCommand:   "+COPS",
			expectValue:     "0,0,\"28403\"",
			shouldLogBuffer: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			buf := bytes.NewBuffer(tc.responseData)
			d := Device{
				uart: &MockUART{
					returnData: buf,
				},
				logger: slog.New(&MockHandler{t: t}),
			}

			err := d.readResponse(tc.expectCommand, nil, DefaultTimeout)
			if err != nil {
				t.Fatalf("Failed to read response: %v", err)
			}

			if tc.shouldLogBuffer {
				t.Logf("Buffer content: %s", string(d.buffer[:d.end]))
			}

			value, ok := d.parseValue(tc.expectCommand)
			if !ok || value != tc.expectValue {
				t.Errorf("Expected value '%s', got '%s'", tc.expectValue, value)
			}
		})
	}
}
