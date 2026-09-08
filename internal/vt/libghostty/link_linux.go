//go:build cgo && linux && amd64

package libghostty

/*
#cgo CFLAGS: -I${SRCDIR}/../../../build/libghostty-vt/linux-amd64/include
#cgo LDFLAGS: ${SRCDIR}/../../../build/libghostty-vt/linux-amd64/lib/libghostty-vt.a
*/
import "C"
