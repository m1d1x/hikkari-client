package downdetector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Sender periodically sends accumulated downdetector reports from the local
// outbox to the central hub endpoint.
type Sender struct {
	Agent    *Agent
	Endpoint string
	Token    string
	Client   *http.Client
	Interval time.Duration

	minBackoff time.Duration
	maxBackoff time.Duration
}

// NewSender constructs a sender with a default interval of 60 seconds.
func NewSender(agent *Agent, endpoint, token string, client *http.Client) *Sender {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Sender{
		Agent:    agent,
		Endpoint: endpoint,
		Token:    token,
		Client:   client,
		Interval: 60 * time.Second,
	}
}

func (s *Sender) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return http.DefaultClient
}

func (s *Sender) getMinBackoff() time.Duration {
	if s.minBackoff > 0 {
		return s.minBackoff
	}
	if s.Interval > 0 && s.Interval < time.Second {
		return s.Interval
	}
	return time.Second
}

func (s *Sender) getMaxBackoff() time.Duration {
	if s.maxBackoff > 0 {
		return s.maxBackoff
	}
	maxB := 60 * time.Second
	if s.Interval > maxB {
		maxB = s.Interval
	}
	return maxB
}

type httpStatusError struct {
	StatusCode int
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("HTTP %d", e.StatusCode)
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var httpErr *httpStatusError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == http.StatusTooManyRequests || httpErr.StatusCode >= 500
	}
	return true
}

func (s *Sender) sendReport(ctx context.Context, r Report) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}

	resp, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		return nil
	}
	return &httpStatusError{StatusCode: resp.StatusCode}
}

// Run executes the background send loop until ctx is canceled.
func (s *Sender) Run(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	interval := s.Interval
	if interval <= 0 {
		interval = 60 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	minBackoff := s.getMinBackoff()
	maxBackoff := s.getMaxBackoff()
	backoff := minBackoff

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if s.Agent == nil || s.Endpoint == "" {
			continue
		}

		failed := false
		for {
			reports := s.Agent.DrainOutbox(5)
			if len(reports) == 0 {
				break
			}
			for _, r := range reports {
				if err := s.sendReport(ctx, r); err != nil {
					failed = true
					if isRetryable(err) {
						select {
						case <-ctx.Done():
							return
						case <-time.After(backoff):
						}
						backoff = min(backoff*2, maxBackoff)
					}
					break
				}
				s.Agent.AckOutbox(1)
			}
			if failed {
				break
			}
		}
		if !failed {
			backoff = minBackoff
		}
	}
}
