// go:build tinygo

package main

import (
	"log/slog"
	"machine"
	"runtime"
	"time"

	"github.com/m-s-sh/sim800l"
)

const (
	GPRSRX = machine.GPIO5
	GPRSTX = machine.GPIO4
)

func defaultGPRSConfig() machine.UARTConfig {
	return machine.UARTConfig{
		TX:       GPRSTX,
		RX:       GPRSRX,
		BaudRate: 9600,
	}
}

// global UART for printf output
var uart = machine.UART0

func putchar(c byte) {
	uart.WriteByte(c)
}

func main() {
	time.Sleep(5 * time.Second) // Wait for the serial port to be ready.
	machine.UART0.Configure(machine.UARTConfig{
		BaudRate: 115200,
		TX:       machine.GPIO16,
		RX:       machine.GPIO17,
	})

	logger := slog.New(slog.NewTextHandler(machine.UART0, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	time.Sleep(5 * time.Second) // Wait for the serial port to be ready.
	logger.Info("starting Pico W SIM800L example")
	err := machine.UART1.Configure(defaultGPRSConfig())
	if err != nil {
		logger.Error("failed to configure UART", slog.String("error", err.Error()))
		return
	}

	device := sim800l.New(machine.UART1, machine.GPIO0, logger)
	if err := device.Init(); err != nil {
		logger.Error("failed to configure SIM800L device", slog.String("error", err.Error()))
		return
	}
	logger.Info("GPRS device configured, connecting to network...")
	if err := device.Connect("internet.vivacom.bg", "VIVACOM", "VIVACOM"); err != nil {
		logger.Error("failed to initialize SIM800L device", slog.String("error", err.Error()))
		return
	}
	logger.Info("GPRS device initialized successfully")

	// Red LED
	pin1 := machine.GPIO2
	pin1.Configure(machine.PinConfig{Mode: machine.PinOutput})
	pin1.High()

	// Connect to web server
	conn, err := device.Dial("tcp", "tcpbin.com:80")
	if err != nil {
		logger.Error("failed to connect to server", slog.String("error", err.Error()))
		return
	}
	logger.Info("connected to server", slog.String("remoteAddr", conn.RemoteAddr().String()))
	var buf [256]byte
	var n int
	for {
		pin1.High()
		time.Sleep(500 * time.Millisecond)
		pin1.Low()
		time.Sleep(500 * time.Millisecond)
		logger.Info("sending HTTP request")
		// Send HTTP request
		request := "Hello" + "n"
		n, err = conn.Write([]byte(request))
		if err != nil {
			logger.Error("failed to write to connection", slog.String("error", err.Error()))
			return
		}
		logger.Info("wrote data to connection", slog.Int("bytes", n))
		// Read response

		n, err = conn.Read(buf[:])
		if err != nil {
			if err == sim800l.ErrTimeout {
				logger.Warn("read timeout, no data received")
				continue
			}
			logger.Error("failed to read from connection", slog.String("error", err.Error()))
			return
		}
		logger.Info("read data from connection", slog.Int("bytes", n), slog.String("data", string(buf[:n])))
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		logger.Info("memory stats",
			slog.Int64("alloc", int64(m.Alloc)), slog.Int("n", n),
		)
	}

}
