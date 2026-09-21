//go:build cgo && windows && arm64

package libghostty

/*
#cgo CPPFLAGS: -DGHOSTTY_STATIC
#cgo CFLAGS: -I${SRCDIR}/../../../build/libghostty-vt/windows-arm64/include
#cgo LDFLAGS: -L${SRCDIR}/../../../build/libghostty-vt/windows-arm64/lib -lghostty-vt-static -lapi-ms-win-core-synch-l1-2-0
*/
import "C"
