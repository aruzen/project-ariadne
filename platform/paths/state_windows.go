//go:build windows

package paths

import "os"

func defaultStateDirectory() (string, error) {
	return os.UserCacheDir()
}
