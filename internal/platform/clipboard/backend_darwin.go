//go:build darwin

package clipboard

import "context"

type systemBackend struct{}

func NewSystemBackend() Backend { return systemBackend{} }

func (systemBackend) Read(ctx context.Context, maxBytes int) ([]byte, error) {
	return readCommand(ctx, maxBytes, "pbpaste")
}

func (systemBackend) Write(ctx context.Context, data []byte) error {
	return writeCommand(ctx, "pbcopy", nil, data)
}
