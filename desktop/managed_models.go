package main

import "strings"

// The integrated launcher delegates model ownership to Desktop so model
// replacement and shutdown use the same process handles．No capture starts．
func (a *App) startManagedModels(roles string) {
	starters := map[string]func() (*RuntimeStatus, error){
		"fast": a.StartFast, "work": a.StartWork, "code": a.StartCode, "embedding": a.StartEmbedding,
	}
	for _, role := range strings.Split(roles, ",") {
		start, ok := starters[role]
		if !ok {
			continue
		}
		if _, err := start(); err != nil {
			a.mu.Lock()
			a.appendGatewayLog("managed model startup failed (" + role + "): " + err.Error())
			a.mu.Unlock()
		}
	}
}
