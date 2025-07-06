package sim800l

import (
	"testing"
)

// Helper function to convert slice to fixed array for tests
func makeTestValues(values []string) ([MaxValues]string, int) {
	var result [MaxValues]string
	length := 0
	for i, v := range values {
		if i < MaxValues {
			result[i] = v
			length++
		}
	}
	return result, length
}

// Helper function to compare tokens
func compareToken(t *testing.T, got Token, want Token, index int) {
	if got.Type != want.Type {
		t.Errorf("token %d: Type = %v, want %v", index, got.Type, want.Type)
	}
	if got.Command != want.Command {
		t.Errorf("token %d: Command = %q, want %q", index, got.Command, want.Command)
	}
	if got.Value != want.Value {
		t.Errorf("token %d: Value = %q, want %q", index, got.Value, want.Value)
	}
	if got.Raw != want.Raw {
		t.Errorf("token %d: Raw = %q, want %q", index, got.Raw, want.Raw)
	}
	if got.ValuesLen != want.ValuesLen {
		t.Errorf("token %d: ValuesLen = %d, want %d", index, got.ValuesLen, want.ValuesLen)
	} else {
		for i := 0; i < got.ValuesLen; i++ {
			if got.Values[i] != want.Values[i] {
				t.Errorf("token %d: Values[%d] = %q, want %q", index, i, got.Values[i], want.Values[i])
			}
		}
	}
}

// Helper to compare token slices
func compareTokens(t *testing.T, got []Token, want []Token) {
	if len(got) != len(want) {
		t.Errorf("got %d tokens, want %d", len(got), len(want))
		return
	}

	for i := range got {
		compareToken(t, got[i], want[i], i)
	}
}

func TestLexer_Tokenize(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []Token
	}{
		{
			name:  "Simple OK response",
			input: "OK\r\n",
			expected: []Token{
				{Type: TokenOK, Raw: "OK"},
			},
		},
		{
			name:  "Error response",
			input: "ERROR\r\n",
			expected: []Token{
				{Type: TokenError, Raw: "ERROR"},
			},
		},
		{
			name:  "CME Error response",
			input: "+CME ERROR: 10\r\n",
			expected: []Token{
				{Type: TokenCME, Value: "10", Raw: "+CME ERROR: 10"},
			},
		},
		{
			name:  "Command with response",
			input: "+CGATT: 1\r\nOK\r\n",
			expected: []Token{
				{Type: TokenResponse, Command: "+CGATT", Value: "1"},
				{Type: TokenOK, Raw: "OK"},
			},
		},
		{
			name:  "Multiple value response",
			input: "+COPS: 0,0,\"28403\"\r\nOK\r\n",
			expected: []Token{
				{Type: TokenResponse, Command: "+COPS", Value: "0,0,\"28403\""},
				{Type: TokenOK, Raw: "OK"},
			},
		},
		{
			name:  "URC notification",
			input: "+CREG: 1,5\r\n",
			expected: []Token{
				{Type: TokenURC, Command: "+CREG", Value: "1,5"},
			},
		},
		{
			name:  "Data input prompt",
			input: "AT+CIPSEND=0,5\r\n>\r\n",
			expected: []Token{
				{Type: TokenCommand, Command: "AT+CIPSEND=0,5", Raw: "AT+CIPSEND=0,5"},
				{Type: TokenPrompt, Raw: ">"},
			},
		},
		{
			name:  "Complex response with empty lines",
			input: "\r\n+COPS: 0,0,\"28403\"\r\n\r\nOK\r\n",
			expected: []Token{
				{Type: TokenEmpty, Raw: ""},
				{Type: TokenResponse, Command: "+COPS", Value: "0,0,\"28403\""},
				{Type: TokenEmpty, Raw: ""},
				{Type: TokenOK, Raw: "OK"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Prepare expected values
			for i := range tc.expected {
				if tc.expected[i].Type == TokenResponse || tc.expected[i].Type == TokenURC {
					values, valuesLen := makeTestValues(parseValues(tc.expected[i].Value))
					tc.expected[i].Values = values
					tc.expected[i].ValuesLen = valuesLen
					if tc.expected[i].Raw == "" {
						// If Raw wasn't explicitly set, set it now
						tc.expected[i].Raw = tc.expected[i].Command + ": " + tc.expected[i].Value
					}
				}
			}

			lexer := NewLexer()
			result := lexer.Tokenize([]byte(tc.input))
			compareTokens(t, result, tc.expected)
		})
	}
}

