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

// GPRS connection states
const (
	GPRSDetached = 0
	GPRSAttached = 1
)

// TCP/IP connection states from SIM800L
const (
	IPInitial     = "IP INITIAL"
	IPStart       = "IP START"
	IPConfig      = "IP CONFIG"
	IPGPRSAct     = "IP GPRSACT"
	IPStatus      = "IP STATUS"
	TCPConnecting = "TCP CONNECTING"
	UDPConnecting = "UDP CONNECTING"
	ServerListen  = "SERVER LISTENING"
	ConnectOK     = "CONNECT OK"
	ConnectFail   = "CONNECT FAIL"
	TCPClosing    = "TCP CLOSING"
	UDPClosing    = "UDP CLOSING"
	TCPClosed     = "TCP CLOSED"
	UDPClosed     = "UDP CLOSED"
	PDPDeact      = "PDP DEACT"
)

// Connect establishes a GPRS connection with the specified APN
// If user and password are empty, they will not be included
func (d *Device) Connect(apn, user, password string) error {

	// Check if module is attached to GPRS service
	resp, err := d.send("+CGATT?", DefaultTimeout)
	if err != nil {
		return fmt.Errorf("failed to check GPRS attachment: %w", err)
	}

	// Parse attachment status
	attached := false
	if val, ok := resp.Values["+CGATT"]; ok {
		if val == "1" {
			attached = true
		}
	}

	// If not attached, attach to GPRS service
	if !attached {
		d.logger.Info("not attached to GPRS, attaching now...")
		_, err = d.send("+CGATT=1", ConnectTimeout)
		if err != nil {
			d.logger.Error("failed to attach to GPRS", "error", err)
			return fmt.Errorf("failed to attach to GPRS: %w", err)
		}
	}

	// Enable multi-connection mode
	_, err = d.send("+CIPMUX=1", DefaultTimeout)
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

	_, err = d.send(cmd, DefaultTimeout)
	if err != nil {
		return fmt.Errorf("failed to set APN: %w", err)
	}

	// Start wireless connection
	_, err = d.send("+CIICR", ConnectTimeout)
	if err != nil {
		return fmt.Errorf("failed to bring up wireless connection: %w", err)
	}

	// Get local IP address - use custom mode that doesn't expect OK response
	resp, err = d.sendWithOptions("+CIFSR", false, DefaultTimeout)
	if err != nil {
		return fmt.Errorf("failed to get IP address: %w", err)
	}

	// Parse IP address response
	if len(resp.Lines) > 0 {
		// The response should contain the IP address directly
		ip := strings.TrimSpace(resp.Lines[0])
		if net.ParseIP(ip) != nil {
			d.IP = ip
			d.logger.Debug("got address", "ip", d.IP)
		} else {
			d.logger.Error("invalid IP address received", "ip", ip)
			return errors.New("invalid IP address received")
		}
	} else {
		return errors.New("no IP address received")
	}

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
	_, err := d.send("+CIPSHUT", DefaultTimeout)
	if err != nil {
		return fmt.Errorf("failed to shut down PDP context: %w", err)
	}

	// Detach from GPRS service
	_, err = d.send("+CGATT=0", DefaultTimeout)
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
	connID := -1
	for i := 0; i < MaxConnections; i++ {
		if d.connections[i] == nil {
			connID = i
			break
		}
	}

	if connID == -1 {
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
		ID:         uint8(connID),
		Type:       connType,
		State:      StateConnecting,
		RemoteIP:   host,
		RemotePort: port,
		Device:     d,
	}

	// Store the connection
	d.connections[connID] = conn

	// Start connection
	networkType := "TCP"
	if connType == UDP {
		networkType = "UDP"
	}

	cmd := fmt.Sprintf("+CIPSTART=%d,\"%s\",\"%s\",\"%s\"",
		connID, networkType, host, port)

	resp, err := d.send(cmd, ConnectTimeout)
	if err != nil {
		d.connections[connID] = nil
		return nil, fmt.Errorf("connection failed: %w", err)
	}

	// Look for CONNECT OK in the response
	if !resp.Success() || (!contains(resp.Raw, []byte("CONNECT OK")) &&
		!contains(resp.Raw, []byte("ALREADY CONNECT"))) {
		d.connections[connID] = nil
		return nil, fmt.Errorf("connection failed: %s", string(resp.Raw))
	}

	// Connection successful
	conn.State = StateConnected

	return conn, nil
}

