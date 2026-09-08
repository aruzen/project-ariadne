//go:build windows

package cui

import (
	"strconv"
	"strings"
)

const maxWin32InputSequence = 96

type terminalInputFilter struct {
	detach           [2]byte
	sequence         []byte
	pendingModifiers []byte
	pendingPrefix    []byte
}

func newTerminalInputFilter(detach []byte) *terminalInputFilter {
	return &terminalInputFilter{detach: [2]byte{detach[0], detach[1]}}
}

func (filter *terminalInputFilter) Feed(data []byte) ([]byte, bool) {
	result := make([]byte, 0, len(data))
	for _, value := range data {
		if len(filter.sequence) == 0 {
			if value == 0x1b {
				filter.sequence = append(filter.sequence, value)
				continue
			}
			if filter.processToken(&result, []byte{value}, value, true, true, false) {
				return result, true
			}
			continue
		}

		filter.sequence = append(filter.sequence, value)
		if len(filter.sequence) == 2 && value != '[' {
			if filter.flushSequenceAsBytes(&result) {
				return result, true
			}
			continue
		}
		if len(filter.sequence) <= 2 {
			continue
		}
		if value == '_' {
			raw := append([]byte(nil), filter.sequence...)
			filter.sequence = filter.sequence[:0]
			virtualKey, unicode, keyDown, ok := parseWin32InputSequence(raw)
			if !ok {
				if filter.processBytes(&result, raw) {
					return result, true
				}
				continue
			}
			hasLogical := unicode > 0 && unicode <= 0xff
			if filter.processToken(&result, raw, byte(unicode), hasLogical, keyDown, isModifierVirtualKey(virtualKey)) {
				return result, true
			}
			continue
		}
		if (value < '0' || value > '9') && value != ';' || len(filter.sequence) > maxWin32InputSequence {
			if filter.flushSequenceAsBytes(&result) {
				return result, true
			}
		}
	}
	return result, false
}

func (filter *terminalInputFilter) Flush() []byte {
	result := make([]byte, 0, len(filter.pendingPrefix)+len(filter.pendingModifiers)+len(filter.sequence))
	result = append(result, filter.pendingPrefix...)
	result = append(result, filter.pendingModifiers...)
	result = append(result, filter.sequence...)
	filter.pendingPrefix = nil
	filter.pendingModifiers = nil
	filter.sequence = nil
	return result
}

func (filter *terminalInputFilter) flushSequenceAsBytes(result *[]byte) bool {
	raw := append([]byte(nil), filter.sequence...)
	filter.sequence = filter.sequence[:0]
	return filter.processBytes(result, raw)
}

func (filter *terminalInputFilter) processBytes(result *[]byte, data []byte) bool {
	for _, value := range data {
		if filter.processToken(result, []byte{value}, value, true, true, false) {
			return true
		}
	}
	return false
}

func (filter *terminalInputFilter) processToken(result *[]byte, raw []byte, logical byte, hasLogical, keyDown, modifier bool) bool {
	if len(filter.pendingPrefix) != 0 {
		if keyDown && hasLogical {
			if logical == filter.detach[1] {
				filter.pendingPrefix = nil
				filter.pendingModifiers = nil
				return true
			}
			if logical == filter.detach[0] {
				*result = append(*result, filter.pendingPrefix...)
				filter.pendingPrefix = nil
				filter.pendingModifiers = nil
				return false
			}
			*result = append(*result, filter.pendingPrefix...)
			filter.pendingPrefix = nil
		} else if keyDown && !modifier {
			*result = append(*result, filter.pendingPrefix...)
			filter.pendingPrefix = nil
		} else {
			filter.pendingPrefix = append(filter.pendingPrefix, raw...)
			return false
		}
	}

	if modifier {
		filter.pendingModifiers = append(filter.pendingModifiers, raw...)
		if !keyDown {
			*result = append(*result, filter.pendingModifiers...)
			filter.pendingModifiers = nil
		}
		return false
	}
	if keyDown && hasLogical && logical == filter.detach[0] {
		filter.pendingPrefix = append(filter.pendingPrefix, filter.pendingModifiers...)
		filter.pendingPrefix = append(filter.pendingPrefix, raw...)
		filter.pendingModifiers = nil
		return false
	}
	*result = append(*result, filter.pendingModifiers...)
	filter.pendingModifiers = nil
	*result = append(*result, raw...)
	return false
}

func parseWin32InputSequence(sequence []byte) (virtualKey int, unicode int, keyDown bool, ok bool) {
	if len(sequence) < 5 || sequence[0] != 0x1b || sequence[1] != '[' || sequence[len(sequence)-1] != '_' {
		return 0, 0, false, false
	}
	fields := strings.Split(string(sequence[2:len(sequence)-1]), ";")
	if len(fields) != 6 {
		return 0, 0, false, false
	}
	values := make([]int, len(fields))
	for index, field := range fields {
		value, err := strconv.Atoi(field)
		if err != nil || value < 0 {
			return 0, 0, false, false
		}
		values[index] = value
	}
	if values[3] != 0 && values[3] != 1 {
		return 0, 0, false, false
	}
	return values[0], values[2], values[3] == 1, true
}

func isModifierVirtualKey(virtualKey int) bool {
	return virtualKey == 0x10 || virtualKey == 0x11 || virtualKey == 0x12
}
