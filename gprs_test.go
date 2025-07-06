package sim800l

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/m-s-sh/mockhw"
)

type MockHandler struct {
	t *testing.T
}

func (h *MockHandler) Enabled(_ context.Context, level slog.Level) bool {
	switch level {
	case slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError:
		return true
	default:
		return false
	}
}

func (h *MockHandler) Handle(ctx context.Context, r slog.Record) error {
	h.t.Helper()

	// Extract attributes and format them as key:value pairs
	var attrs []string
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, a.Key+":"+a.Value.String())
		return true
	})

	// Format message with attributes
	msg := r.Message
	if len(attrs) > 0 {
		msg += " " + bytes.NewBuffer([]byte(attrs[0])).String()
		for _, attr := range attrs[1:] {
			msg += ", " + bytes.NewBuffer([]byte(attr)).String()
		}
	}

	if r.Level >= slog.LevelError {
		h.t.Errorf("Log level %v: %s", r.Level, msg)
	} else {
		h.t.Logf("Log level %v: %s", r.Level, msg)
	}
	return nil
}

func (h *MockHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// For simplicity, we ignore attributes in this mock handler
	return h
}
func (h *MockHandler) WithGroup(name string) slog.Handler {
	// For simplicity, we ignore groups in this mock handler
	return h
}

// Add any other logger methods that your interface requires

func TestCheckForReceivedData(t *testing.T) {
	tests := []struct {
		name           string
		inputData      []byte
		connectionID   uint8
		expectedLength int
		expectError    bool
		setupBuffers   bool
	}{
		{
			name:           "Basic data receive",
			inputData:      []byte("+RECEIVE,0,4:\r\n\x02\x00\x00\r"),
			connectionID:   0,
			expectedLength: 4,
			expectError:    false,
			setupBuffers:   true,
		},
		{
			name:           "Two-part receive notification",
			inputData:      []byte("+RECEIVE,1,10:\r\nHelloWorld"),
			connectionID:   1,
			expectedLength: 10,
			expectError:    false,
			setupBuffers:   true,
		},
		{
			name:           "Begin receive notification",
			inputData:      []byte("\r\n\r\n+RECEIVE,1,300:\r\nHTTP/1.1 400 Bad Request\r\nDate: Mon, 30 Jun 2025 15:23:54 GMT\r\nDate: Mon, 30 Jun 2025 15:23:54 GMT\r\nContent-Type: text/html\r\nContent-Length: 154\r\nConnection: close\r\nServer: tcpbin\r\n\r\n<html>\r\n<head><title>400 Bad Request</title></head>\r\n<body>\r\n<center><h1>400 Bad Request</h1></center>\r\n<hr><center>openresty</center>\r\n</body>\r\n</html>\r\n"),
			connectionID:   1,
			expectedLength: 300,
			expectError:    false,
			setupBuffers:   true,
		},
		{
			name:         "Invalid connection ID",
			inputData:    []byte("+RECEIVE,7,5:\r\ntest\r\n"),
			connectionID: 7, // Invalid ID (out of range)
			expectError:  true,
			setupBuffers: false,
		},
		{
			name:         "No connection at ID",
			inputData:    []byte("+RECEIVE,2,5:\r\ntest\r\n"),
			connectionID: 2, // Valid ID but no connection setup
			expectError:  true,
			setupBuffers: false,
		},
		{
			name:           "Large data packet",
			inputData:      append([]byte("+RECEIVE,0,128:\r\n"), bytes.Repeat([]byte("X"), 128)...),
			connectionID:   0,
			expectedLength: 128,
			expectError:    false,
			setupBuffers:   true,
		},
	}

	for _, tc := range tests {
		uart := mockhw.NewUART(1000) // 1 second max delay
		uart.SetRxBuffer(tc.inputData)
		t.Run(tc.name, func(t *testing.T) {
			d := Device{
				uart:           uart,
				logger:         slog.New(&MockHandler{t: t}),
				connections:    [MaxConnections]*Connection{},
				recvBuffers:    [MaxConnections][1024]byte{},
				recvBufLengths: [MaxConnections]int{},
			}

			// Setup connections as needed for the test
			if tc.setupBuffers && tc.connectionID < MaxConnections {
				d.connections[tc.connectionID] = &Connection{
					ID:         tc.connectionID,
					Device:     &d,
					State:      StateConnected,
					RemoteIP:   "test.example.com",
					RemotePort: "80",
				}
			}

			err := d.checkForReceivedData(DefaultTimeout)

			if tc.expectError {
				if err == nil {
					t.Error("expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("failed to check for received data: %v", err)
				}

				// Verify data was stored correctly in the receive buffer
				if tc.setupBuffers && tc.connectionID < MaxConnections {
					if d.recvBufLengths[tc.connectionID] != tc.expectedLength {
						t.Errorf("expected buffer length %d, got %d",
							tc.expectedLength, d.recvBufLengths[tc.connectionID])
					}

					// You could add more checks here for the actual content
					// For example, verify the first few bytes match expected data
					if tc.expectedLength > 0 && d.recvBufLengths[tc.connectionID] > 0 {
						t.Logf("Received data (first few bytes): %v",
							d.recvBuffers[tc.connectionID][:min(5, d.recvBufLengths[tc.connectionID])])
					}
				}
			}
		})
	}
}
