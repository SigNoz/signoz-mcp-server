package noopanalytics

import (
	"context"

	"github.com/SigNoz/signoz-mcp-server/pkg/analytics"
	"github.com/SigNoz/signoz-mcp-server/pkg/types/analyticstypes"
)

type provider struct {
	stopC chan struct{}
}

func New() analytics.Analytics {
	return &provider{stopC: make(chan struct{})}
}

func (p *provider) Enabled() bool { return false }
func (p *provider) Start(_ context.Context) error  { <-p.stopC; return nil }
func (p *provider) Stop(_ context.Context) error   { close(p.stopC); return nil }
func (p *provider) Send(_ context.Context, _ ...analyticstypes.Message) {}
func (p *provider) TrackGroup(_ context.Context, _, _ string, _ map[string]any)     {}
func (p *provider) TrackUser(_ context.Context, _, _, _ string, _ map[string]any)    {}
func (p *provider) IdentifyGroup(_ context.Context, _ string, _ map[string]any)      {}
func (p *provider) IdentifyUser(_ context.Context, _, _ string, _ map[string]any)    {}
