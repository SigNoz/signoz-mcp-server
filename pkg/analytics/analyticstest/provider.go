package analyticstest

import (
	"context"

	"github.com/SigNoz/signoz-mcp-server/pkg/types/analyticstypes"
)

// Provider is an exported test-only analytics provider.
// All methods are no-ops. The exported type allows direct construction
// in test packages without going through the factory.
type Provider struct {
	stopC chan struct{}
}

func New() *Provider {
	return &Provider{stopC: make(chan struct{})}
}

func (p *Provider) Start(_ context.Context) error  { <-p.stopC; return nil }
func (p *Provider) Stop(_ context.Context) error   { close(p.stopC); return nil }
func (p *Provider) Send(_ context.Context, _ ...analyticstypes.Message) {}
func (p *Provider) TrackGroup(_ context.Context, _, _ string, _ map[string]any)     {}
func (p *Provider) TrackUser(_ context.Context, _, _, _ string, _ map[string]any)    {}
func (p *Provider) IdentifyGroup(_ context.Context, _ string, _ map[string]any)      {}
func (p *Provider) IdentifyUser(_ context.Context, _, _ string, _ map[string]any)    {}
