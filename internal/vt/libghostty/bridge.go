//go:build cgo && (darwin || (linux && amd64) || (windows && amd64))

// Package libghostty isolates the unstable libghostty-vt C API.
package libghostty

/*
#include <ghostty/vt.h>

static int ariadne_ghostty_version(const char **pointer, size_t *length) {
	GhosttyString value = {0};
	GhosttyResult result =
		ghostty_build_info(GHOSTTY_BUILD_INFO_VERSION_STRING, &value);
	if (result != GHOSTTY_SUCCESS) {
		return (int)result;
	}
	*pointer = (const char *)value.ptr;
	*length = value.len;
	return 0;
}
*/
import "C"

import "fmt"

// Version returns the version reported by the statically linked library.
func Version() (string, error) {
	var pointer *C.char
	var length C.size_t
	if result := C.ariadne_ghostty_version(&pointer, &length); result != 0 {
		return "", fmt.Errorf("ghostty_build_info: result %d", int(result))
	}
	return C.GoStringN(pointer, C.int(length)), nil
}
