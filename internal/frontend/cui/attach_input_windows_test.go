//go:build windows

package cui

import (
	"bytes"
	"testing"
)

func TestWindowsInputFilterEncodedDetach(t *testing.T) {
	filter := newTerminalInputFilter([]byte{1, 'd'})
	var output []byte
	detached := false
	for _, value := range windowsEncodedDetachForTest() {
		data, found := filter.Feed([]byte{value})
		output = append(output, data...)
		if found {
			detached = true
			break
		}
	}
	if !detached || len(output) != 0 {
		t.Fatalf("output=%q detached=%v", output, detached)
	}
}

func TestWindowsInputFilterPreservesNonDetachRecords(t *testing.T) {
	input := []byte("\x1b[17;29;0;1;8;1_" +
		"\x1b[67;46;3;1;8;1_" +
		"\x1b[67;46;3;0;8;1_" +
		"\x1b[17;29;0;0;0;1_")
	filter := newTerminalInputFilter([]byte{1, 'd'})
	var output []byte
	for _, value := range input {
		data, detached := filter.Feed([]byte{value})
		if detached {
			t.Fatal("non-detach input detached")
		}
		output = append(output, data...)
	}
	output = append(output, filter.Flush()...)
	if !bytes.Equal(output, input) {
		t.Fatalf("output=%q, want %q", output, input)
	}
}

func TestWindowsInputFilterFlushesIncompleteSequence(t *testing.T) {
	input := []byte("x\x1b[65;30")
	filter := newTerminalInputFilter([]byte{1, 'd'})
	output, detached := filter.Feed(input)
	if detached {
		t.Fatal("incomplete sequence detached")
	}
	output = append(output, filter.Flush()...)
	if !bytes.Equal(output, input) {
		t.Fatalf("output=%q, want %q", output, input)
	}
}

func windowsEncodedDetachForTest() []byte {
	return []byte("\x1b[17;29;0;1;8;1_" +
		"\x1b[65;30;1;1;8;1_" +
		"\x1b[65;30;1;0;8;1_" +
		"\x1b[17;29;0;0;0;1_" +
		"\x1b[68;32;100;1;0;1_" +
		"\x1b[68;32;100;0;0;1_")
}
