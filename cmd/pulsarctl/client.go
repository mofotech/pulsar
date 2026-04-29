package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

type apiEnvelope struct {
	Data  json.RawMessage `json:"data"`
	Error *apiError       `json:"error,omitempty"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func do(method, url, token string, body interface{}) (json.RawMessage, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, url, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}

	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		// non-JSON body (e.g. healthz "ok")
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(raw))
		}
		return raw, nil
	}

	if env.Error != nil {
		return nil, fmt.Errorf("%s: %s", env.Error.Code, env.Error.Message)
	}
	return env.Data, nil
}

func apiURL(endpoint, path string) string {
	return endpoint + path
}

// resolve returns the active endpoint+token, preferring CLI flags over stored creds.
func resolve() (endpoint, token string, err error) {
	// Flags take precedence (set by PersistentPreRunE)
	if globalEndpoint != "" {
		endpoint = globalEndpoint
	}
	if globalToken != "" {
		token = globalToken
	}
	if endpoint != "" && token != "" {
		return
	}
	creds, cerr := loadCreds()
	if cerr != nil {
		if endpoint == "" || token == "" {
			err = cerr
			return
		}
	} else {
		if endpoint == "" {
			endpoint = creds.Endpoint
		}
		if token == "" {
			token = creds.Token
		}
	}
	return
}
