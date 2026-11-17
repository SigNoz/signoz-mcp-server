package tools

import (
	"context"
	"maps"
	"slices"
	"sync"
)

type argumentUseKey struct{}

// argumentUse records the undeclared top-level arguments a handler took on
// purpose during one call, so the ignored-arguments notice follows from what
// the handler did instead of from a second copy of that knowledge.
type argumentUse struct {
	mu      sync.Mutex
	handled map[string]struct{}
}

func withArgumentUse(ctx context.Context) (context.Context, *argumentUse) {
	use := &argumentUse{handled: make(map[string]struct{})}
	return context.WithValue(ctx, argumentUseKey{}, use), use
}

// markArgumentsHandled records top-level argument keys the handler read,
// forwarded to SigNoz, or discarded as an expected copy, so they are not
// reported as ignored. Call it where the handler does that, not at
// registration. Without a tracker in ctx, for example in a direct handler
// call, it does nothing.
func markArgumentsHandled(ctx context.Context, names ...string) {
	use, _ := ctx.Value(argumentUseKey{}).(*argumentUse)
	if use == nil {
		return
	}
	use.mu.Lock()
	defer use.mu.Unlock()
	for _, name := range names {
		use.handled[name] = struct{}{}
	}
}

// markForwardedArguments marks every key of a body the handler sends to
// SigNoz, which then decides what each field means.
func markForwardedArguments(ctx context.Context, body map[string]any) {
	markArgumentsHandled(ctx, slices.Collect(maps.Keys(body))...)
}

// unhandled returns the undeclared keys nothing marked during the call.
func (use *argumentUse) unhandled(undeclared []string) []string {
	if use == nil {
		return nil
	}
	use.mu.Lock()
	defer use.mu.Unlock()
	var ignored []string
	for _, name := range undeclared {
		if _, handled := use.handled[name]; !handled {
			ignored = append(ignored, name)
		}
	}
	return ignored
}
