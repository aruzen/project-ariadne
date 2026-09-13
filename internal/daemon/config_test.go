package daemon

import (
	"testing"

	"github.com/aruzen/streammux"
)

func TestDefaultConfigBackpressuresInboundStreams(t *testing.T) {
	if policy := DefaultConfig("state.json").Peer.InboundQueuePolicy; policy != streammux.InboundQueueBackpressure {
		t.Fatalf("InboundQueuePolicy = %v", policy)
	}
}