func TestLexer_Stream(t *testing.T) {
	tests := []struct {
		name      string
		chunks    []string
		expected  []Token
		checkEach bool // Whether to check tokens after each chunk
	}{
		{
			name: "Split simple response",
			chunks: []string{
				"+CGA",
				"TT: 1\r\n",
				"OK\r\n",
			},
			expected: []Token{
				{Type: TokenResponse, Command: "+CGATT", Value: "1", Raw: "+CGATT: 1"},
				{Type: TokenOK, Raw: "OK"},
			},
		},
		{
			name: "Split at line boundary",
			chunks: []string{
				"+CGATT: 1\r\n",
				"OK\r\n",
			},
			expected: []Token{
				{Type: TokenResponse, Command: "+CGATT", Value: "1", Raw: "+CGATT: 1"},
				{Type: TokenOK, Raw: "OK"},
			},
			checkEach: true,
		},
		{
			name: "Split inside quoted string",
			chunks: []string{
				"+COPS: 0,0,\"284",
				"03\"\r\n",
				"OK\r\n",
			},
			expected: []Token{
				{Type: TokenResponse, Command: "+COPS", Value: "0,0,\"28403\"", Raw: "+COPS: 0,0,\"28403\""},
				{Type: TokenOK, Raw: "OK"},
			},
		},
		{
			name: "Multiple URCs in chunks",
			chunks: []string{
				"+CREG: 1,5\r\n+CG",
				"REG: 1,1\r\n+IPD,0,5:",
				"Hello\r\n",
			},
			expected: []Token{
				{Type: TokenURC, Command: "+CREG", Value: "1,5", Raw: "+CREG: 1,5"},
				{Type: TokenURC, Command: "+CGREG", Value: "1,1", Raw: "+CGREG: 1,1"},
				{Type: TokenURC, Command: "+IPD", Value: ",0,5:Hello", Raw: "+IPD,0,5:Hello"},
			},
			checkEach: true,
		},
		{
			name: "Complex response with prompt",
			chunks: []string{
				"AT+CIPSEND=0,5\r\n",
				">\r\n",
				"Hello",
				"OK\r\n",
			},
			expected: []Token{
				{Type: TokenCommand, Command: "AT+CIPSEND=0,5", Raw: "AT+CIPSEND=0,5"},
				{Type: TokenPrompt, Raw: ">"},
				{Type: TokenData, Raw: "Hello"},
				{Type: TokenOK, Raw: "OK"},
			},
			checkEach: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Prepare expected values for response/URC tokens
			for i := range tc.expected {
				if tc.expected[i].Type == TokenResponse || tc.expected[i].Type == TokenURC {
					if tc.expected[i].ValuesLen == 0 {
						values, valuesLen := makeTestValues(parseValues(tc.expected[i].Value))
						tc.expected[i].Values = values
						tc.expected[i].ValuesLen = valuesLen
					}
				}
			}

			lexer := NewLexer()
			var allTokens []Token

			// Process each chunk
			for i, chunk := range tc.chunks {
				lexer.Append([]byte(chunk))
				tokens, _ := lexer.Process()

				// Add tokens to our collection
				for _, tok := range tokens {
					allTokens = append(allTokens, tok)
				}

				// If we should check after each chunk, verify we have the expected tokens so far
				if tc.checkEach {
					// Calculate how many tokens we should have by now
					expectedSoFar := 0

					remainingChunks := ""

					// Count how many complete responses should be in the chunks processed so far
					for j := 0; j <= i; j++ {
						remainingChunks += tc.chunks[j]
					}

					// Count complete responses (ending with \r\n)
					for _, expected := range tc.expected {
						if expected.Raw+"\r\n" <= remainingChunks {
							expectedSoFar++
						}
					}

					if len(allTokens) > 0 && expectedSoFar > 0 {
						// Only compare if we have tokens and expect some
						compareTokens(t, allTokens, tc.expected[:expectedSoFar])
					}
				}
			}

			// Final check of all tokens
			compareTokens(t, allTokens, tc.expected)
		})
	}
}

func TestLexer_Reset(t *testing.T) {
	lexer := NewLexer()
	lexer.Append([]byte("+CGATT: 1\r\nOK\r\n"))
	tokens, _ := lexer.Process()

	if len(tokens) != 2 {
		t.Fatalf("Expected 2 tokens, got %d", len(tokens))
	}

	// Reset the lexer
	lexer.Reset()

	// Check buffer is empty
	if lexer.bufferLen != 0 {
		t.Errorf("Buffer not empty after reset: len=%d", lexer.bufferLen)
	}

	// Check token count is reset
	if lexer.tokenLen != 0 {
		t.Errorf("Token count not reset: count=%d", lexer.tokenLen)
	}

	// Try parsing new data
	lexer.Append([]byte("ERROR\r\n"))
	tokens, _ = lexer.Process()

	if len(tokens) != 1 {
		t.Fatalf("Expected 1 token after reset, got %d", len(tokens))
	}

	if tokens[0].Type != TokenError {
		t.Errorf("Expected ERROR token, got %v", tokens[0].Type)
	}
}

func TestLexer_BufferAvailable(t *testing.T) {
	lexer := NewLexer()

	// Empty buffer
	if avail := lexer.BufferAvailable(); avail != MaxBufferSize {
		t.Errorf("Expected %d available bytes, got %d", MaxBufferSize, avail)
	}

	// Add some data
	testData := make([]byte, 100)
	lexer.Append(testData)

	if avail := lexer.BufferAvailable(); avail != MaxBufferSize-100 {
		t.Errorf("Expected %d available bytes, got %d", MaxBufferSize-100, avail)
	}
}

func TestLexer_HasCompleteResponse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "No complete response",
			input:    "+CGATT",
			expected: false,
		},
		{
			name:     "OK response",
			input:    "+CGATT: 1\r\nOK\r\n",
			expected: true,
		},
		{
			name:     "ERROR response",
			input:    "ERROR\r\n",
			expected: true,
		},
		{
			name:     "CME ERROR response",
			input:    "+CME ERROR: 10\r\n",
			expected: true,
		},
		{
			name:     "CMS ERROR response",
			input:    "+CMS ERROR: 304\r\n",
			expected: true,
		},
		{
			name:     "Partial error",
			input:    "+CME ERR",
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lexer := NewLexer()
			lexer.Append([]byte(tc.input))

			if hasComplete := lexer.HasCompleteResponse(); hasComplete != tc.expected {
				t.Errorf("HasCompleteResponse() = %v, want %v", hasComplete, tc.expected)
			}
		})
	}
}
