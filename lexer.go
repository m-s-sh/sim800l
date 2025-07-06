package sim800l

import (
	"bytes"
	"strings"
)

// TokenType represents the type of token parsed from AT command responses
type TokenType int

const (
	TokenInvalid  TokenType = iota
	TokenOK                 // OK response
	TokenError              // ERROR response
	TokenCME                // +CME ERROR response
	TokenCMS                // +CMS ERROR response
	TokenCommand            // Command echo (e.g., AT+CGATT?)
	TokenResponse           // Response line (e.g., +CGATT: 1)
	TokenURC                // Unsolicited Result Code
	TokenData               // Raw data from connection
	TokenPrompt             // > prompt for data input
	TokenEmpty              // Empty line
)

// Maximum buffer and token sizes
const (
	MaxBufferSize = 512
	MaxTokens     = 16
	MaxValues     = 8
)

// Token represents a parsed segment of an AT command response
type Token struct {
	Type      TokenType
	Command   string            // Command name (e.g., "+CGATT" from "+CGATT: 1")
	Value     string            // Parameter value (e.g., "1" from "+CGATT: 1")
	Values    [MaxValues]string // Multiple values (e.g., ["0", "0", "28403"] from "+COPS: 0,0,"28403"")
	ValuesLen int               // Number of valid values in the Values array
	Raw       string            // Raw token text
}

// Lexer tokenizes AT command responses with support for streaming data
type Lexer struct {
	buffer    [MaxBufferSize]byte
	bufferLen int
	tokens    [MaxTokens]Token
	tokenLen  int
}

// NewLexer creates a new AT command response lexer
func NewLexer() *Lexer {
	return &Lexer{
		bufferLen: 0,
		tokenLen:  0,
	}
}

// Reset clears the lexer state but keeps allocated memory
func (l *Lexer) Reset() {
	l.bufferLen = 0
	l.tokenLen = 0
}

// Tokenize processes the given data and returns tokens
// It can be called multiple times with partial data until a complete response is received
func (l *Lexer) Tokenize(data []byte) []Token {
	// Reset token count but keep the array
	l.tokenLen = 0

	// Append to existing buffer, being careful not to overflow
	toCopy := len(data)
	if toCopy > MaxBufferSize-l.bufferLen {
		toCopy = MaxBufferSize - l.bufferLen
	}
	for i := 0; i < toCopy; i++ {
		l.buffer[l.bufferLen+i] = data[i]
	}
	l.bufferLen += toCopy

	// Process complete lines only
	for {
		line, rest, found := l.nextLine()
		if !found {
			break // No complete line found
		}

		// Update buffer with remaining data
		l.bufferLen = len(rest)
		for i := 0; i < l.bufferLen; i++ {
			l.buffer[i] = rest[i]
		}

		if len(line) == 0 {
			if l.tokenLen < MaxTokens {
				l.tokens[l.tokenLen] = Token{Type: TokenEmpty, Raw: ""}
				l.tokenLen++
			}
			continue
		}

		l.parseLine(line)
	}

	// Return a slice view of the tokens array
	return l.tokens[:l.tokenLen]
}

// Append adds more data to the buffer without resetting tokens
// Returns true if buffer has space for more data
func (l *Lexer) Append(data []byte) bool {
	if l.bufferLen >= MaxBufferSize {
		return false // Buffer is full
	}

	toCopy := len(data)
	if toCopy > MaxBufferSize-l.bufferLen {
		toCopy = MaxBufferSize - l.bufferLen
	}

	for i := 0; i < toCopy; i++ {
		l.buffer[l.bufferLen+i] = data[i]
	}
	l.bufferLen += toCopy

	return l.bufferLen < MaxBufferSize
}

// Process parses any complete lines in the current buffer
// Returns true if more complete responses may be available
func (l *Lexer) Process() ([]Token, bool) {
	l.tokenLen = 0
	hasData := l.bufferLen > 0

	// Process complete lines only
	for {
		line, rest, found := l.nextLine()
		if !found {
			break // No complete line found
		}

		// Update buffer with remaining data
		l.bufferLen = len(rest)
		for i := 0; i < l.bufferLen; i++ {
			l.buffer[i] = rest[i]
		}

		if len(line) == 0 {
			if l.tokenLen < MaxTokens {
				l.tokens[l.tokenLen] = Token{Type: TokenEmpty, Raw: ""}
				l.tokenLen++
			}
			continue
		}

		l.parseLine(line)
	}

	return l.tokens[:l.tokenLen], hasData
}

// nextLine extracts the next line from the buffer
func (l *Lexer) nextLine() (line []byte, rest []byte, found bool) {
	if l.bufferLen == 0 {
		return nil, l.buffer[:0], false
	}

	// Special case for prompt character
	if l.buffer[0] == '>' {
		return []byte(">"), l.buffer[1:l.bufferLen], true
	}

	// Find end of line
	idx := -1
	for i := 0; i < l.bufferLen; i++ {
		if l.buffer[i] == '\n' {
			idx = i
			break
		}
	}

	if idx == -1 {
		// No complete line yet
		return nil, l.buffer[:l.bufferLen], false
	}

	line = l.buffer[:idx]
	rest = l.buffer[idx+1 : l.bufferLen]

	// Trim carriage return if present
	lineLen := len(line)
	if lineLen > 0 && line[lineLen-1] == '\r' {
		line = line[:lineLen-1]
	}

	return line, rest, true
}

