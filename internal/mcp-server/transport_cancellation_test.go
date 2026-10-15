package mcp_server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestProductionHTTPModernRequestCancellationReachesHandler(t *testing.T) {
	logger := logpkg.New("error")
	cfg := &config.Config{URL: "https://tenant.example.com", APIKey: "test-key", ClientCacheSize: 1, ClientCacheTTL: time.Minute, MaxRequestBytes: 1 << 20}
	h := tools.NewHandler(logger, cfg)
	m := NewMCPServer(logger, h, cfg, nil, nil)
	server := m.newSDKServer()

	started := make(chan struct{})
	cancelled := make(chan error, 1)
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			if method != "tools/list" {
				return next(ctx, method, request)
			}
			close(started)
			<-ctx.Done()
			cancelled <- ctx.Err()
			return nil, ctx.Err()
		}
	})
	handler := m.buildHTTP(server).Handler

	ctx, cancel := context.WithCancel(context.Background())
	body := protocolRequestJSON(t, 301, "tools/list", modernParams(nil, nil))
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil).WithContext(ctx)
	req.Body = io.NopCloser(bytes.NewBufferString(body))
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for key, values := range modernHeaders("tools/list", "") {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("modern request did not reach handler")
	}
	cancel()
	select {
	case err := <-cancelled:
		if err != context.Canceled {
			t.Fatalf("handler context error = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("modern handler context was not cancelled")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP request did not return after cancellation")
	}
}

func TestProductionServerRunCancellationIsNormalizedByStdioWrapper(t *testing.T) {
	logger := logpkg.New("error")
	cfg := &config.Config{URL: "https://tenant.example.com", APIKey: "test-key", ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	m := NewMCPServer(logger, tools.NewHandler(logger, cfg), cfg, nil, nil)
	server := m.newSDKServer()

	clientToServerReader, clientToServerWriter := io.Pipe()
	serverToClientReader, serverToClientWriter := io.Pipe()
	t.Cleanup(func() {
		_ = clientToServerWriter.Close()
		_ = serverToClientReader.Close()
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runPersistentTransport(ctx, server, &mcp.IOTransport{Reader: clientToServerReader, Writer: serverToClientWriter})
	}()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("normalized stdio cancellation = %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server.Run did not return after stdio context cancellation")
	}
}
