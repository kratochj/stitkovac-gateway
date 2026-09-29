package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"
)

var platformPattern = regexp.MustCompile(`^[a-z0-9]+-[a-z0-9]+$`)

// Download uses a provisioned HTTPS origin, never a URL from a cloud command or
// manifest. Fixed version/platform paths and disabled redirects bind both files
// to that origin. The caller supplies only an explicit stable version.
func (s *Store) Download(ctx context.Context, repository, version string, transport http.RoundTripper) (Manifest, error) {
	origin, err := url.Parse(repository)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || origin.Path != "" && origin.Path != "/" || !versionPattern.MatchString(version) || !platformPattern.MatchString(s.platform) {
		return Manifest{}, errors.New("invalid release repository or version")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	origin.Path = "/releases/" + version + "/" + s.platform + "/"
	origin.RawPath = ""
	client := &http.Client{Transport: transport, Timeout: 3 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("release redirects are forbidden") }}
	get := func(name string) (*http.Response, error) {
		delay := 250 * time.Millisecond
		for {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin.String()+name, nil)
			if err != nil {
				return nil, err
			}
			request.Header.Set("User-Agent", "Stitkovac-Gateway-Updater/1")
			response, err := client.Do(request)
			if err != nil {
				return nil, errors.New("release download failed")
			}
			if response.StatusCode == http.StatusOK {
				return response, nil
			}
			retry := response.StatusCode == 429 || response.StatusCode == 502 || response.StatusCode == 503 || response.StatusCode == 504
			response.Body.Close()
			if !retry {
				return nil, fmt.Errorf("release repository rejected request (%d)", response.StatusCode)
			}
			// A fleet rollout can saturate the bounded hosting slots. Retry downloads,
			// never the command queue, with jitter and one deadline for both files.
			wait := delay/2 + time.Duration(rand.Int64N(int64(delay/2)+1))
			if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 && seconds <= 30 {
				wait += time.Duration(seconds) * time.Second
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
			delay = min(delay*2, 5*time.Second)
		}
	}

	response, err := get("manifest.json")
	if err != nil {
		return Manifest{}, err
	}
	b, err := io.ReadAll(io.LimitReader(response.Body, MaxManifest+1))
	response.Body.Close()
	if err != nil {
		return Manifest{}, errors.New("release manifest download interrupted")
	}
	m, err := Verify(b, s.keys, s.platform)
	if err != nil {
		return Manifest{}, err
	}
	if m.Version != version {
		return Manifest{}, errors.New("repository returned another release version")
	}
	response, err = get("gateway")
	if err != nil {
		return Manifest{}, err
	}
	defer response.Body.Close()
	if response.ContentLength >= 0 && response.ContentLength != m.Size {
		return Manifest{}, errors.New("release download length mismatch")
	}
	return s.Stage(b, response.Body)
}
