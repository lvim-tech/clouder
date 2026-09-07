package dropbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lvim-tech/clouder/internal/provider"
	"github.com/lvim-tech/clouder/internal/secret"
)

const (
	authorizeURL = "https://www.dropbox.com/oauth2/authorize"
	tokenURL     = "https://api.dropboxapi.com/oauth2/token"
	apiBase      = "https://api.dropboxapi.com/2"
	contentBase  = "https://content.dropboxapi.com/2"
)

// authFlow is one account's PKCE login in progress: the verifier is held
// between building the URL and exchanging the code. No client secret is used.
type authFlow struct {
	appKey    string
	account   string
	verifier  string
	challenge string
	secrets   secret.Store
}

// beginAuth starts a PKCE login for account.
func beginAuth(settings map[string]string, account string, secrets secret.Store) (provider.AuthFlow, error) {
	appKey := strings.TrimSpace(settings["app_key"])
	if appKey == "" {
		return nil, fmt.Errorf("no app_key in [providers.dropbox] — create a Dropbox app (PKCE, no secret) and set its key first")
	}
	verifier, err := randomVerifier()
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(verifier))
	return &authFlow{
		appKey:    appKey,
		account:   account,
		verifier:  verifier,
		challenge: base64.RawURLEncoding.EncodeToString(sum[:]),
		secrets:   secrets,
	}, nil
}

func (a *authFlow) URL() string {
	q := url.Values{}
	q.Set("client_id", a.appKey)
	q.Set("response_type", "code")
	q.Set("code_challenge", a.challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("token_access_type", "offline") // ask for a refresh token
	return authorizeURL + "?" + q.Encode()
}

func (a *authFlow) Exchange(ctx context.Context, code string) error {
	code = strings.TrimSpace(code)
	if code == "" {
		return fmt.Errorf("empty code")
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", a.appKey)
	form.Set("code_verifier", a.verifier)

	var tr struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := postForm(ctx, form, &tr); err != nil {
		return fmt.Errorf("token exchange: %w", err)
	}
	if tr.RefreshToken == "" {
		return fmt.Errorf("Dropbox returned no refresh token (was token_access_type=offline honored?)")
	}
	return a.secrets.Set(secret.Key("dropbox", a.account), tr.RefreshToken)
}

func randomVerifier() (string, error) {
	b := make([]byte, 64)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// postForm posts an application/x-www-form-urlencoded body to the token
// endpoint and decodes the JSON response into out.
func postForm(ctx context.Context, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, out)
}

// client is one account's authenticated HTTP access, minting short-lived
// access tokens from the long-lived refresh token as needed.
type client struct {
	appKey       string
	refreshToken string
	hc           *http.Client

	mu     sync.Mutex
	access string
	expiry time.Time
}

func newClient(appKey, refresh string) *client {
	return &client{appKey: appKey, refreshToken: refresh, hc: &http.Client{Timeout: 5 * time.Minute}}
}

// token returns a valid access token, refreshing when missing or near expiry.
func (c *client) token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.access != "" && time.Now().Before(c.expiry.Add(-60*time.Second)) {
		return c.access, nil
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", c.refreshToken)
	form.Set("client_id", c.appKey)
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := postForm(ctx, form, &tr); err != nil {
		return "", fmt.Errorf("refresh failed (run clouder --add-account dropbox again): %w", err)
	}
	c.access = tr.AccessToken
	c.expiry = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return c.access, nil
}

func (c *client) invalidate() {
	c.mu.Lock()
	c.access = ""
	c.mu.Unlock()
}

// send builds a request with build(), attaches the bearer token, and executes
// it — refreshing once on 401 and honoring Retry-After on 429. On a 2xx the
// live response is returned with its body open (the caller closes it); on any
// other status the body is drained and an error returned. build must produce a
// fresh request each call, since a retry re-sends it.
func (c *client) send(ctx context.Context, build func() (*http.Request, error)) (*http.Response, error) {
	const maxAttempts = 4
	refreshed := false
	for attempt := 0; attempt < maxAttempts; attempt++ {
		tok, err := c.token(ctx)
		if err != nil {
			return nil, err
		}
		req, err := build()
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := c.hc.Do(req)
		if err != nil {
			return nil, err
		}
		switch {
		case resp.StatusCode/100 == 2:
			return resp, nil
		case resp.StatusCode == http.StatusUnauthorized && !refreshed:
			resp.Body.Close()
			c.invalidate()
			refreshed = true
			continue
		case resp.StatusCode == http.StatusTooManyRequests:
			wait := retryAfter(resp)
			resp.Body.Close()
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		default:
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			return nil, &apiError{status: resp.StatusCode, body: strings.TrimSpace(string(body))}
		}
	}
	return nil, fmt.Errorf("gave up after %d attempts", maxAttempts)
}

type apiError struct {
	status int
	body   string
}

func (e *apiError) Error() string { return fmt.Sprintf("dropbox %d: %s", e.status, e.body) }

func retryAfter(resp *http.Response) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs >= 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return 2 * time.Second
}

// rpc posts a JSON body to an api.dropboxapi.com endpoint and decodes the JSON
// reply into out (out may be nil).
func (c *client) rpc(ctx context.Context, endpoint string, in, out any) error {
	payload, err := json.Marshal(in)
	if err != nil {
		return err
	}
	resp, err := c.send(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// headerSafe escapes every non-ASCII rune as \uXXXX so a JSON argument with
// Cyrillic (or any non-ASCII) filenames is legal in the Dropbox-API-Arg HTTP
// header, which must be ASCII.
func headerSafe(b []byte) string {
	var sb strings.Builder
	for _, r := range string(b) {
		if r < 0x80 {
			sb.WriteRune(r)
		} else if r <= 0xFFFF {
			fmt.Fprintf(&sb, "\\u%04x", r)
		} else {
			r -= 0x10000
			fmt.Fprintf(&sb, "\\u%04x\\u%04x", 0xD800+(r>>10), 0xDC00+(r&0x3FF))
		}
	}
	return sb.String()
}
