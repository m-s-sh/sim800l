// go:build tinygo

package main

import (
	"log/slog"
	"machine"
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
}
