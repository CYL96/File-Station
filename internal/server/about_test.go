package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type releaseRoundTripper func(*http.Request) (*http.Response, error)

func (f releaseRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func localReleaseClient(t *testing.T, handler http.HandlerFunc) *http.Client {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{
		Timeout:       time.Second,
		CheckRedirect: releaseHTTPClient.CheckRedirect,
		Transport: releaseRoundTripper(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodGet || r.URL.String() != latestReleaseAPI {
				t.Errorf("unexpected GitHub request: %s %s", r.Method, r.URL)
			}
			if r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("User-Agent") != "File-Station/"+softwareVersion {
				t.Errorf("missing GitHub request headers: %v", r.Header)
			}
			if _, ok := r.Context().Deadline(); !ok {
				t.Error("GitHub request has no deadline")
			}
			forward := r.Clone(r.Context())
			forward.URL.Scheme = target.Scheme
			forward.URL.Host = target.Host
			return http.DefaultTransport.RoundTrip(forward)
		}),
	}
}

func TestLatestReleaseVersionStates(t *testing.T) {
	cases := []struct {
		name    string
		current string
		tag     string
		status  string
		tagLink bool
	}{
		{"newer minor numerically", "1.9.0", "v1.10.0", "update-available", true},
		{"older minor numerically", "1.10.0", "v1.9.0", "up-to-date", true},
		{"same version", "1.0.0", "v1.0.0", "up-to-date", true},
		{"higher major wins", "1.99.99", "2.0.0", "update-available", true},
		{"lower major stays current", "2.0.0", "1.99.99", "up-to-date", true},
		{"newer patch", "1.0.0", "1.0.1", "update-available", true},
		{"older patch", "1.0.1", "1.0.0", "up-to-date", true},
		{"current v prefix", "v1.0.0", "1.0.1", "update-available", true},
		{"current prerelease unknown", "1.0.0-rc.1", "1.0.0", "unknown", true},
		{"release prerelease unknown", "1.0.0", "1.1.0-rc.1", "unknown", false},
		{"build metadata unknown", "1.0.0", "1.1.0+build", "unknown", false},
		{"invalid installed version", "development", "1.0.1", "unknown", true},
		{"non-semver tag", "1.0.0", "latest", "unknown", false},
		{"missing patch", "1.0.0", "2.0", "unknown", false},
		{"extra component", "1.0.0", "2.0.0.0", "unknown", false},
		{"leading zero", "1.0.0", "01.0.0", "unknown", false},
		{"overflow", "1.0.0", "18446744073709551616.0.0", "unknown", false},
		{"unsafe tag", "1.0.0", "../../evil?next=https://evil.test", "unknown", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := localReleaseClient(t, func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": tc.tag, "draft": false, "prerelease": false, "html_url": "https://evil.test/release"})
			})
			actual, err := latestRelease(context.Background(), client, tc.current)
			if err != nil {
				t.Fatal(err)
			}
			wantURL := repositoryURL + "/releases"
			if tc.tagLink {
				wantURL += "/tag/" + url.PathEscape(tc.tag)
			}
			want := updateResponse{CurrentVersion: tc.current, LatestVersion: tc.tag, Status: tc.status, ReleaseURL: wantURL}
			if actual != want {
				t.Fatalf("release response = %+v, want %+v", actual, want)
			}
		})
	}
}

