// Package sim800l implements a driver for the SIM800L GSM/GPRS module.
// This file contains the net.Conn implementation for Connection objects.
package sim800l

import (
	"errors"
	"io"
	"net"
	"time"
)

var (
	ErrInvalidConnection        = errors.New("invalid connection")
	ErrConnectionNotEstablished = errors.New("connection not established")
	ErrConnectionClosed         = errors.New("connection closed")
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

// Connection represents a single connection to a remote server
// Connection already defined in sim800l.go

// Read reads data from the connection
// Implements the net.Conn interface
func (c *Connection) Read(b []byte) (int, error) {
	// Check if connection is valid
	if c == nil || c.Device == nil {
		return 0, ErrInvalidConnection
	}

	// Check connection state
	if c.State != StateConnected {
		return 0, io.EOF
	}

	// Use the module's connection read implementation
	return c.Device.connectionRead(c.ID, b)
}

// Write writes data to the connection
// Implements the net.Conn interface
func (c *Connection) Write(b []byte) (int, error) {
	// Check if connection is valid
	if c == nil || c.Device == nil {
		return 0, ErrInvalidConnection
	}

	// Check connection state
	if c.State != StateConnected {
		return 0, ErrConnectionNotEstablished
	}

	// Use the module's SendData function
	return c.Device.connectionSend(c.ID, b)
}

// Close closes the connection
// Implements the net.Conn interface
func (c *Connection) Close() error {
	// Check if connection is valid
	if c == nil || c.Device == nil {
		return ErrInvalidConnection
	}

	// Use the module's CloseConnection function
	return c.Device.CloseConnection(c.ID)
}

// LocalAddr returns the local network address
// Implements the net.Conn interface
func (c *Connection) LocalAddr() net.Addr {
	if c == nil || c.Device == nil || c.Device.IP == "" {
		return nil
	}

	// Create a simple implementation of net.Addr
	return simpleAddr{
		network: c.networkString(),
		address: c.Device.IP,
	}
}

// RemoteAddr returns the remote network address
// Implements the net.Conn interface
func (c *Connection) RemoteAddr() net.Addr {
	if c == nil || c.RemoteIP == "" {
		return nil
	}

	// Create a simple implementation of net.Addr
	return simpleAddr{
		network: c.networkString(),
		address: c.RemoteIP + ":" + c.RemotePort,
	}
}

// SetDeadline sets the read and write deadlines
// Note: This implementation is a placeholder, as the SIM800L doesn't support precise deadlines
func (c *Connection) SetDeadline(t time.Time) error {
	// Not fully implemented due to SIM800L limitations
	return nil
}

// SetReadDeadline sets the read deadline
// Note: This implementation is a placeholder, as the SIM800L doesn't support precise deadlines
func (c *Connection) SetReadDeadline(t time.Time) error {
	// Not fully implemented due to SIM800L limitations
	return nil
}

// SetWriteDeadline sets the write deadline
// Note: This implementation is a placeholder, as the SIM800L doesn't support precise deadlines
func (c *Connection) SetWriteDeadline(t time.Time) error {
	// Not fully implemented due to SIM800L limitations
	return nil
}

// networkString returns the network type as a string
func (c *Connection) networkString() string {
	if c.Type == TCP {
		return "tcp"
	}
	return "udp"
}

// simpleAddr implements the net.Addr interface
type simpleAddr struct {
	network string
	address string
}

func (a simpleAddr) Network() string {
	return a.network
}

func (a simpleAddr) String() string {
	return a.address
}

// IsConnected returns true if the connection is active
func (c *Connection) IsConnected() bool {
	return c != nil && c.State == StateConnected
}

// GetState returns the current connection state as a string
func (c *Connection) GetState() string {
	if c == nil {
		return "INVALID"
	}

	switch c.State {
	case StateInitial:
		return "INITIAL"
	case StateConnecting:
		return "CONNECTING"
	case StateConnected:
		return "CONNECTED"
	case StateClosing:
		return "CLOSING"
	case StateClosed:
		return "CLOSED"
	case StateError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}
