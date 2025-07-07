// Package sim800l implements a driver for the SIM800L GSM/GPRS module.
// This file contains GPRS connection management functionality.
package sim800l

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

var (
	ErrWouldBlock    = errors.New("would block")
	ErrCannotSend    = errors.New("cannot send data")
	ErrCannotConnect = errors.New("cannot connect to remote host")
)

// Connect establishes a GPRS connection with the specified APN
// If user and password are empty, they will not be included
func (d *Device) Connect(apn, user, password string) error {

	// Check if module is attached to GPRS service
	err := d.send("+CGATT?")
	if err != nil {
		return fmt.Errorf("failed to check GPRS attachment: %w", err)
	}

	// Parse attachment status
	attached := false
	if val, ok := d.parseValue("+CGATT"); ok {
		if val == "1" {
			attached = true
		}
	}

	// If not attached, attach to GPRS service
	if !attached {
		d.logger.Info("not attached to GPRS, attaching now...")
		err = d.send("+CGATT=1")
		if err != nil {
			d.logger.Error("failed to attach to GPRS", "error", err)
			return fmt.Errorf("failed to attach to GPRS: %w", err)
		}
	}

	// Enable multi-connection mode
	err = d.send("+CIPMUX=1")
	if err != nil {
		return fmt.Errorf("failed to enable multi-connection: %w", err)
	}

	// Start wireless connection with specified APN
	var cmd string
	if user != "" && password != "" {
		cmd = fmt.Sprintf("+CSTT=\"%s\",\"%s\",\"%s\"", apn, user, password)
	} else {
		cmd = fmt.Sprintf("+CSTT=\"%s\"", apn)
	}

	err = d.send(cmd)
	if err != nil {
		return fmt.Errorf("failed to set APN: %w", err)
	}

	// Start wireless connection
	err = d.send("+CIICR")
	if err != nil {
		return fmt.Errorf("failed to bring up wireless connection: %w", err)
	}

	// Get local IP address - use custom mode that doesn't expect OK response
	err = d.sendWithOptions("+CIFSR", func(buffer []byte) error {
		// Custom check function to look for valid IP address
		if buffer[len(buffer)-1] != '\n' {
			return fmt.Errorf("invalid response format")
		}
		if !bytes.Contains(buffer, []byte(".")) {
			return fmt.Errorf("no valid IP address found")
		}
		return nil
	}, DefaultTimeout)
	if err != nil {
		return fmt.Errorf("failed to get IP address: %w", err)
	}

	// Parse IP address response - check all lines for valid IP
	ip := strings.TrimSpace(string(d.buffer[:d.end]))
	if net.ParseIP(ip) == nil {
		d.logger.Error("invalid IP address in all response lines")
	}
	d.IP = ip
	return nil
}

// Disconnect closes the GPRS connection
func (d *Device) Disconnect() error {
	// Close all active connections first
	for i := 0; i < MaxConnections; i++ {
		if d.connections[i] != nil {
			_ = d.CloseConnection(uint8(i))
		}
	}

	// Shut down PDP context
	err := d.send("+CIPSHUT")
	if err != nil {
		return fmt.Errorf("failed to shut down PDP context: %w", err)
	}

	// Detach from GPRS service
	err = d.send("+CGATT=0")
	if err != nil {
		return fmt.Errorf("failed to detach from GPRS: %w", err)
	}

	// Clear IP address
	d.IP = ""

	return nil
}

