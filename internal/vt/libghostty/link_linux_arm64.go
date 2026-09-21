//go:build cgo && linux && arm64

package libghostty

/*
#cgo CFLAGS: -I${SRCDIR}/../../../build/libghostty-vt/linux-arm64/include
#cgo LDFLAGS: ${SRCDIR}/../../../build/libghostty-vt/linux-arm64/lib/libghostty-vt.a
*/
import "C"
