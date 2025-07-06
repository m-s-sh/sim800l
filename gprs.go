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
	ErrWouldBlock = errors.New("would block")
)

// Connect establishes a GPRS connection with the specified APN
// If user and password are empty, they will not be included
func (d *Device) Connect(apn, user, password string) error {

	// Check if module is attached to GPRS service
	err := d.send("+CGATT?", DefaultTimeout)
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
		err = d.send("+CGATT=1", ConnectTimeout)
		if err != nil {
			d.logger.Error("failed to attach to GPRS", "error", err)
			return fmt.Errorf("failed to attach to GPRS: %w", err)
		}
	}

	// Enable multi-connection mode
	err = d.send("+CIPMUX=1", DefaultTimeout)
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

	err = d.send(cmd, DefaultTimeout)
	if err != nil {
		return fmt.Errorf("failed to set APN: %w", err)
	}

	// Start wireless connection
	err = d.send("+CIICR", ConnectTimeout)
	if err != nil {
		return fmt.Errorf("failed to bring up wireless connection: %w", err)
	}

	// Get local IP address - use custom mode that doesn't expect OK response
	err = d.sendWithOptions("+CIFSR", func(buffer []byte) bool {
		// Custom check function to look for valid IP address
		return buffer[len(buffer)-1] == '\n' && bytes.Contains(buffer, []byte("."))
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
	err := d.send("+CIPSHUT", DefaultTimeout)
	if err != nil {
		return fmt.Errorf("failed to shut down PDP context: %w", err)
	}

	// Detach from GPRS service
	err = d.send("+CGATT=0", DefaultTimeout)
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

	// Start connection
	networkType := "TCP"
	if connType == UDP {
		networkType = "UDP"
	}

	cmd := fmt.Sprintf("+CIPSTART=%d,\"%s\",\"%s\",\"%s\"",
		connID, networkType, host, port)

	err = d.send(cmd, DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to start connection: %w", err)
	}

	if err := d.readResponse("", func(buffer []byte) bool {
		// Custom check function to look for CONNECT OK or ALREADY CONNECT
		return bytes.Contains(buffer, []byte("CONNECT OK")) ||
			bytes.Contains(buffer, []byte("CONNECT FAIL")) ||
			bytes.Contains(buffer, []byte("ALREADY CONNECT"))
	}, ConnectTimeout); err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}

	if bytes.Contains(d.buffer[:], []byte("CONNECT FAIL")) {
		return nil, fmt.Errorf("connection failed")
	}

	// Connection successful
	conn.State = StateConnected
	d.connections[connID] = conn
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
	err := d.send(cmd, DefaultTimeout)

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
	err := d.sendWithOptions("+CIPSTATUS", func(buffer []byte) bool {
		// Custom check function to look for +CIPSTATUS lines
		return bytes.Contains(buffer, []byte("+CIPSTATUS:"))
	}, DefaultTimeout)
	if err != nil {
		return err
	}

	// Parse connection status
	// for _, line := range resp.Lines {
	// 	if strings.HasPrefix(line, "+CIPSTATUS:") {
	// 		parts := strings.Split(line[11:], ",")
	// 		if len(parts) >= 4 {
	// 			// Parse connection ID
	// 			id, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	// 			if err == nil && id >= 0 && id < MaxConnections {""
	// 				// If we have this connection, update its state
	// 				if d.connections[id] != nil {
	// 					switch strings.Trim(parts[1], "\"") {
	// 					case "TCP", "UDP":
	// 						d.connections[id].State = StateConnected
	// 					case "CLOSED":
	// 						d.connections[id].State = StateClosed
	// 						d.connections[id] = nil
	// 					}
	// 				}
	// 			}
	// 		}
	// 	}
	// }

	return nil
}

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
		err := d.sendWithOptions(cmd, func(buffer []byte) bool {
			return bytes.Contains(buffer, []byte(">"))
		}, DefaultTimeout)
		if err != nil {
			return totalSent, fmt.Errorf("failed to send data: %w", err)
		}

		// Send data
		_, err = d.uart.Write(data[offset : offset+size])
		if err != nil {
			return totalSent, fmt.Errorf("failed to send data: %w", err)
		}

		// Wait for SEND OK response
		if err := d.readResponse("", func(buffer []byte) bool {
			// Custom check function to look for SEND OK or SEND FAIL
			return bytes.Contains(buffer, []byte("SEND OK")) ||
				bytes.Contains(buffer, []byte("SEND FAIL"))
		}, DefaultTimeout); err != nil {
			return totalSent, fmt.Errorf("failed to read response: %w", err)
		}

		totalSent += size
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
// func (d *Device) CheckNetworkRegistration() (bool, error) {
// 	err := d.send("+CREG?", DefaultTimeout)
// 	if err != nil {
// 		return false, err
// 	}

// 	// Parse registration status
// 	// +CREG: 0,1 means registered to home network
// 	// +CREG: 0,5 means registered to roaming network
// 	if val, ok := resp.Values["+CREG"]; ok {
// 		parts := strings.Split(val, ",")
// 		if len(parts) >= 2 {
// 			status := strings.TrimSpace(parts[1])
// 			return status == "1" || status == "5", nil
// 		}
// 	}

// 	return false, errors.New("could not parse registration status")
// }

