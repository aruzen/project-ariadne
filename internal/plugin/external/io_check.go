package external

import (
	"context"
	"github.com/aruzen/ariadne/internal/core"
)

type operationCheckKey struct{}

func withOperationCheck(ctx context.Context, check func(core.Snapshot) error) context.Context {
	return context.WithValue(ctx, operationCheckKey{}, check)
}

// CheckOperation rechecks broker permission after the daemon acquires its I/O
// lifecycle lock. Ordinary daemon/frontend operations carry no plugin check.
func CheckOperation(ctx context.Context, snapshot core.Snapshot) error {
	if check, ok := ctx.Value(operationCheckKey{}).(func(core.Snapshot) error); ok {
		return check(snapshot)
	}
	return nil
}
