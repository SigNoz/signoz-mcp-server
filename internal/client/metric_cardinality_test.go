package client

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetMetricCardinality_ContractCheck(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantWarn    bool
		wantErr     bool
	}{
		{
			name:     "valid shape — no warn",
			body:     `{"status":"success","data":{"attributes":[{"key":"k8s.pod.uid","valueCount":42,"values":["uid-1"]}],"totalKeys":1}}`,
			wantWarn: false,
		},
		{
			name:     "data field absent — warn",
			body:     `{"status":"success"}`,
			wantWarn: true,
		},
		{
			name:     "data present but attributes absent — warn",
			body:     `{"status":"success","data":{"items":[{"key":"k8s.pod.uid","valueCount":42}]}}`,
			wantWarn: true,
		},
		{
			name:     "data present, attributes null — warn",
			body:     `{"status":"success","data":{"attributes":null}}`,
			wantWarn: true,
		},
		{
			name:     "empty attributes array — no warn (valid zero-label metric)",
			body:     `{"status":"success","data":{"attributes":[],"totalKeys":0}}`,
			wantWarn: false,
		},
		{
			name:     "malformed JSON — warn",
			body:     `not json`,
			wantWarn: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			var buf bytes.Buffer
			logger := newBufferedLogger(&buf, -4) // DEBUG level captures WARN
			c := NewClient(logger, srv.URL, "test-api-key", "SIGNOZ-API-KEY", nil)

			result, err := c.GetMetricCardinality(context.Background(), "k8s.pod.uid", 0, 1000)

			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.NotEmpty(t, result)

			logged := buf.String()
			if tc.wantWarn {
				assert.Contains(t, logged, "Unexpected response shape", "expected WARN log for contract violation")
			} else {
				assert.NotContains(t, logged, "Unexpected response shape", "expected no WARN log for valid shape")
			}
		})
	}
}