// CloseConnection closes a specific connection by ID
func (d *Device) CloseConnection(id uint8) error {
	if id >= MaxConnections || d.connections[id] == nil {
		return fmt.Errorf("invalid connection ID: %d", id)
	}

	conn := d.connections[id]
	conn.State = StateClosing

	// Send close command
	cmd := fmt.Sprintf("+CIPCLOSE=%d", id)
	_, err := d.send(cmd, DefaultTimeout)

	// Even if there was an error, mark the connection as closed
	d.connections[id] = nil

	if err != nil {
		return fmt.Errorf("failed to close connection %d: %w", id, err)
	}

	return nil
}

// GetConnectionStatus returns the status of all connections
func (d *Device) GetConnectionStatus() error {
	// CIPSTATUS returns STATE: info and multiple +CIPSTATUS lines that we need to parse
	resp, err := d.sendWithOptions("+CIPSTATUS", false, DefaultTimeout)
	if err != nil {
		return err
	}

	// Parse connection status
	for _, line := range resp.Lines {
		if strings.HasPrefix(line, "+CIPSTATUS:") {
			parts := strings.Split(line[11:], ",")
			if len(parts) >= 4 {
				// Parse connection ID
				id, err := strconv.Atoi(strings.TrimSpace(parts[0]))
				if err == nil && id >= 0 && id < MaxConnections {
					// If we have this connection, update its state
					if d.connections[id] != nil {
						switch strings.Trim(parts[1], "\"") {
						case "TCP", "UDP":
							d.connections[id].State = StateConnected
						case "CLOSED":
							d.connections[id].State = StateClosed
							d.connections[id] = nil
						}
					}
				}
			}
		}
	}

	return nil
}

// SendData sends data through a connection
func (d *Device) SendData(id uint8, data []byte) (int, error) {
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
		resp, err := d.send(cmd, DefaultTimeout)
		if err != nil {
			return totalSent, err
		}

		// Look for ">" prompt
		if !contains(resp.Raw, []byte(">")) {
			return totalSent, errors.New("data prompt not received")
		}

		// Send data
		_, err = d.uart.Write(data[offset : offset+size])
		if err != nil {
			return totalSent, fmt.Errorf("failed to send data: %w", err)
		}

		// Wait for SEND OK response
		deadline := time.Now().Add(DefaultTimeout)
		for time.Now().Before(deadline) {
			if d.uart.Buffered() > 0 {
				buf := make([]byte, 256)
				n, _ := d.uart.Read(buf)
				if n > 0 && contains(buf[:n], []byte("SEND OK")) {
					totalSent += size
					break
				}
				if n > 0 && contains(buf[:n], []byte("SEND FAIL")) {
					return totalSent, errors.New("send failed")
				}
			}
			time.Sleep(10 * time.Millisecond)
		}

		if !time.Now().Before(deadline) {
			return totalSent, ErrTimeout
		}

		// Small delay between chunks
		time.Sleep(100 * time.Millisecond)
	}

	return totalSent, nil
}

// contains checks if a is contained in b
func contains(b, a []byte) bool {
	return len(a) <= len(b) && len(a) > 0 && len(b) > 0 &&
		bytes.Contains(b, a) // This relies on the bytes package imported in sim800l.go
}

// CheckNetworkRegistration verifies the network registration status
func (d *Device) CheckNetworkRegistration() (bool, error) {
	resp, err := d.send("+CREG?", DefaultTimeout)
	if err != nil {
		return false, err
	}

	// Parse registration status
	// +CREG: 0,1 means registered to home network
	// +CREG: 0,5 means registered to roaming network
	if val, ok := resp.Values["+CREG"]; ok {
		parts := strings.Split(val, ",")
		if len(parts) >= 2 {
			status := strings.TrimSpace(parts[1])
			return status == "1" || status == "5", nil
		}
	}

	return false, errors.New("could not parse registration status")
}