// Dial establishes a connection to the remote host
// Returns a Connection object that implements the net.Conn interface
func (d *Device) Dial(network, address string) (net.Conn, error) {
	// Check if we're connected to GPRS
	if d.IP == "" {
		return nil, ErrNoIP
	}

	// Find available connection slot
	cid := -1
	for i := 0; i < MaxConnections; i++ {
		if d.connections[i] == nil {
			cid = i
			break
		}
	}

	if cid == -1 {
		return nil, ErrMaxConn
	}

	// Parse network type
	var connType ConnectionType
	switch strings.ToLower(network) {
	case "tcp":
		connType = TCP
	case "udp":
		connType = UDP
	default:
		return nil, fmt.Errorf("unsupported network type: %s", network)
	}

	// Parse address (host:port)
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid address format: %w", err)
	}

	// Create connection object
	conn := &Connection{
		ID:         uint8(cid),
		Type:       connType,
		State:      StateConnecting,
		RemoteIP:   host,
		RemotePort: port,
		Device:     d,
	}

	// Store the connection

	// Start connection
	networkType := "TCP"
	if connType == UDP {
		networkType = "UDP"
	}

	cmd := fmt.Sprintf("+CIPSTART=%d,\"%s\",\"%s\",\"%s\"",
		cid, networkType, host, port)

	err = d.send(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to start connection: %w", err)
	}

	if err := d.readResponse("+CIPSTART", func(buffer []byte) error {
		// Custom check function to look for CONNECT OK or ALREADY CONNECT
		if bytes.Contains(buffer, []byte("CONNECT OK")) {
			return nil
		}
		if bytes.Contains(buffer, []byte("CONNECT FAIL")) {
			return ErrCannotConnect
		}
		if bytes.Contains(buffer, []byte("ALREADY CONNECT")) {
			return nil
		}
		return ErrUnexpectedResponse
	}, ConnectTimeout); err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}

	// Connection successful
	conn.State = StateConnected
	d.connections[cid] = conn
	return conn, nil
}

// CloseConnection closes a specific connection by ID
func (d *Device) CloseConnection(cid uint8) error {
	if cid >= MaxConnections || d.connections[cid] == nil {
		return fmt.Errorf("invalid connection ID: %d", cid)
	}

	conn := d.connections[cid]
	conn.State = StateClosing

	// Send close command
	cmd := fmt.Sprintf("+CIPCLOSE=%d", cid)
	err := d.send(cmd)

	// Even if there was an error, mark the connection as closed
	d.connections[cid] = nil

	if err != nil {
		return fmt.Errorf("failed to close connection %d: %w", cid, err)
	}

	return nil
}

// // GetConnectionStatus returns the status of all connections
// func (d *Device) GetConnectionStatus() error {
// 	// CIPSTATUS returns STATE: info and multiple +CIPSTATUS lines that we need to parse
// 	err := d.sendWithOptions("+CIPSTATUS", func(buffer []byte) bool {
// 		// Custom check function to look for +CIPSTATUS lines
// 		return bytes.Contains(buffer, []byte("+CIPSTATUS:"))
// 	}, DefaultTimeout)
// 	if err != nil {
// 		return err
// 	}

// 	// Parse connection status
// 	// for _, line := range resp.Lines {
// 	// 	if strings.HasPrefix(line, "+CIPSTATUS:") {
// 	// 		parts := strings.Split(line[11:], ",")
// 	// 		if len(parts) >= 4 {
// 	// 			// Parse connection ID
// 	// 			id, err := strconv.Atoi(strings.TrimSpace(parts[0]))
// 	// 			if err == nil && id >= 0 && id < MaxConnections {""
// 	// 				// If we have this connection, update its state
// 	// 				if d.connections[id] != nil {
// 	// 					switch strings.Trim(parts[1], "\"") {
// 	// 					case "TCP", "UDP":
// 	// 						d.connections[id].State = StateConnected
// 	// 					case "CLOSED":
// 	// 						d.connections[id].State = StateClosed
// 	// 						d.connections[id] = nil
// 	// 					}
// 	// 				}
// 	// 			}
// 	// 		}
// 	// 	}
// 	// }

// 	return nil
// }

// connectionSend sends data through a connection
func (d *Device) connectionSend(id uint8, data []byte) (int, error) {
	if id >= MaxConnections || d.connections[id] == nil {
		return 0, fmt.Errorf("invalid connection ID: %d", id)
	}

	if len(data) == 0 {
		return 0, nil
	}

	// Maximum size for a single send
	const maxChunk = 1024

	// Send data in chunks if needed
	totalSent := 0
	for offset := 0; offset < len(data); offset += maxChunk {
		// Calculate chunk size
		size := len(data) - offset
		if size > maxChunk {
			size = maxChunk
		}

		// Send command to prepare for data
		cmd := fmt.Sprintf("+CIPSEND=%d,%d", id, size)
		if err := d.sendRaw(cmd); err != nil {
			return totalSent, err
		}

		t, err := d.readLine(DefaultTimeout)
		if err != nil {
			return totalSent, fmt.Errorf("failed to read prompt: %w", err)
		}
		if t != TokenPrompt {
			return totalSent, ErrUnexpectedResponse
		}
		// Send data
		_, err = d.uart.Write(data[offset : offset+size])
		if err != nil {
			return totalSent, fmt.Errorf("failed to send data: %w", err)
		}

		// Wait for SEND OK response
		if err := d.readResponse("", func(buffer []byte) error {
			// Custom check function to look for SEND OK or SEND FAIL
			if bytes.Contains(buffer, []byte("SEND OK")) {
				return nil
			}
			if bytes.Contains(buffer, []byte("SEND FAIL")) {
				return ErrCannotSend
			}
			return ErrUnexpectedResponse
		}, DefaultTimeout); err != nil {
			return totalSent, err
		}

		totalSent += size
		// Small delay between chunks
		time.Sleep(100 * time.Millisecond)
	}
	return totalSent, nil
}

