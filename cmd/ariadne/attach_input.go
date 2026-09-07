//go:build darwin || linux || windows

package main

type byteDetachFilter struct {
	detach        [2]byte
	pendingPrefix bool
}

func newByteDetachFilter(detach []byte) byteDetachFilter {
	return byteDetachFilter{detach: [2]byte{detach[0], detach[1]}}
}

func (filter *byteDetachFilter) feed(data []byte) ([]byte, bool) {
	result := make([]byte, 0, len(data)+1)
	for _, value := range data {
		if !filter.pendingPrefix {
			if value == filter.detach[0] {
				filter.pendingPrefix = true
			} else {
				result = append(result, value)
			}
			continue
		}
		filter.pendingPrefix = false
		switch value {
		case filter.detach[1]:
			return result, true
		case filter.detach[0]:
			result = append(result, filter.detach[0])
		default:
			result = append(result, filter.detach[0], value)
		}
	}
	return result, false
}

func (filter *byteDetachFilter) flush() []byte {
	if !filter.pendingPrefix {
		return nil
	}
	filter.pendingPrefix = false
	return []byte{filter.detach[0]}
}
