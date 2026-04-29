package main

import "testing"

func TestCorpusFailureThresholdExceeded(t *testing.T) {
	tests := []struct {
		name     string
		total    int
		failures int
		notFound int
		want     bool
	}{
		{
			name:     "allows clean build",
			total:    100,
			failures: 0,
			notFound: 0,
			want:     false,
		},
		{
			name:     "allows limited non-404 failures",
			total:    100,
			failures: 10,
			notFound: 0,
			want:     false,
		},
		{
			name:     "blocks too many total failures",
			total:    100,
			failures: 11,
			notFound: 0,
			want:     true,
		},
		{
			name:     "blocks too many 404s",
			total:    100,
			failures: 0,
			notFound: 6,
			want:     true,
		},
		{
			name:     "blocks combined failures and 404s",
			total:    100,
			failures: 8,
			notFound: 3,
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := corpusFailureThresholdExceeded(tt.total, tt.failures, tt.notFound)
			if got != tt.want {
				t.Fatalf("corpusFailureThresholdExceeded(%d, %d, %d) = %v, want %v", tt.total, tt.failures, tt.notFound, got, tt.want)
			}
		})
	}
}
