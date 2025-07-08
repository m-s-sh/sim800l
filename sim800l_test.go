// This file contains test code that would normally be included in a testing environment
// but may not be compatible with TinyGo. It's provided as a reference for how to test
// the custom response feature.

// Package sim800l contains implementation for SIM800L module
package sim800l

import (
	"bytes"
	"log/slog"
	"testing"
	"time"

	"github.com/m-s-sh/mockhw"
)

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

func Test_readResponse(t *testing.T) {
	tests := []struct {
		name            string
		responseData    []byte
		expectCommand   string
		expectValue     string
		shouldLogBuffer bool
	}{
		{
			name:            "CGATT response",
			responseData:    []byte("\r+CGATT: 1\r\nOK\r\n"),
			expectCommand:   "+CGATT",
			expectValue:     "1",
			shouldLogBuffer: true,
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
			uart := mockhw.NewUART(1000) // 1 second max delay
			uart.SetRxBuffer(tc.responseData)
			d := Device{
				uart:   uart,
				logger: slog.New(&MockHandler{t: t}),
			}

			err := d.readResponse([]byte(tc.expectCommand), nil, time.Minute)
			if err != nil {
				t.Fatalf("Failed to read response: %v", err)
			}

			value, ok := d.parseValue([]byte(tc.expectCommand))
			if !ok || !bytes.Equal(value, []byte(tc.expectValue)) {
				t.Errorf("Expected value '%s', got '%s'", tc.expectValue, value)
			}

			if tc.shouldLogBuffer {
				t.Logf("Buffer content: %s, value: %s", string(d.buffer[:d.end]), value)
			}
		})
	}
}

func Test_sendRaw(t *testing.T) {
	uart := mockhw.NewUART(1000) // 1 second max delay
	d := Device{
		uart:   uart,
		logger: slog.New(&MockHandler{t: t}),
	}

	data := []byte("+AABB")
	err := d.sendRaw(data)
	if err != nil {
		t.Fatalf("Failed to send raw bytes: %v", err)
	}

	data = append([]byte("AT"), data...)
	data = append(data, []byte("\r\n")...)
	// Verify that the data was sent correctly
	sentData := uart.TxBuffer()
	if string(sentData) != string(data) {
		t.Errorf("Expected sent data '%s', got '%s'", string(data), string(sentData))
	}
}
