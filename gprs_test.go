package sim800l

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
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
	d := Device{
		uart: &MockUART{
			returnData: bytes.NewBuffer([]byte("+RECEIVE,0,4:\r\n\x02\x00\x00\r\n")),
		},
		logger:      slog.New(&MockHandler{t: t}),
		connections: [5]*Connection{},
	}
	d.connections[0] = &Connection{
		ID:         0,
		Device:     &d,
		State:      StateConnected,
		RemoteIP:   "",
		RemotePort: "",
	}
	err := d.checkForReceivedData()

	if err != nil {
		t.Error("failed to check for received data:", err)
	}
}
