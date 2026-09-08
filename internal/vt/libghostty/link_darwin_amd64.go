//go:build cgo && darwin && amd64

package libghostty

/*
#cgo CFLAGS: -I${SRCDIR}/../../../build/libghostty-vt/darwin-amd64/include
#cgo LDFLAGS: ${SRCDIR}/../../../build/libghostty-vt/darwin-amd64/lib/libghostty-vt.a
*/
import "C"