func TestLatestReleaseUpstreamResponses(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		headers   map[string]string
		wantError bool
	}{
		{name: "no release", status: 404, body: `{"message":"Not Found"}`},
		{name: "rate limited", status: 429, wantError: true},
		{name: "exhausted quota", status: 403, headers: map[string]string{"X-RateLimit-Remaining": "0"}, wantError: true},
		{name: "secondary quota", status: 403, headers: map[string]string{"Retry-After": "60"}, wantError: true},
		{name: "forbidden", status: 403, wantError: true},
		{name: "GitHub failure", status: 503, wantError: true},
		{name: "malformed JSON", status: 200, body: `{`, wantError: true},
		{name: "missing tag", status: 200, body: `{"draft":false,"prerelease":false}`, wantError: true},
		{name: "null JSON", status: 200, body: `null`, wantError: true},
		{name: "missing stable flags", status: 200, body: `{"tag_name":"v2.0.0"}`, wantError: true},
		{name: "draft rejected", status: 200, body: `{"tag_name":"v2.0.0","draft":true,"prerelease":false}`, wantError: true},
		{name: "prerelease rejected", status: 200, body: `{"tag_name":"v2.0.0","draft":false,"prerelease":true}`, wantError: true},
		{name: "wrong field type", status: 200, body: `{"tag_name":12,"draft":false,"prerelease":false}`, wantError: true},
		{name: "trailing JSON", status: 200, body: `{"tag_name":"v2.0.0","draft":false,"prerelease":false}{}`, wantError: true},
		{name: "oversized response", status: 200, body: `{"tag_name":"v2.0.0","draft":false,"prerelease":false,"body":"` + strings.Repeat("x", maxReleaseResponseBytes) + `"}`, wantError: true},
		{name: "oversized trailing whitespace", status: 200, body: `{"tag_name":"v2.0.0","draft":false,"prerelease":false}` + strings.Repeat(" ", maxReleaseResponseBytes), wantError: true},
		{name: "redirect rejected", status: 302, headers: map[string]string{"Location": "https://evil.test"}, wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := localReleaseClient(t, func(w http.ResponseWriter, r *http.Request) {
				for key, value := range tc.headers {
					w.Header().Set(key, value)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			actual, err := latestRelease(context.Background(), client, softwareVersion)
			if !tc.wantError {
				if err != nil {
					t.Fatal(err)
				}
				want := updateResponse{CurrentVersion: softwareVersion, Status: "unreleased", ReleaseURL: repositoryURL + "/releases"}
				if actual != want {
					t.Fatalf("unreleased = %+v, want %+v", actual, want)
				}
				return
			}
			var apiErr *apiError
			if !errors.As(err, &apiErr) || apiErr.status != http.StatusBadGateway {
				t.Fatalf("release error = %v, want HTTP 502", err)
			}
		})
	}
}

func TestLatestReleaseNetworkAndContextErrors(t *testing.T) {
	cases := []struct {
		name           string
		waitForContext bool
		status         int
	}{
		{"network failure", false, http.StatusBadGateway},
		{"deadline", true, http.StatusGatewayTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			client := &http.Client{Transport: releaseRoundTripper(func(r *http.Request) (*http.Response, error) {
				if tc.waitForContext {
					<-r.Context().Done()
					return nil, r.Context().Err()
				}
				return nil, errors.New("network unavailable")
			})}
			_, err := latestRelease(ctx, client, softwareVersion)
			var apiErr *apiError
			if !errors.As(err, &apiErr) || apiErr.status != tc.status {
				t.Fatalf("release error = %v, want HTTP %d", err, tc.status)
			}
		})
	}
	t.Run("caller cancellation propagates", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		client := &http.Client{Transport: releaseRoundTripper(func(r *http.Request) (*http.Response, error) {
			if !errors.Is(r.Context().Err(), context.Canceled) {
				t.Error("caller cancellation did not reach GitHub request")
			}
			return nil, r.Context().Err()
		})}
		if _, err := latestRelease(ctx, client, softwareVersion); err == nil {
			t.Fatal("canceled update check succeeded")
		}
	})
}

func TestAboutAndUpdateRoutes(t *testing.T) {
	var requests atomic.Int32
	client := localReleaseClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNotFound)
	})
	originalClient := releaseHTTPClient
	t.Cleanup(func() { releaseHTTPClient = originalClient })
	releaseHTTPClient = client
	f := newFileSettingsFixture(t)
	releaseHTTPClient = originalClient
	for _, path := range []string{"/api/settings/about", "/api/settings/update"} {
		f.requestWithAuth("GET", path, "", http.DefaultClient, "", f.srv.URL, http.StatusUnauthorized)
	}
	if requests.Load() != 0 {
		t.Fatal("unauthenticated update request reached GitHub")
	}
	var about aboutResponse
	if err := json.Unmarshal(f.request("GET", "/api/settings/about", "", http.StatusOK), &about); err != nil {
		t.Fatal(err)
	}
	if about.Version == "" || about.Version != strings.TrimSpace(embeddedVersion) || about.RepositoryURL != repositoryURL {
		t.Fatalf("unexpected about metadata: %+v", about)
	}
	if requests.Load() != 0 {
		t.Fatal("opening About unexpectedly checked GitHub")
	}
	var update updateResponse
	if err := json.Unmarshal(f.request("GET", "/api/settings/update", "", http.StatusOK), &update); err != nil {
		t.Fatal(err)
	}
	if update.CurrentVersion != about.Version || update.LatestVersion != "" || update.Status != "unreleased" || update.ReleaseURL != repositoryURL+"/releases" || requests.Load() != 1 {
		t.Fatalf("unexpected update metadata: %+v, GitHub requests = %d", update, requests.Load())
	}
}
