# SIM800L Driver for TinyGo

This package implements a driver for the SIM800L GSM/GPRS module optimized for TinyGo running on constrained environments like the Raspberry Pi Pico.

[![Go Reference](https://pkg.go.dev/badge/github.com/m-s-sh/sim800l.svg)](https://pkg.go.dev/github.com/m-s-sh/sim800l)

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
- Memory-optimized for constrained environments

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

## Memory Optimization for TinyGo

This driver is specifically optimized for TinyGo and constrained environments:

- Uses fixed-size arrays instead of dynamic slices for all buffers
- Minimizes memory allocations during operation
- Efficient buffer management for UART communication
- Static allocation of response buffers and data structures
- Uses internal device buffer for command construction to avoid allocations

## Custom Response Handling

The driver includes built-in handlers for standard AT command responses. Most functionality is exposed through public methods that handle the underlying AT command communication for you.

```go
// Check if the device is responsive
if err := device.Init(); err != nil {
    logger.Error("Device not responding", "error", err)
    return
}

// Get the current signal strength
signalStrength := device.Signal() 
logger.Info("Signal strength", "value", signalStrength)

// Connect to GPRS
if err := device.Connect("your-apn", "username", "password"); err != nil {
    logger.Error("Failed to connect", "error", err)
}
```

This abstraction handles all the complexities of AT command processing, response parsing, and error handling.

### Example Usage

```go
// Check the network signal strength
signalStrength := device.Signal()
logger.Info("Current signal strength", "value", signalStrength)

// Check network registration
if err := device.Connect("your-apn", "", ""); err != nil {
    if errors.Is(err, ErrNoIP) {
        logger.Error("No IP address assigned")
    } else {
        logger.Error("Failed to connect", "error", err)
    }
}

// Get device information
logger.Info("Device IMEI", "imei", device.IMEI)
logger.Info("Network operator", "operator", device.Operator)
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

## API Reference

### Device Creation and Configuration

- `New(uart UART, resetPin Pin, logger *slog.Logger) *Device` - Creates a new SIM800L device instance
- `Init() error` - Initializes the SIM800L device (includes hardware reset)
- `HardReset() error` - Performs a hardware reset of the device
- `Signal() int` - Returns the current signal strength (0-31, 99=unknown)

### Network and GPRS Connection

- `Connect(apn, user, password string) error` - Establishes a GPRS connection with the specified APN
- `Disconnect() error` - Closes the GPRS connection
- `Dial(network, address string) (net.Conn, error)` - Creates a TCP or UDP connection
- `CloseConnection(id uint8) error` - Closes a specific connection by ID

### Device Information

- `IMEI string` - Module IMEI number (available after Init)
- `Operator string` - Network operator name
- `IP string` - Current IP address (when connected to GPRS)

## License

See [LICENSE](LICENSE) file for details.