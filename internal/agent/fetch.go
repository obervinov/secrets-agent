package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	fetchAttempts = 3
	maxPayload    = 1 << 20
	fetchTimeout  = 20 * time.Second
)

// Fetcher retrieves the payload from the secrets proxy.
type Fetcher struct {
	Client  *http.Client
	URL     string
	Headers map[string]string
	Log     Logger
}

func NewFetcher(cfg *Config, log Logger) *Fetcher {
	return &Fetcher{
		Client:  &http.Client{Timeout: fetchTimeout},
		URL:     cfg.URL,
		Headers: cfg.AuthHeaders,
		Log:     log,
	}
}

// Fetch returns the normalised payload. A 4xx other than 429 is permanent — wrong
// credentials or a wrong path — so it is not retried.
func (f *Fetcher) Fetch(ctx context.Context) (Values, error) {
	var lastErr error

	for attempt := 1; attempt <= fetchAttempts; attempt++ {
		values, permanent, err := f.attempt(ctx)
		if err == nil {
			return values, nil
		}
		lastErr = err
		if permanent {
			return nil, err
		}
		f.Log.Warnf("fetch attempt %d/%d failed: %v", attempt, fetchAttempts, err)
		if attempt < fetchAttempts {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
	}
	return nil, lastErr
}

func (f *Fetcher) attempt(ctx context.Context) (Values, bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return nil, true, err
	}
	for name, value := range f.Headers {
		request.Header.Set(name, value)
	}

	response, err := f.Client.Do(request)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxPayload))
	if err != nil {
		return nil, false, err
	}

	if response.StatusCode != http.StatusOK {
		// The proxy puts a diagnostic in the body. Without it the journal shows only a
		// status code and the cause is unguessable — the body of an error response is
		// the proxy's own message, not secret material.
		permanent := response.StatusCode >= 400 && response.StatusCode < 500 &&
			response.StatusCode != http.StatusTooManyRequests
		return nil, permanent, fmt.Errorf("HTTP %d: %s", response.StatusCode, firstLine(body))
	}

	values, err := Decode(body)
	if err != nil {
		return nil, true, err
	}
	return values, false, nil
}

func firstLine(body []byte) string {
	const limit = 200
	text := string(body)
	if index := indexAny(text, "\r\n"); index >= 0 {
		text = text[:index]
	}
	if len(text) > limit {
		text = text[:limit]
	}
	return text
}

func indexAny(text, chars string) int {
	for index, rune := range text {
		for _, candidate := range chars {
			if rune == candidate {
				return index
			}
		}
	}
	return -1
}
