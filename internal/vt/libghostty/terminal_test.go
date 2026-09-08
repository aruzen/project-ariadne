//go:build cgo && (darwin || (linux && amd64) || (windows && amd64))

package libghostty

import "testing"

func TestTerminalScreen(t *testing.T) {
	terminal, err := NewTerminal(8, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	if err := terminal.Write([]byte("one\r\n\x1b[1;32mtwo\x1b[0m")); err != nil {
		t.Fatal(err)
	}
	screen, err := terminal.Screen()
	if err != nil {
		t.Fatal(err)
	}
	if screen.Cols != 8 || screen.Rows != 3 || screen.At(0, 0).Text != "o" || screen.At(2, 1).Text != "o" {
		t.Fatalf("unexpected screen: %+v", screen)
	}
	green := screen.At(0, 1).Style.Foreground
	if green.G <= green.R || green.G <= green.B {
		t.Fatalf("green foreground not preserved: %+v", green)
	}
	if err := terminal.Resize(10, 4); err != nil {
		t.Fatal(err)
	}
	resized, err := terminal.Screen()
	if err != nil || resized.Cols != 10 || resized.Rows != 4 {
		t.Fatalf("resized screen = %dx%d, %v", resized.Cols, resized.Rows, err)
	}
}

func TestTerminalRejectsInvalidSizeAndUseAfterClose(t *testing.T) {
	if _, err := NewTerminal(0, 1); err == nil {
		t.Fatal("NewTerminal accepted zero columns")
	}
	terminal, err := NewTerminal(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	terminal.Close()
	if err := terminal.Write([]byte("x")); err == nil {
		t.Fatal("Write accepted a closed terminal")
	}
}

func TestTerminalReturnsPTYResponses(t *testing.T) {
	terminal, err := NewTerminal(8, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	response, err := terminal.WriteWithResponse([]byte("\x1b[6n"))
	if err != nil {
		t.Fatal(err)
	}
	if string(response) != "\x1b[1;1R" {
		t.Fatalf("response = %q", response)
	}
}
