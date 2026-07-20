package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheckLiveness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		wantErr    bool
	}{
		{name: "live server", statusCode: http.StatusOK},
		{name: "not ready", statusCode: http.StatusServiceUnavailable, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
			}))
			t.Cleanup(server.Close)

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			t.Cleanup(cancel)

			err := checkLiveness(ctx, server.Client(), server.URL)
			if (err != nil) != tt.wantErr {
				t.Fatalf("checkLiveness() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}