// connectionRead implements reading data from a specific connection
// Used internally by the Connection's Read method
func (d *Device) connectionRead(id uint8, b []byte) (int, error) {
	// Check if there's data available in the buffer
	if d.recvBufLengths[id] == 0 {
		// Try to check for new data from the device
		err := d.checkForReceivedData(DefaultTimeout)
		if err != nil && err != ErrTimeout {
			// Non-blocking, just log the error
			d.logger.Debug("error checking for data", "error", err)
		}

		// If still no data, return would-block error
		if d.recvBufLengths[id] == 0 {
			return 0, ErrWouldBlock
		}
	}

	// Copy data from receive buffer to the provided buffer
	n := copy(b, d.recvBuffers[id][:d.recvBufLengths[id]])

	// If we read all data, reset the buffer
	if n >= d.recvBufLengths[id] {
		d.recvBufLengths[id] = 0
	} else {
		// Otherwise, shift remaining data to the beginning of the buffer
		copy(d.recvBuffers[id][:], d.recvBuffers[id][n:d.recvBufLengths[id]])
		d.recvBufLengths[id] -= n
	}

	return n, nil
}

// checkForReceivedData checks for any new data received on any connection
// This should be called periodically to process pending data notifications
func (d *Device) checkForReceivedData(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	// State machine variables
	state := 0      // 0=looking for +RECEIVE, 1=reading data
	cid := -1       // Current connection ID
	dataLength := 0 // Expected data length

	for time.Since(deadline) < 0 {
		switch state {
		case 0: // Looking for +RECEIVE notification
			// Try to find +RECEIVE

			t, err := d.readLine(DefaultTimeout)
			if err != nil {
				return err
			}
			if t != TokenLine {
				return fmt.Errorf("unexpected token type: %v", t)
			}
			line := string(d.buffer[:d.end])
			parts := strings.SplitN(line, ",", 3)
			if len(parts) < 3 || !strings.HasPrefix(parts[0], "+RECEIVE") {
				return fmt.Errorf("invalid +RECEIVE format: %s", line)
			}

			// Parse connection ID
			cid, err = strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil || cid < 0 || cid >= MaxConnections {
				return fmt.Errorf("invalid connection ID in +RECEIVE: %s", parts[1])
			}
			// Parse data length
			end := strings.Index(parts[2], ":")
			if end < 0 {
				return fmt.Errorf("invalid +RECEIVE format, missing data length: %s", parts[2])
			}
			dataLength, err = strconv.Atoi(parts[2][:end]) // Remove trailing :
			// Check if data length is valid
			if err != nil || dataLength <= 0 {
				return fmt.Errorf("invalid data length in +RECEIVE: %s", parts[2])
			}
			if dataLength > MaxBufferSize {
				return fmt.Errorf("data length exceeds maximum buffer size: %d", dataLength)
			}
			state = 1 // Move to reading data state
			// Reset the receive buffer for this connection
			d.recvBufLengths[cid] = 0

		case 1: // Reading data directly
			// Read to to the expected data length
			n, err := d.uart.Read(d.buffer[:])
			if err != nil {
				return fmt.Errorf("failed to read data for connection %d: %w", cid, err)
			}
			n = min(n, dataLength)
			// copy the data to the receive buffer and if are not done read one more time
			if n > 0 {
				copy(d.recvBuffers[cid][d.recvBufLengths[cid]:], d.buffer[:n])
				d.recvBufLengths[cid] += n
				dataLength -= n
			}
			// Check if we have read enough data
			if dataLength <= 0 {
				return nil // Successfully read all expected data
			}
		}
	}
	return ErrTimeout
}
