package statefile

import "golang.org/x/sys/windows"

func replaceFile(source, destination string) error {
	sourcePointer, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPointer, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(
		sourcePointer,
		destinationPointer,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	)
}

// MoveFileEx with MOVEFILE_WRITE_THROUGH flushes the replacement operation.
// Windows does not expose a directory Sync operation through os.File.
func syncDirectory(string) error {
	return nil
}
