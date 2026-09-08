//go:build cgo && darwin && arm64

package libghostty

/*
#cgo CFLAGS: -I${SRCDIR}/../../../build/libghostty-vt/darwin-arm64/include
#cgo LDFLAGS: ${SRCDIR}/../../../build/libghostty-vt/darwin-arm64/lib/libghostty-vt.a
*/
import "C"
