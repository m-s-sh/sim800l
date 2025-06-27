// This file contains test code that would normally be included in a testing environment
// but may not be compatible with TinyGo. It's provided as a reference for how to test
// the custom response feature.

// Package sim800l contains implementation for SIM800L module
package sim800l

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// MockUART implements drivers.UART for testing
type MockUART struct {
	data        bytes.Buffer
	returnData  bytes.Buffer
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

// TestSuccess tests the Success method of Response
func TestSuccess(t *testing.T) {
	// Test success case
	respSuccess := &Response{
		Timeout: false,
	}
	if !respSuccess.Success() {
		t.Error("Expected Success() to return true for non-timeout response")
	}

	// Test failure case
	respFailure := &Response{
		Timeout: true,
	}
	if respFailure.Success() {
		t.Error("Expected Success() to return false for timeout response")
	}
}

// TestParseResponse tests the parseResponse method
func TestParseResponse(t *testing.T) {
	testCases := []struct {
		name     string
		rawData  string
		expected []string
		values   map[string]string
	}{
		{
			name:     "Empty response",
			rawData:  "",
			expected: nil,
			values:   map[string]string{},
		},
		{
			name:     "Simple OK response",
			rawData:  "OK\r\n",
			expected: nil,
			values:   map[string]string{},
		},
		{
			name:     "Single line response",
			rawData:  "Test line\r\nOK\r\n",
			expected: []string{"Test line"},
			values:   map[string]string{},
		},
		{
			name:     "Value response",
			rawData:  "+CSQ: 21,0\r\nOK\r\n",
			expected: []string{"+CSQ: 21,0"},
			values:   map[string]string{"+CSQ": "21,0"},
		},
		{
			name:     "Multi-line response",
			rawData:  "Line 1\r\n+KEY: VALUE\r\nLine 3\r\nOK\r\n",
			expected: []string{"Line 1", "+KEY: VALUE", "Line 3"},
			values:   map[string]string{"+KEY": "VALUE"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			uart := &MockUART{}

			logger := slog.Default()
			device := Device{
				uart:   uart,
				logger: logger,
			}

			resp := &Response{
				Raw:    []byte(tc.rawData),
				Values: make(map[string]string),
			}

			device.parseResponse(resp)

			// Check lines
			if len(resp.Lines) != len(tc.expected) {
				t.Errorf("Expected %d lines, got %d", len(tc.expected), len(resp.Lines))
			} else {
				for i, line := range tc.expected {
					if i < len(resp.Lines) && resp.Lines[i] != line {
						t.Errorf("Line %d: expected '%s', got '%s'", i, line, resp.Lines[i])
					}
				}
			}

			// Check values
			if len(resp.Values) != len(tc.values) {
				t.Errorf("Expected %d values, got %d", len(tc.values), len(resp.Values))
			} else {
				for k, v := range tc.values {
					if val, ok := resp.Values[k]; !ok || val != v {
						t.Errorf("Value for key '%s': expected '%s', got '%s'", k, v, val)
					}
				}
			}
		})
	}
}

// TestReadResponse tests the readResponse method
func TestReadResponse(t *testing.T) {
	testCases := []struct {
		name          string
		inputData     string
		checkResponse func([]byte) bool
		expectError   bool
		errorType     error
		timeout       time.Duration
	}{
		// {
		// 	name:        "Simple OK response",
		// 	inputData:   "OK\r\n",
		// 	waitForOK:   true,
		// 	expectError: false,
		// 	timeout:     time.Second,
		// },
		// {
		// 	name:        "Error response",
		// 	inputData:   "ERROR\r\n",
		// 	waitForOK:   true,
		// 	expectError: true,
		// 	errorType:   &ATError{},
		// 	timeout:     time.Second,
		// },
		// {
		// 	name:        "CME Error response",
		// 	inputData:   "+CME ERROR: 10\r\n",
		// 	waitForOK:   true,
		// 	expectError: true,
		// 	errorType:   &ATError{},
		// 	timeout:     time.Second,
		// },
		{
			name:      "Non-OK response with waitForOK=false",
			inputData: "\r\n192.168.1.1\r\n",
			checkResponse: func(buffer []byte) bool {
				return buffer[len(buffer)-1] == '\n' && bytes.Contains(buffer, []byte("."))
			},
			expectError: false,
			timeout:     20 * time.Second,
		},
		// {
		// 	name:        "Multiple line response with OK",
		// 	inputData:   "+CSQ: 21,0\r\nSome data\r\nOK\r\n",
		// 	waitForOK:   true,
		// 	expectError: false,
		// 	timeout:     time.Second,
		// },
		// {
		// 	name:        "Empty response",
		// 	inputData:   "",
		// 	waitForOK:   true,
		// 	expectError: true, // Should timeout with no response
		// 	errorType:   ErrTimeout,
		// 	timeout:     10 * time.Millisecond, // Short timeout for test
		// },
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create mock UART with the test data
			uart := &MockUART{}

			// Prepare data in the mock UART's return buffer
			uart.returnData.Write([]byte(tc.inputData))

			// Create a device with the mock UART
			logger := slog.Default()
			device := Device{
				uart:   uart,
				logger: logger,
			}

			// Create response object
			resp := &Response{
				Command:       "TEST",
				Values:        make(map[string]string),
				checkResponse: tc.checkResponse, // No custom check for this test
			}

			// Call the method to test
			err := device.readResponse(resp, tc.timeout)

			// Check error expectations
			if tc.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tc.expectError && err != nil {
				t.Errorf("Did not expect error but got: %v", err)
			}

			// Check specific error type if applicable
			if tc.errorType != nil && err != nil {
				switch expectedErr := tc.errorType.(type) {
				case *ATError:
					if _, ok := err.(*ATError); !ok {
						t.Errorf("Expected error of type *ATError but got %T", err)
					}
				default:
					if err != expectedErr {
						t.Errorf("Expected error %v but got %v", expectedErr, err)
					}
				}
			}

			// Check that the response's Raw field contains the input data
			if tc.inputData != "" && !tc.expectError {
				t.Log("Input Data:", tc.inputData[:len(tc.inputData)-2])
				if !bytes.Contains(resp.Raw, []byte(strings.Trim(tc.inputData, "\r\n"))) { // -2 to remove trailing \r\n
					t.Errorf("Response raw data does not contain expected input: %v", resp.Raw)
				}
			}

			// Check timeout flag
			if tc.errorType == ErrTimeout && !resp.Timeout {
				t.Error("Expected Timeout flag to be set, but it wasn't")
			}
		})
	}
}

