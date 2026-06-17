package mta

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type Client struct {
	URL        string
	HTTPClient *http.Client
}

func NewClient(url string) *Client {
	if url == "" {
		url = os.Getenv("MTA_FEED_URL")
	}
	if url == "" {
		url = DefaultFeedURL
	}
	return &Client{
		URL: url,
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *Client) Fetch(ctx context.Context) (Feed, error) {
	var reader io.Reader

	if strings.HasPrefix(c.URL, "file://") {
		path := strings.TrimPrefix(c.URL, "file://")
		f, err := os.Open(path)
		if err != nil {
			return Feed{}, fmt.Errorf("open fixture: %w", err)
		}
		defer f.Close()
		reader = f
	} else {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
		if reqErr != nil {
			return Feed{}, reqErr
		}
		req.Header.Set("User-Agent", "mta-ssh/1.0")

		resp, doErr := c.HTTPClient.Do(req)
		if doErr != nil {
			return Feed{}, fmt.Errorf("fetch alerts: %w", doErr)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return Feed{}, fmt.Errorf("fetch alerts: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		reader = resp.Body
	}

	var feed Feed
	if err := json.NewDecoder(reader).Decode(&feed); err != nil {
		return Feed{}, fmt.Errorf("decode alerts: %w", err)
	}
	return feed, nil
}