// ConnectionRead implements reading data from a specific connection
// Used internally by the Connection's Read method
func (d *Device) ConnectionRead(id uint8, b []byte) (int, error) {
	// Check if the connection exists
	if id >= MaxConnections || d.connections[id] == nil {
		return 0, errors.New("invalid connection")
	}

	// Check if there's data available in the buffer
	if d.recvBufLengths[id] == 0 {
		// Try to check for new data from the device
		err := d.checkForReceivedData()
		if err != nil {
			// Non-blocking, just log the error
			d.logger.Debug("Error checking for data", "error", err)
		}

		// If still no data, return would-block error
		if d.recvBufLengths[id] == 0 {
			return 0, errors.New("would block")
		}
	}

	// Copy data from receive buffer to the provided buffer
	n := copy(b, d.recvBuffers[id][:d.recvBufLengths[id]])

	// If we read all data, reset the buffer
	if n >= d.recvBufLengths[id] {
		d.recvBufLengths[id] = 0
	} else {
		// Otherwise, shift remaining data to the beginning of the buffer
		copy(d.recvBuffers[id], d.recvBuffers[id][n:d.recvBufLengths[id]])
		d.recvBufLengths[id] -= n
	}

	return n, nil
}

// checkForReceivedData checks for any new data received on any connection
// This should be called periodically to process pending data notifications
func (d *Device) checkForReceivedData() error {
	// If no data is available, return immediately
	if d.uart.Buffered() == 0 {
		return nil
	}

	// Check for data notifications in the UART buffer
	tempBuf := make([]byte, 256)
	n, err := d.uart.Read(tempBuf)
	if err != nil || n == 0 {
		return err
	}

	// Process the data looking for +RECEIVE notifications
	// Format: +RECEIVE,<id>,<length>:<data>
	data := string(tempBuf[:n])
	if strings.Contains(data, "+RECEIVE") {
		d.logger.Debug("got RECEIVE notification", "data", data)

		// Parse the notification to extract connection ID, length, and data
		parts := strings.Split(data, "+RECEIVE,")
		for _, part := range parts[1:] { // Skip the first part (before first +RECEIVE)
			// Extract connection ID and length
			idLenParts := strings.Split(part, ",")
			if len(idLenParts) < 2 {
				continue
			}

			// Parse connection ID
			id, err := strconv.Atoi(strings.TrimSpace(idLenParts[0]))
			if err != nil || id < 0 || id >= MaxConnections || d.connections[id] == nil {
				continue
			}

			// Parse data length and extract data
			lenDataParts := strings.SplitN(idLenParts[1], ":", 2)
			if len(lenDataParts) < 2 {
				continue
			}

			dataLength, err := strconv.Atoi(strings.TrimSpace(lenDataParts[0]))
			if err != nil || dataLength <= 0 {
				continue
			}

			// Get the actual data
			receivedData := []byte(lenDataParts[1])
			if len(receivedData) > dataLength {
				receivedData = receivedData[:dataLength]
			}

			// Add to receive buffer
			availSpace := len(d.recvBuffers[id]) - d.recvBufLengths[id]
			if availSpace <= 0 {
				// Buffer full, log warning
				d.logger.Warn("receive buffer full, dropping data", "connID", id)
				continue
			}

			copyLen := min(len(receivedData), availSpace)
			copy(d.recvBuffers[id][d.recvBufLengths[id]:], receivedData[:copyLen])
			d.recvBufLengths[id] += copyLen

			d.logger.Debug("data added to buffer", "connID", id, "bytes", copyLen)
		}
	}

	return nil
}

// GetIP sends AT+CIFSR command and returns the current IP address
// This command uses the custom response handling mode since CIFSR returns
// the IP directly without a standard OK response
func (d *Device) GetIP() (string, error) {
	// Use custom response mode (false) since CIFSR doesn't return OK
	resp, err := d.sendWithOptions("+CIFSR", false, DefaultTimeout)
	if err != nil {
		return "", fmt.Errorf("failed to get IP address: %w", err)
	}

	// Parse the response for an IP address
	if len(resp.Lines) > 0 {
		for _, line := range resp.Lines {
			ip := strings.TrimSpace(line)
			if net.ParseIP(ip) != nil {
				d.IP = ip // Cache the IP in the device
				return ip, nil
			}
		}
	}

	return "", errors.New("no valid IP address in response")
}

// GetGPRSStatus returns the current GPRS connection state string
// (e.g., "IP STATUS", "TCP CONNECTING", etc.)
func (d *Device) GetGPRSStatus() (string, error) {
	// Use custom response mode for CIPSTATUS for proper parsing
	resp, err := d.sendWithOptions("+CIPSTATUS", false, DefaultTimeout)
	if err != nil {
		return "", err
	}

	// Look for STATE: line in the response
	for _, line := range resp.Lines {
		if strings.HasPrefix(line, "STATE:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1]), nil
			}
		}
	}

	return "", errors.New("state information not found in response")
}
