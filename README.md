# SIM800L Driver for TinyGo

This package implements a driver for the SIM800L GSM/GPRS module optimized for TinyGo.

## Features

- Full AT command support
- GPRS connection management
- Multiple connection handling (up to 6 simultaneous connections)
- IP address management
- Non-blocking reads with poll/check mechanism
- Custom response handling for special AT commands

## Custom Response Handling

Some AT commands like `AT+CIFSR` (get IP address) don't return standard `OK`/`ERROR` responses. 
Instead, they return the result directly. This driver handles these special cases using a custom response mode.

### Example Usage

```go
// Get the device IP address using custom response handling
ip, err := device.GetIP()
if err != nil {
    logger.Error("Failed to get IP address", "error", err)
} else {
    logger.Info("Device IP address", "ip", ip)
}

// Check GPRS status with custom response handling
status, err := device.GetGPRSStatus()
if err != nil {
    logger.Error("Failed to get GPRS status", "error", err)
} else {
    logger.Info("GPRS status", "status", status)
}
```

### Low-level API for Custom Responses

For advanced users who need to send custom AT commands with special response handling:

```go
// Use sendWithOptions to enable custom response mode
// This tells the parser to collect all response data and not expect OK/ERROR
resp, err := device.sendWithOptions("+CIFSR", DefaultTimeout, true)
if err != nil {
    return err
}

// The response lines contain the actual data (like IP address)
for _, line := range resp.Lines {
    // Process the response data
    fmt.Println("Response line:", line)
}
```

## Connection Management

The driver supports up to 6 simultaneous connections (0-5) in multi-connection mode.
Each connection gets its own receive buffer.

```go
// Creating a new TCP connection
connID, err := device.TCPConnect("example.com", "80")
if err != nil {
    // Handle error
}

// Send data
err = device.Send(connID, []byte("GET / HTTP/1.0\r\n\r\n"))
if err != nil {
    // Handle error
}

// Poll for available data
for {
    // Check if data is available
    available, err := device.ConnectionAvailable(connID)
    if err != nil {
        break
    }
    
    if available > 0 {
        // Read data
        buffer := make([]byte, available)
        n, err := device.ConnectionRead(connID, buffer)
        if err != nil {
            break
        }
        
        // Process data
        fmt.Printf("Received %d bytes: %s\n", n, buffer[:n])
    }
    
    // Small delay
    time.Sleep(100 * time.Millisecond)
}

// Close connection when done
device.CloseConnection(connID)
```

## Handling Special Responses

The SIM800L sometimes returns responses that don't follow the standard AT command pattern.
For example, when receiving data from a connection, the module will send:

```
+RECEIVE,0,128:
[128 bytes of data]
```

The driver handles these special cases with appropriate parsing and buffering.
