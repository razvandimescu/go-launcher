package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	launcher "github.com/razvandimescu/go-launcher"
)

type httpFetcher struct {
	releaseURL string
	client     *http.Client
}

// HTTP returns a Fetcher that checks a plain HTTP endpoint for the latest
// version. The endpoint must return JSON matching the launcher.Release struct.
// The Download URL is taken from the returned Release.URL field.
func HTTP(releaseURL string, opts ...Option) launcher.Fetcher {
	f := &httpFetcher{
		releaseURL: releaseURL,
		client:     newDefaultClient(),
	}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

func (f *httpFetcher) LatestVersion(ctx context.Context) (*launcher.Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.releaseURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch latest version: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http returned %d", resp.StatusCode)
	}

	var release launcher.Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}

	if release.URL == "" {
		return nil, fmt.Errorf("release has no download URL")
	}

	return &release, nil
}

func (f *httpFetcher) Download(ctx context.Context, release *launcher.Release, dst io.Writer, progress func(float64)) error {
	return downloadHTTP(ctx, release.URL, dst, f.client, progress)
}
