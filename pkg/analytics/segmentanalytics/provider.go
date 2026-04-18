package segmentanalytics

import (
	"context"

	segment "github.com/segmentio/analytics-go/v3"
	"go.uber.org/zap"

	"github.com/SigNoz/signoz-mcp-server/pkg/analytics"
	"github.com/SigNoz/signoz-mcp-server/pkg/types/analyticstypes"
)

type provider struct {
	client segment.Client
	logger *zap.Logger
	stopC  chan struct{}
}

func New(logger *zap.Logger, cfg analytics.Config) (analytics.Analytics, error) {
	client, err := segment.NewWithConfig(cfg.Segment.Key, segment.Config{
		Logger: newSegmentLogger(logger),
	})
	if err != nil {
		return nil, err
	}
	return &provider{
		client: client,
		logger: logger,
		stopC:  make(chan struct{}),
	}, nil
}

func (p *provider) Enabled() bool { return true }

func (p *provider) Start(_ context.Context) error {
	<-p.stopC
	return nil
}

func (p *provider) Stop(_ context.Context) error {
	if err := p.client.Close(); err != nil {
		p.logger.Error("failed to close segment client", zap.Error(err))
	}
	close(p.stopC)
	return nil
}

func (p *provider) Send(_ context.Context, messages ...analyticstypes.Message) {
	for _, msg := range messages {
		if err := p.client.Enqueue(msg); err != nil {
			p.logger.Error("failed to enqueue segment message", zap.Error(err))
		}
	}
}

func (p *provider) TrackGroup(_ context.Context, group, event string, properties map[string]any) {
	if properties == nil {
		return
	}
	if err := p.client.Enqueue(analyticstypes.Track{
		UserId:     group,
		Event:      event,
		Properties: analyticstypes.NewPropertiesFromMap(properties),
		Context: &analyticstypes.Context{
			Extra: map[string]interface{}{
				analyticstypes.KeyGroupID: group,
			},
		},
	}); err != nil {
		p.logger.Error("failed to enqueue TrackGroup", zap.String("event", event), zap.Error(err))
	}
}

func (p *provider) TrackUser(_ context.Context, group, user, event string, properties map[string]any) {
	if properties == nil {
		return
	}
	if err := p.client.Enqueue(analyticstypes.Track{
		UserId:     user,
		Event:      event,
		Properties: analyticstypes.NewPropertiesFromMap(properties),
		Context: &analyticstypes.Context{
			Extra: map[string]interface{}{
				analyticstypes.KeyGroupID: group,
			},
		},
	}); err != nil {
		p.logger.Error("failed to enqueue TrackUser", zap.String("event", event), zap.Error(err))
	}
}

func (p *provider) IdentifyGroup(_ context.Context, group string, traits map[string]any) {
	if traits == nil {
		return
	}
	if err := p.client.Enqueue(analyticstypes.Identify{
		UserId: group,
		Traits: analyticstypes.NewTraitsFromMap(traits),
	}); err != nil {
		p.logger.Error("failed to enqueue IdentifyGroup Identify", zap.Error(err))
	}
	if err := p.client.Enqueue(analyticstypes.Group{
		UserId:  group,
		GroupId: group,
		Traits:  analyticstypes.NewTraitsFromMap(traits),
	}); err != nil {
		p.logger.Error("failed to enqueue IdentifyGroup Group", zap.Error(err))
	}
}

func (p *provider) IdentifyUser(_ context.Context, group, user string, traits map[string]any) {
	if traits == nil {
		return
	}
	if err := p.client.Enqueue(analyticstypes.Identify{
		UserId: user,
		Traits: analyticstypes.NewTraitsFromMap(traits),
	}); err != nil {
		p.logger.Error("failed to enqueue IdentifyUser Identify", zap.Error(err))
	}
	if err := p.client.Enqueue(analyticstypes.Group{
		UserId:  user,
		GroupId: group,
		Traits:  analyticstypes.NewTraits().Set("id", group),
	}); err != nil {
		p.logger.Error("failed to enqueue IdentifyUser Group", zap.Error(err))
	}
}
