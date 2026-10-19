package main

import (
	"net/http"
	"time"
)

// Ordinary reasoning and continued answers share one finite generation allowance．
// Ordinary control requests keep their shorter timeout．Cancellation still
// closes the active request immediately through its context．Explicit planned
// web searches select their context-only policy in clientForPayload．
const generationTimeout = 10 * time.Minute

func (a *App) clientForPath(path string) *http.Client {
	if path != "/v1/chat/completions" && path != "/v1/rag/query" {
		return a.httpClient
	}
	client := *a.httpClient
	client.Timeout = generationTimeout
	return &client
}