// parseLine processes a single line into a token
func (l *Lexer) parseLine(line []byte) {
	// Skip if token array is full
	if l.tokenLen >= MaxTokens {
		return
	}

	lineStr := string(line)

	// Check for special responses
	switch {
	case lineStr == "OK":
		l.tokens[l.tokenLen] = Token{Type: TokenOK, Raw: lineStr}
		l.tokenLen++
		return

	case lineStr == "ERROR":
		l.tokens[l.tokenLen] = Token{Type: TokenError, Raw: lineStr}
		l.tokenLen++
		return

	case strings.HasPrefix(lineStr, "+CME ERROR:"):
		l.tokens[l.tokenLen] = Token{
			Type:  TokenCME,
			Value: strings.TrimSpace(lineStr[11:]), // Extract error code
			Raw:   lineStr,
		}
		l.tokenLen++
		return

	case strings.HasPrefix(lineStr, "+CMS ERROR:"):
		l.tokens[l.tokenLen] = Token{
			Type:  TokenCMS,
			Value: strings.TrimSpace(lineStr[11:]), // Extract error code
			Raw:   lineStr,
		}
		l.tokenLen++
		return

	case lineStr == ">":
		l.tokens[l.tokenLen] = Token{Type: TokenPrompt, Raw: lineStr}
		l.tokenLen++
		return
	}

	// Check for response format: "+COMMAND: value1,value2,..."
	if strings.Contains(lineStr, ":") {
		parts := strings.SplitN(lineStr, ":", 2)
		command := strings.TrimSpace(parts[0])
		value := ""
		var values [MaxValues]string
		valuesLen := 0

		if len(parts) > 1 {
			value = strings.TrimSpace(parts[1])
			// Split by commas, handling quoted values
			valuesSlice := parseValues(value)
			// Copy to fixed array
			for i, v := range valuesSlice {
				if i < MaxValues {
					values[i] = v
					valuesLen++
				}
			}
		}

		// Check if this is a URC (Unsolicited Result Code)
		tokenType := TokenResponse
		if isURC(command) {
			tokenType = TokenURC
		}

		l.tokens[l.tokenLen] = Token{
			Type:      tokenType,
			Command:   command,
			Value:     value,
			Values:    values,
			ValuesLen: valuesLen,
			Raw:       lineStr,
		}
		l.tokenLen++
		return
	}

	// Assume command echo or unknown data
	if strings.HasPrefix(lineStr, "AT") {
		l.tokens[l.tokenLen] = Token{
			Type:    TokenCommand,
			Command: lineStr,
			Raw:     lineStr,
		}
	} else {
		l.tokens[l.tokenLen] = Token{
			Type: TokenData,
			Raw:  lineStr,
		}
	}
	l.tokenLen++
}

// BufferAvailable returns the number of bytes available in the buffer
func (l *Lexer) BufferAvailable() int {
	return MaxBufferSize - l.bufferLen
}

// HasCompleteResponse checks if the buffer contains a complete AT response
// (either OK, ERROR, or ERROR code)
func (l *Lexer) HasCompleteResponse() bool {
	buf := l.buffer[:l.bufferLen]
	return containsAny(buf, [][]byte{
		[]byte("OK\r\n"),
		[]byte("ERROR\r\n"),
		[]byte("+CME ERROR"),
		[]byte("+CMS ERROR"),
	})
}

// containsAny checks if any of the patterns appear in the data
func containsAny(data []byte, patterns [][]byte) bool {
	for _, p := range patterns {
		if bytes.Contains(data, p) {
			return true
		}
	}
	return false
}

// parseValues handles splitting comma-separated values, respecting quotes
func parseValues(s string) []string {
	tempValues := make([]string, 0, MaxValues)
	var inQuote bool
	var builder strings.Builder

	for i := 0; i < len(s); i++ {
		c := s[i]

		if c == '"' {
			inQuote = !inQuote
			builder.WriteByte(c)
		} else if c == ',' && !inQuote {
			if len(tempValues) < MaxValues {
				tempValues = append(tempValues, builder.String())
			}
			builder.Reset()
		} else {
			builder.WriteByte(c)
		}
	}

	if builder.Len() > 0 && len(tempValues) < MaxValues {
		tempValues = append(tempValues, builder.String())
	}

	return tempValues
}

var urcs = []string{
	"+CREG", "+CGREG", "+CEREG", // Network registration
	"+CMTI", "+CMT", // SMS notifications
	"+CUSD",                           // USSD notifications
	"+CLIP",                           // Caller ID
	"+CIEV",                           // Generic URC
	"RING",                            // Incoming call
	"+DTMF",                           // DTMF tones
	"BUSY", "NO ANSWER", "NO CARRIER", // Call status
	"+CPAS", // Phone activity status
	"+IPD",  // Incoming data notification
}

// isURC determines if a command is an Unsolicited Result Code
func isURC(cmd string) bool {
	for _, urc := range urcs {
		if cmd == urc {
			return true
		}
	}
	return false
}
