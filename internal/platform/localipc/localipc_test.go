package localipc

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestDefaultEndpoint(t *testing.T) {
	endpoint, err := DefaultEndpoint()
	if err != nil {
		t.Fatalf("DefaultEndpoint: %v", err)
	}
	if endpoint == "" {
		t.Fatal("DefaultEndpoint returned an empty endpoint")
	}
}

func TestListenDialAndRejectDuplicate(t *testing.T) {
	endpoint := testEndpoint(t)
	listener, err := Listen(endpoint)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() {
		_ = listener.Close()
		_ = listener.Cleanup()
	}()
	if duplicate, err := Listen(endpoint); !errors.Is(err, ErrAlreadyRunning) {
		if duplicate != nil {
			_ = duplicate.Close()
			_ = duplicate.Cleanup()
		}
		t.Fatalf("duplicate Listen error = %v", err)
	}

	accepted := make(chan net.Conn, 1)
	acceptErrors := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			acceptErrors <- err
			return
		}
		accepted <- connection
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := DialContext(ctx, endpoint)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer client.Close()
	select {
	case server := <-accepted:
		_ = server.Close()
	case err := <-acceptErrors:
		t.Fatalf("Accept: %v", err)
	case <-ctx.Done():
		t.Fatalf("Accept timed out: %v", ctx.Err())
	}
}

func TestDialMissingEndpointIsAbsent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	connection, err := DialContext(ctx, testEndpoint(t))
	if connection != nil {
		_ = connection.Close()
	}
	if !IsAbsent(err) {
		t.Fatalf("missing endpoint error = %v", err)
	}
}
