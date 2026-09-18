package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sync"
)

type capturedRequest struct {
	Path string `json:"path"`
	Body string `json:"body"`
}

var (
	requestsMu sync.Mutex
	requests   = make([]capturedRequest, 0)
)

func capture(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "could not read body", http.StatusBadRequest)
		return
	}

	requestsMu.Lock()
	requests = append(requests, capturedRequest{Path: r.URL.Path, Body: string(body)})
	requestsMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func listRequests(w http.ResponseWriter, _ *http.Request) {
	requestsMu.Lock()
	snapshot := make([]capturedRequest, len(requests))
	copy(snapshot, requests)
	requestsMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(snapshot); err != nil {
		log.Printf("encode captured requests: %v", err)
	}
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/e2e", capture)
	mux.HandleFunc("/requests", listRequests)
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	log.Fatal(http.ListenAndServe(":8080", mux))
}
