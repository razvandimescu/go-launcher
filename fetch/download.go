// Package fetch provides built-in Fetcher implementations for go-launcher.
package fetch

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// newDefaultClient bounds the points where an unreachable host would otherwise
// stall forever. Deliberately no overall Client.Timeout: that would cap the
// transfer itself, and a release binary may legitimately take minutes.
func newDefaultClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
}

// downloadHTTP downloads from url into dst, reporting progress via the callback.
// contentLength is used for progress calculation; pass -1 if unknown.
func downloadHTTP(ctx context.Context, url string, dst io.Writer, client *http.Client, progress func(float64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var reader io.Reader = resp.Body

	if progress != nil && resp.ContentLength > 0 {
		reader = &progressReader{
			reader:   resp.Body,
			total:    resp.ContentLength,
			progress: progress,
		}
	}

	_, err = io.Copy(dst, reader)
	if err != nil {
		return fmt.Errorf("copy: %w", err)
	}

	if progress != nil {
		progress(1.0)
	}

	return nil
}

type progressReader struct {
	reader   io.Reader
	total    int64
	read     int64
	progress func(float64)
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	pr.read += int64(n)
	if pr.total > 0 {
		pr.progress(float64(pr.read) / float64(pr.total))
	}
	return n, err
}
