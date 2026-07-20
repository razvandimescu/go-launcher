package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	launcher "github.com/razvandimescu/go-launcher"
)

type githubFetcher struct {
	owner        string
	repo         string
	assetPattern string
	client       *http.Client
	apiURL       string // default: "https://api.github.com"
}

// Option configures a Fetcher.
type Option func(any)

// AssetPattern sets a glob-like pattern to match release asset filenames.
// Supports "*" as a wildcard. Example: "my-app-*-amd64"
func AssetPattern(pattern string) Option {
	return func(f any) {
		if g, ok := f.(*githubFetcher); ok {
			g.assetPattern = pattern
		}
	}
}

// WithHTTPClient sets a custom HTTP client for the fetcher.
func WithHTTPClient(c *http.Client) Option {
	return func(f any) {
		if g, ok := f.(*githubFetcher); ok {
			g.client = c
		}
		if h, ok := f.(*httpFetcher); ok {
			h.client = c
		}
	}
}

// WithAPIURL overrides the GitHub API base URL (for GitHub Enterprise).
func WithAPIURL(url string) Option {
	return func(f any) {
		if g, ok := f.(*githubFetcher); ok {
			g.apiURL = strings.TrimRight(url, "/")
		}
	}
}

// GitHubRelease returns a Fetcher that checks GitHub Releases for the latest
// version and downloads the matching asset.
func GitHubRelease(owner, repo string, opts ...Option) launcher.Fetcher {
	f := &githubFetcher{
		owner:  owner,
		repo:   repo,
		client: newDefaultClient(),
		apiURL: "https://api.github.com",
	}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func (f *githubFetcher) LatestVersion(ctx context.Context) (*launcher.Release, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", f.apiURL, f.owner, f.repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api returned %d", resp.StatusCode)
	}

	var release ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}

	asset, err := f.matchAsset(release.Assets)
	if err != nil {
		return nil, err
	}

	return &launcher.Release{
		Version: release.TagName,
		URL:     asset.BrowserDownloadURL,
	}, nil
}

func (f *githubFetcher) Download(ctx context.Context, release *launcher.Release, dst io.Writer, progress func(float64)) error {
	return downloadHTTP(ctx, release.URL, dst, f.client, progress)
}

func (f *githubFetcher) matchAsset(assets []ghAsset) (*ghAsset, error) {
	if len(assets) == 0 {
		return nil, fmt.Errorf("no assets in release")
	}

	if f.assetPattern == "" {
		return &assets[0], nil
	}

	for i := range assets {
		if matchPattern(f.assetPattern, assets[i].Name) {
			return &assets[i], nil
		}
	}

	return nil, fmt.Errorf("no asset matching pattern %q", f.assetPattern)
}

// matchPattern performs simple glob matching with "*" wildcard support.
func matchPattern(pattern, name string) bool {
	matched, err := path.Match(pattern, name)
	if err != nil {
		// Fall back to contains check if pattern is invalid
		return strings.Contains(name, strings.ReplaceAll(pattern, "*", ""))
	}
	return matched
}
