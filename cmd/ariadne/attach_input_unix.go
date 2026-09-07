//go:build darwin || linux

package main

type terminalInputFilter struct{ bytes byteDetachFilter }

func newTerminalInputFilter(detach []byte) *terminalInputFilter {
	return &terminalInputFilter{bytes: newByteDetachFilter(detach)}
}

func (filter *terminalInputFilter) Feed(data []byte) ([]byte, bool) {
	return filter.bytes.feed(data)
}

func (filter *terminalInputFilter) Flush() []byte { return filter.bytes.flush() }
