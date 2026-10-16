package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	repositoryURL           = "https://github.com/CYL96/File-Station"
	latestReleaseAPI        = "https://api.github.com/repos/CYL96/File-Station/releases/latest"
	maxReleaseResponseBytes = 1 << 20
)

//go:embed version.txt
var embeddedVersion string

var softwareVersion = strings.TrimSpace(embeddedVersion)

var releaseHTTPClient = &http.Client{
	Timeout:       8 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

type aboutResponse struct {
	Version       string `json:"version"`
	RepositoryURL string `json:"repositoryUrl"`
}

type updateResponse struct {
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	Status         string `json:"status"`
	ReleaseURL     string `json:"releaseUrl"`
}

func (s *Server) aboutSettings(w http.ResponseWriter, r *http.Request) error {
	jsonResponse(w, aboutResponse{Version: softwareVersion, RepositoryURL: repositoryURL})
	return nil
}

// The client is private to this handler, allowing tests to use a local GitHub stand-in.
func updateSettings(client *http.Client) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		result, err := latestRelease(r.Context(), client, softwareVersion)
		if err != nil {
			return err
		}
		jsonResponse(w, result)
		return nil
	}
}

func latestRelease(ctx context.Context, client *http.Client, current string) (updateResponse, error) {
	result := updateResponse{CurrentVersion: current, Status: "unreleased", ReleaseURL: repositoryURL + "/releases"}
	// Keep a deadline even if a test or internal caller supplies a different client.
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseAPI, nil)
	if err != nil {
		return result, fail(http.StatusInternalServerError, "创建更新检查请求失败")
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "File-Station/"+softwareVersion)
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return result, fail(http.StatusGatewayTimeout, "检查更新超时，请稍后重试")
		}
		return result, fail(http.StatusBadGateway, "无法连接 GitHub 检查更新，请检查网络后重试")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return result, nil
	}
	if response.StatusCode == http.StatusTooManyRequests || (response.StatusCode == http.StatusForbidden && (response.Header.Get("X-RateLimit-Remaining") == "0" || response.Header.Get("Retry-After") != "")) {
		return result, fail(http.StatusBadGateway, "GitHub 请求次数已达上限，请稍后重试")
	}
	if response.StatusCode != http.StatusOK {
		return result, fail(http.StatusBadGateway, "GitHub 更新检查失败，请稍后重试")
	}
	var release struct {
		TagName    string `json:"tag_name"`
		Draft      *bool  `json:"draft"`
		Prerelease *bool  `json:"prerelease"`
	}
	limited := &io.LimitedReader{R: response.Body, N: maxReleaseResponseBytes + 1}
	decoder := json.NewDecoder(limited)
	if err = decoder.Decode(&release); err != nil {
		return result, fail(http.StatusBadGateway, "GitHub 返回的发布信息无效")
	}
	if decoder.Decode(&struct{}{}) != io.EOF || limited.N == 0 || release.TagName == "" || len(release.TagName) > 256 || release.Draft == nil || release.Prerelease == nil || *release.Draft || *release.Prerelease {
		return result, fail(http.StatusBadGateway, "GitHub 返回的发布信息无效")
	}
	result.LatestVersion = release.TagName
	result.Status = "unknown"
	latest, latestOK := stableVersion(release.TagName)
	installed, installedOK := stableVersion(current)
	if latestOK {
		// Never trust html_url from a remote response or put an arbitrary tag in a URL.
		result.ReleaseURL += "/tag/" + url.PathEscape(release.TagName)
	}
	if !latestOK || !installedOK {
		return result, nil
	}
	result.Status = "up-to-date"
	for i := range latest {
		if latest[i] > installed[i] {
			result.Status = "update-available"
			break
		}
		if latest[i] < installed[i] {
			break
		}
	}
	return result, nil
}

// Only stable major.minor.patch versions are comparable; unknown/prerelease
// versions must not be presented as current.
func stableVersion(version string) ([3]uint64, bool) {
	var parts [3]uint64
	version = strings.TrimPrefix(version, "v")
	for i := range parts {
		component, rest, found := strings.Cut(version, ".")
		if found != (i < len(parts)-1) || component == "" || (len(component) > 1 && component[0] == '0') {
			return parts, false
		}
		for _, digit := range component {
			if digit < '0' || digit > '9' {
				return parts, false
			}
		}
		number, err := strconv.ParseUint(component, 10, 64)
		if err != nil {
			return parts, false
		}
		parts[i] = number
		version = rest
	}
	return parts, true
}
