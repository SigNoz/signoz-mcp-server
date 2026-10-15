package mcp_server

import (
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics/noopanalytics"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// TestInitializeDoesNotAdvertiseResourceSubscribe pins the capability
// contract: this server registers resources but never implements
// resources/subscribe, so the initialize result must not advertise
// resources.subscribe — well-behaved clients should never attempt it.
func TestInitializeDoesNotAdvertiseResourceSubscribe(t *testing.T) {
	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	handler := tools.NewHandler(logpkg.New("error"), cfg)
	mcpServer := NewMCPServer(logpkg.New("error"), handler, cfg, noopanalytics.New(), nil)

	s := mcpServer.newSDKServer()
	// Register at least one resource so the resources capability is present
	// at all — the load-bearing assertion is that subscribe stays unset even
	// when resources are advertised.
	handler.RegisterQueryBuilderV5Handlers(s)

	client, err := newIntegrationClient(t, s)
	if err != nil {
		t.Fatal(err)
	}
	result := client.client.InitializeResult()
	if result.Capabilities.Resources == nil {
		t.Fatal("resources capability missing from initialize result")
	}
	if result.Capabilities.Resources.Subscribe {
		t.Fatal("initialize result advertises resources.subscribe")
	}
}
