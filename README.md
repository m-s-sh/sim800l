# SIM800L Driver for TinyGo

This package implements a driver for the SIM800L GSM/GPRS module optimized for TinyGo running on constrained environments like the Raspberry Pi Pico.

## Comments
It is experiment in it's roots, of using CoPilot, so will be interesting to see the result. Not all code and text is will fully tested.  
_(this paragraph was written by human, about the rest not 100% sure)._

## Features

- Full AT command support
- GPRS connection management with APN support
- Multiple connection handling (up to 5 simultaneous connections, IDs 0-4)
- Standard net.Conn interface implementation
- Non-blocking reads with buffering
- Hardware reset support
- Detailed logging with slog
- Custom response handling for special AT commands

## Basic Usage

```go
// Configure UART for the SIM800L module
err := machine.UART1.Configure(machine.UARTConfig{
    TX:       GPRSTX,
    RX:       GPRSRX,
    BaudRate: 9600,
})

// Create a new SIM800L device with reset pin and logger
device := sim800l.New(machine.UART1, machine.GPIO0, logger)

// Initialize the device (performs hardware reset)
if err := device.Init(); err != nil {
    logger.Error("failed to initialize SIM800L device", "error", err)
    return
}

// Connect to the GPRS network with APN credentials
if err := device.Connect("your-apn", "username", "password"); err != nil {
    logger.Error("failed to connect to network", "error", err)
    return
}

// Create a TCP connection (implements net.Conn interface)
conn, err := device.Dial("tcp", "example.com:80")
if err != nil {
    logger.Error("failed to connect", "error", err)
    return
}
defer conn.Close()

// Send HTTP request
conn.Write([]byte("GET / HTTP/1.0\r\n\r\n"))

// Read response
buffer := make([]byte, 1024)
n, err := conn.Read(buffer)
if err != nil {
    logger.Error("read error", "error", err)
} else {
    logger.Info("response", "data", string(buffer[:n]))
}
```

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
// Method 1: Use sendWithOptions with waitForOK=false
// This collects all response data without expecting OK/ERROR
resp, err := device.sendWithOptions("+CIFSR", false, DefaultTimeout, nil)
if err != nil {
    return err
}

// Method 2: Use sendWithCustomCheck with a custom response check function
// This gives you complete control over when to stop reading
ipCheck := func(buffer []byte) bool {
    // Stop reading if we see a valid IP address pattern
    return regexp.Match(`\d+\.\d+\.\d+\.\d+`, buffer)
}
resp, err := device.sendWithCustomCheck("+CIFSR", DefaultTimeout, ipCheck)
if err != nil {
    return err
}

// The response lines contain the actual data (like IP address)
for _, line := range resp.Lines {
    // Process the response data
    fmt.Println("Response line:", line)
}
```

## Network Connection Interface

The `Connection` struct implements Go's standard `net.Conn` interface, allowing for familiar Go networking patterns:

```go
// Get connection
conn, err := device.Dial("tcp", "api.example.com:80")
if err != nil {
    // Handle error
    return
}
defer conn.Close()

// Use standard net.Conn methods
conn.Write([]byte("GET /data HTTP/1.1\r\nHost: api.example.com\r\n\r\n"))

// Read response
buffer := make([]byte, 1024)
n, err := conn.Read(buffer)
if err != nil && err != io.EOF {
    // Handle error
    return
}
fmt.Printf("Received %d bytes: %s\n", n, buffer[:n])
```

## Connection Management

Each SIM800L module can handle up to 5 simultaneous connections (IDs 0-4).
The driver manages these connections automatically and provides buffer space for each connection.

```go
// Check network registration
registered, err := device.CheckNetworkRegistration()
if !registered {
    logger.Error("Not registered to network")
    return
}

// Check connection status
err = device.GetConnectionStatus()
if err != nil {
    logger.Error("Failed to get connection status", "error", err)
}

// Disconnect from GPRS when done
err = device.Disconnect()
if err != nil {
    logger.Error("Failed to disconnect", "error", err)
}
```

## Handling Special Responses

The SIM800L sometimes returns responses that don't follow the standard AT command pattern.
For example, when receiving data from a connection, the module will send:

```
+RECEIVE,0,128:
[128 bytes of data]
```

The driver handles these special cases automatically through the `checkForReceivedData` method,
which parses incoming data notifications and places the received data into the appropriate connection buffer.

## Error Handling

The driver provides specific error types for common error conditions:

```go
var (
    ErrTimeout       = errors.New("AT command timeout")
    ErrError         = errors.New("AT command error")
    ErrNotConnected  = errors.New("not connected to network")
    ErrNoIP          = errors.New("no IP address")
    ErrBadParameter  = errors.New("invalid parameter")
    ErrMaxConn       = errors.New("maximum connections reached")
    ErrUnimplemented = errors.New("operation not implemented")
)
```

## Hardware Reset

The driver supports hardware reset of the SIM800L module. This is useful for recovering from error states or initializing the module:

```go
// Perform hardware reset
if err := device.HardReset(); err != nil {
    logger.Error("Failed to reset module", "error", err)
    return
}

// After reset, wait for module to stabilize
// (The Init method already includes this reset and wait)
```

## API Reference

### Device Creation and Configuration

- `New(uart drivers.UART, resetPin machine.Pin, logger *slog.Logger) *Device` - Creates a new SIM800L device instance
- `Init() error` - Initializes the SIM800L device (includes hardware reset)
- `HardReset() error` - Performs a hardware reset of the device

### Network and GPRS Connection

- `Connect(apn, user, password string) error` - Establishes a GPRS connection with the specified APN
- `Disconnect() error` - Closes the GPRS connection
- `Dial(network, address string) (net.Conn, error)` - Creates a TCP or UDP connection
- `CloseConnection(id uint8) error` - Closes a specific connection by ID
- `GetConnectionStatus() error` - Updates status of all connections
- `CheckNetworkRegistration() (bool, error)` - Checks if registered to network
- `GetIP() (string, error)` - Returns the current IP address
- `GetGPRSStatus() (string, error)` - Returns the current GPRS connection state

### Data Transfer

- `SendData(id uint8, data []byte) (int, error)` - Sends data through a connection
- `ConnectionRead(id uint8, b []byte) (int, error)` - Reads data from a connection

### Low-level AT Command Handling

- `send(cmd string, timeout time.Duration) (*Response, error)` - Sends AT command with standard response handling
- `sendWithOptions(cmd string, expectOK bool, timeout time.Duration, checkFunc ResponseCheckFunc) (*Response, error)` - Sends AT command with custom response handling
- `sendWithCustomCheck(cmd string, timeout time.Duration, checkFunc ResponseCheckFunc) (*Response, error)` - Sends AT command with custom response completion check

## License

See [LICENSE](LICENSE) file for details.