// TestReadResponseByteByByte tests the readResponse method with byte-by-byte reading
func TestReadResponseByteByByte(t *testing.T) {
	// This test simulates data coming in byte by byte with delays
	uart := &MockUART{}
	logger := slog.Default()
	device := Device{
		uart:   uart,
		logger: logger,
	}

	// We'll use a custom test function to simulate byte-by-byte input
	testByteByByte := func(input string, waitForOK bool, expectError bool) {
		// Reset UART's buffers
		uart.data.Reset()
		uart.returnData.Reset()

		// Setup response object
		resp := &Response{
			Command: "TEST",
			Values:  make(map[string]string),
		}

		// Force buffer count to be 1 so we read one byte at a time
		uart.bufferCount = 1

		// Start a goroutine to feed data byte by byte
		done := make(chan struct{})
		go func() {
			defer close(done)

			// Write each byte with a small delay
			for _, b := range []byte(input) {
				uart.returnData.WriteByte(b)
				time.Sleep(1 * time.Millisecond)
			}
		}()

		// Call the method to test
		err := device.readResponse(resp, 100*time.Millisecond)

		// Check error expectations
		if expectError && err == nil {
			t.Errorf("Expected error but got none for input: %q", input)
		}
		if !expectError && err != nil {
			t.Errorf("Did not expect error but got: %v for input: %q", err, input)
		}

		// Wait for goroutine to finish
		<-done
	}

	// Test cases
	testByteByByte("OK\r\n", true, false)
	testByteByByte("ERROR\r\n", true, true)
	testByteByByte("+CME ERROR: 10\r\n", true, true)
	testByteByByte("192.168.1.1\r\n", false, false) // Non-OK with waitForOK=false
}
