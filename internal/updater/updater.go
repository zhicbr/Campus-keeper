package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultRepo         = "zhicbr/net-keeper"
	DefaultReleasesAPI  = "https://api.github.com/repos/" + DefaultRepo + "/releases/latest"
	DefaultReleasesPage = "https://github.com/" + DefaultRepo + "/releases/latest"
)

// ReleaseInfo describes a GitHub Release returned by the update check.
type ReleaseInfo struct {
	Version     string
	ReleaseURL  string
	DownloadURL string
	Notes       string
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type githubRelease struct {
	TagName    string        `json:"tag_name"`
	HTMLURL    string        `json:"html_url"`
	Body       string        `json:"body"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	Assets     []githubAsset `json:"assets"`
}

// Client queries GitHub Releases for newer versions.
type Client struct {
	apiURL     string
	httpClient *http.Client
	userAgent  string
}

// NewClient creates a release update checker for the repository.
func NewClient(currentVersion string) *Client {
	return &Client{
		apiURL: DefaultReleasesAPI,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		userAgent: "CampusKeeper/" + strings.TrimPrefix(strings.TrimSpace(currentVersion), "v"),
	}
}

// SetAPIURL overrides the GitHub API URL (used in tests).
func (c *Client) SetAPIURL(url string) {
	c.apiURL = url
}

// SetHTTPClient overrides the HTTP client (used in tests).
func (c *Client) SetHTTPClient(client *http.Client) {
	c.httpClient = client
}

// CheckLatest queries GitHub's latest release and returns whether a newer version exists.
func (c *Client) CheckLatest(ctx context.Context, currentVersion string) (*ReleaseInfo, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, false, fmt.Errorf("GitHub API HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return nil, false, err
	}

	var rel githubRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, false, err
	}
	if rel.Draft || strings.TrimSpace(rel.TagName) == "" {
		return nil, false, nil
	}

	releaseURL := strings.TrimSpace(rel.HTMLURL)
	if releaseURL == "" {
		releaseURL = DefaultReleasesPage
	}
	downloadURL := releaseURL
	for _, a := range rel.Assets {
		if strings.HasSuffix(strings.ToLower(a.Name), ".exe") && a.BrowserDownloadURL != "" {
			downloadURL = a.BrowserDownloadURL
			break
		}
	}

	info := &ReleaseInfo{
		Version:     strings.TrimSpace(rel.TagName),
		ReleaseURL:  releaseURL,
		DownloadURL: downloadURL,
		Notes:       strings.TrimSpace(rel.Body),
	}
	return info, IsNewerVersion(info.Version, currentVersion), nil
}

// IsNewerVersion reports whether candidate is strictly newer than current using semver (major.minor.patch).
func IsNewerVersion(candidate, current string) bool {
	cParts, cOk := parseSemver(candidate)
	curParts, curOk := parseSemver(current)
	if !cOk || !curOk {
		return false
	}
	for i := 0; i < 3; i++ {
		if cParts[i] > curParts[i] {
			return true
		}
		if cParts[i] < curParts[i] {
			return false
		}
	}
	return false
}

func parseSemver(raw string) ([3]int, bool) {
	var out [3]int
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	if idx := strings.IndexAny(s, "-+"); idx >= 0 {
		s = s[:idx]
	}
	if s == "" {
		return out, false
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		parts = parts[:3]
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
