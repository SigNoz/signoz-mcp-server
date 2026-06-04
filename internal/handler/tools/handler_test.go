package tools

import (
	"context"
	"testing"

	expirable "github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

func ctxWith(apiKey, url string) context.Context {
	ctx := util.SetAPIKey(context.Background(), apiKey)
	ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
	return util.SetSigNozURL(ctx, url)
}

func TestGetClient_SharedAcrossAPIKeysPerURL(t *testing.T) {
	h := &Handler{
		logger:      logpkg.New("error"),
		clientCache: expirable.NewLRU[string, *signozclient.SigNoz](16, nil, 0),
	}

	c1, err := h.GetClient(ctxWith("key-a", "https://tenant.example.com"))
	require.NoError(t, err)
	c2, err := h.GetClient(ctxWith("key-b", "https://tenant.example.com"))
	require.NoError(t, err)
	assert.True(t, c1 == c2, "same URL must reuse one cached client regardless of API key")

	c3, err := h.GetClient(ctxWith("key-a", "https://other.example.com"))
	require.NoError(t, err)
	assert.False(t, c1 == c3, "different URLs must get different clients")
}

func TestGetClient_MissingCredentialsError(t *testing.T) {
	h := &Handler{
		logger:      logpkg.New("error"),
		clientCache: expirable.NewLRU[string, *signozclient.SigNoz](16, nil, 0),
	}
	_, err := h.GetClient(util.SetSigNozURL(context.Background(), "https://x.io")) // no apiKey
	assert.Error(t, err)
}
