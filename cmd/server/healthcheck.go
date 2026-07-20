package main

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

const healthcheckURL = "http://127.0.0.1:8000/livez"

func runHealthcheck() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	return checkLiveness(ctx, http.DefaultClient, healthcheckURL)
}

func checkLiveness(ctx context.Context, client *http.Client, endpoint string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create liveness request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request liveness endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("liveness endpoint returned %s", resp.Status)
	}

	return nil
}
