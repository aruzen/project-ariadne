//go:build darwin || linux || windows

package main

import (
	"bytes"
	"io"
	"testing"
)

type oneByteReader struct{ reader *bytes.Reader }

func (reader oneByteReader) Read(buffer []byte) (int, error) {
	if len(buffer) > 1 {
		buffer = buffer[:1]
	}
	return reader.reader.Read(buffer)
}

func TestParseDetachSequence(t *testing.T) {
	sequence, err := parseDetachSequence("Ctrl-A d")
	if err != nil || !bytes.Equal(sequence, []byte{1, 'd'}) {
		t.Fatalf("sequence = %v, %v", sequence, err)
	}
	if _, err := parseDetachSequence("ctrl-a"); err == nil {
		t.Fatal("one-key detach sequence accepted")
	}
}

func TestReadTerminalInputDetachAndEscaping(t *testing.T) {
	tests := []struct {
		name       string
		input      []byte
		wantData   []byte
		wantDetach bool
	}{
		{name: "ordinary", input: []byte("hello"), wantData: []byte("hello")},
		{name: "unknown suffix", input: []byte{'a', 1, 'x'}, wantData: []byte{'a', 1, 'x'}},
		{name: "literal prefix", input: []byte{'a', 1, 1, 'b'}, wantData: []byte{'a', 1, 'b'}},
		{name: "detach", input: []byte{'a', 1, 'd', 'x'}, wantData: []byte{'a'}, wantDetach: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			messages := make(chan inputMessage, 8)
			readTerminalInput(oneByteReader{reader: bytes.NewReader(test.input)}, []byte{1, 'd'}, messages)
			var data []byte
			detached := false
			for len(messages) != 0 {
				message := <-messages
				switch message.kind {
				case inputData:
					data = append(data, message.data...)
				case inputDetach:
					detached = true
				case inputFailure:
					if !test.wantDetach && message.err != io.EOF {
						t.Fatalf("input error = %v", message.err)
					}
				}
			}
			if !bytes.Equal(data, test.wantData) || detached != test.wantDetach {
				t.Fatalf("data=%v detached=%v", data, detached)
			}
		})
	}
}
