package sim800l

import "io"

type Pin interface {
	High()
	Low()
}

type UART interface {
	io.Reader
	io.Writer

	Buffered() int
}