// connectionRead implements reading data from a specific connection
// Used internally by the Connection's Read method

func (d *Device) connectionRead(id uint8, b []byte) (int, error) {
	// Check if the connection exists
	if id >= MaxConnections || d.connections[id] == nil {
		return 0, ErrInvalidConnection
	}

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
	startTime := time.Now()
	var buffer [256]byte // Fixed buffer size
	end := 0

	// State machine variables
	state := 0      // 0=looking for +RECEIVE, 1=reading data
	cid := -1       // Current connection ID
	dataLength := 0 // Expected data length

	for time.Since(startTime) < timeout {
		// Read available data into buffer
		n, err := d.uart.Read(buffer[end:])
		if err != nil {
			return err
		}

		if n == 0 {
			time.Sleep(10 * time.Millisecond)
			continue
		}

		end += n

		// Handle buffer overflow
		if end >= len(buffer) {
			d.logger.Warn("buffer overflow, resetting")
			end = 0
			state = 0
			continue
		}

		// Process buffer based on current state
		switch state {
		case 0: // Looking for +RECEIVE notification
			// Try to find +RECEIVE
			if idx := bytes.Index(buffer[:end], []byte("+RECEIVE")); idx >= 0 {
				// Find colon after +RECEIVE
				if colonIdx := bytes.Index(buffer[idx:end], []byte(":")); colonIdx >= 0 {
					colonIdx += idx // Adjust to full buffer position

					// Extract connection ID and length
					paramStr := string(buffer[idx+9 : colonIdx]) // +RECEIVE, = 9 chars
					params := strings.Split(paramStr, ",")
					if len(params) >= 2 {
						var parseErr error
						cid, parseErr = strconv.Atoi(strings.TrimSpace(params[0]))
						if parseErr != nil || cid < 0 || cid >= MaxConnections ||
							d.connections[cid] == nil {
							d.logger.Debug("invalid connection ID", "id", params[0])
							// Reset buffer and continue looking
							copy(buffer[:], buffer[colonIdx+1:end])
							end -= (colonIdx + 1)
							continue
						}

						dataLength, parseErr = strconv.Atoi(strings.TrimSpace(params[1]))
						if parseErr != nil {
							// Reset buffer and continue looking
							copy(buffer[:], buffer[colonIdx+1:end])
							end -= (colonIdx + 1)
							continue
						}

						// Find the data start (after \r\n)
						if crlfIdx := bytes.Index(buffer[colonIdx:end], []byte("\r\n")); crlfIdx >= 0 {
							dataStartIdx := colonIdx + crlfIdx + 2 // +2 for \r\n

							// Check how much data we already have
							dataAvailable := end - dataStartIdx
							dataToProcess := min(dataAvailable, dataLength)

							// Copy available data to connection buffer
							if dataToProcess > 0 {
								// Check for buffer space
								if d.recvBufLengths[cid]+dataToProcess > len(d.recvBuffers[cid]) {
									// Not enough space, copy what fits
									spaceLeft := len(d.recvBuffers[cid]) - d.recvBufLengths[cid]
									if spaceLeft > 0 {
										copy(d.recvBuffers[cid][d.recvBufLengths[cid]:],
											buffer[dataStartIdx:dataStartIdx+spaceLeft])
										d.recvBufLengths[cid] += spaceLeft
									}
									d.logger.Warn("receive buffer overflow",
										"connection", cid,
										"data_lost", dataToProcess-spaceLeft)
								} else {
									// Enough space, copy all data
									copy(d.recvBuffers[cid][d.recvBufLengths[cid]:],
										buffer[dataStartIdx:dataStartIdx+dataToProcess])
									d.recvBufLengths[cid] += dataToProcess
								}
							}

							// If we got all data, return success
							if d.recvBufLengths[cid] >= dataLength {
								return nil
							}

							// Otherwise, prepare for reading more data
							state = 1
							end = 0
						}
					}
				}
			}

		case 1: // Reading data directly
			// How much more data we need
			remaining := dataLength - d.recvBufLengths[cid]
			if remaining <= 0 {
				return nil // We have all the data
			}

			// Process available data
			dataToRead := min(end, remaining)

			// Check for buffer space
			if d.recvBufLengths[cid]+dataToRead > len(d.recvBuffers[cid]) {
				// Not enough space, copy what fits
				spaceLeft := len(d.recvBuffers[cid]) - d.recvBufLengths[cid]
				if spaceLeft > 0 {
					copy(d.recvBuffers[cid][d.recvBufLengths[cid]:],
						buffer[:spaceLeft])
					d.recvBufLengths[cid] += spaceLeft
				}
				d.logger.Warn("receive buffer overflow",
					"connection", cid,
					"data_lost", dataToRead-spaceLeft)
			} else {
				// Enough space, copy all data
				copy(d.recvBuffers[cid][d.recvBufLengths[cid]:],
					buffer[:dataToRead])
				d.recvBufLengths[cid] += dataToRead
			}

			// Move any remaining data to start of buffer
			remaining = end - dataToRead
			if remaining > 0 {
				copy(buffer[:], buffer[dataToRead:end])
				end = remaining
			} else {
				end = 0
			}

			// Check if we have all the data now
			if d.recvBufLengths[cid] >= dataLength {
				return nil
			}
		}
	}

	return ErrTimeout
}
