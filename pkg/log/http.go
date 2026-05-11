package log

import (
	"log/slog"
	"net/http"

	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

// HTTPRequestAttrs returns stable request metadata for logs. It intentionally
// excludes query strings and auth material.
func HTTPRequestAttrs(r *http.Request) []slog.Attr {
	return httpRequestAttrs(r, false)
}

// MCPHTTPRequestAttrs returns stable request metadata plus the MCP session
// header. Use it only on /mcp-scoped request logs.
func MCPHTTPRequestAttrs(r *http.Request) []slog.Attr {
	return httpRequestAttrs(r, true)
}

func httpRequestAttrs(r *http.Request, includeMCPSession bool) []slog.Attr {
	if r == nil {
		return nil
	}

	attrs := []slog.Attr{
		slog.String("http.request.method", r.Method),
	}
	if r.URL != nil && r.URL.Path != "" {
		attrs = append(attrs, slog.String("url.path", r.URL.Path))
	}
	if serverAddress := util.HTTPServerAddress(r); serverAddress != "" {
		attrs = append(attrs, slog.String("server.address", serverAddress))
	}
	if clientAddress := util.HTTPClientAddress(r); clientAddress != "" {
		attrs = append(attrs, slog.String("client.address", clientAddress))
	}
	if userAgent := util.HTTPUserAgent(r); userAgent != "" {
		attrs = append(attrs, slog.String("user_agent.original", userAgent))
	}
	if sessionID := util.HTTPSessionID(r); includeMCPSession && sessionID != "" {
		attrs = append(attrs, slog.String("mcp.session.id", sessionID))
	}
	return attrs
}
