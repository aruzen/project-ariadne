//go:build cgo && windows && amd64

package libghostty

/*
#cgo CPPFLAGS: -DGHOSTTY_STATIC
#cgo CFLAGS: -I${SRCDIR}/../../../build/libghostty-vt/windows-amd64/include
#cgo LDFLAGS: -L${SRCDIR}/../../../build/libghostty-vt/windows-amd64/lib -lghostty-vt-static -lapi-ms-win-core-synch-l1-2-0
*/
import "C"
