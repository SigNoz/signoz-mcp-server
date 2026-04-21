package segmentanalytics

import (
	"context"
	"log/slog"

	segment "github.com/segmentio/analytics-go/v3"

	"github.com/SigNoz/signoz-mcp-server/pkg/analytics"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/types/analyticstypes"
)

type provider struct {
	client segment.Client
	logger *slog.Logger
	stopC  chan struct{}
}

func New(logger *slog.Logger, cfg analytics.Config) (analytics.Analytics, error) {
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

func (p *provider) Stop(ctx context.Context) error {
	if err := p.client.Close(); err != nil {
		p.logger.ErrorContext(ctx, "failed to close segment client", logpkg.ErrAttr(err))
	}
	close(p.stopC)
	return nil
}

func (p *provider) Send(ctx context.Context, messages ...analyticstypes.Message) {
	for _, msg := range messages {
		if err := p.client.Enqueue(msg); err != nil {
			p.logger.ErrorContext(ctx, "failed to enqueue segment message", logpkg.ErrAttr(err))
		}
	}
}

func (p *provider) TrackUser(ctx context.Context, group, user, event string, properties map[string]any) {
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
		p.logger.ErrorContext(ctx, "failed to enqueue TrackUser",
			slog.String("event", event),
			logpkg.ErrAttr(err))
	}
}

func (p *provider) IdentifyUser(ctx context.Context, group, user string, traits map[string]any) {
	if traits == nil {
		return
	}
	if err := p.client.Enqueue(analyticstypes.Identify{
		UserId: user,
		Traits: analyticstypes.NewTraitsFromMap(traits),
	}); err != nil {
		p.logger.ErrorContext(ctx, "failed to enqueue IdentifyUser Identify", logpkg.ErrAttr(err))
	}
	if err := p.client.Enqueue(analyticstypes.Group{
		UserId:  user,
		GroupId: group,
		Traits:  analyticstypes.NewTraits().Set("id", group),
	}); err != nil {
		p.logger.ErrorContext(ctx, "failed to enqueue IdentifyUser Group", logpkg.ErrAttr(err))
	}
}
