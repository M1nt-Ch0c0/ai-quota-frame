// Package googleauth mints Google-signed ID tokens for a service account so the
// collectors can pass an OAuth gateway (oauth2-proxy with JWT bearer tokens)
// without a browser session. The private key never leaves this process; only
// a short-lived signed assertion is sent to Google's token endpoint.
package googleauth

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	googleTokenURL = "https://oauth2.googleapis.com/token"
	assertionTTL   = time.Hour
	// Refresh this long before expiry so a token never expires mid-refresh.
	refreshMargin = 5 * time.Minute
	maxTokenBody  = 64 << 10
)

// IDTokenSource returns a cached ID token for one service account and
// audience, minting a new one shortly before the cached token expires.
type IDTokenSource struct {
	email      string
	keyID      string
	key        *rsa.PrivateKey
	audience   string
	tokenURL   string
	httpClient *http.Client
	now        func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

type serviceAccountFile struct {
	Type         string `json:"type"`
	ClientEmail  string `json:"client_email"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	TokenURI     string `json:"token_uri"`
}

// NewIDTokenSource reads a Google service account JSON key file.
func NewIDTokenSource(path, audience string, timeout time.Duration) (*IDTokenSource, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Google service account file: %w", err)
	}
	var account serviceAccountFile
	if err := json.Unmarshal(raw, &account); err != nil {
		return nil, errors.New("Google service account file is not valid JSON")
	}
	if account.Type != "service_account" || account.ClientEmail == "" || account.PrivateKey == "" {
		return nil, errors.New("Google service account file must be a service_account JSON key")
	}
	if account.TokenURI != "" && account.TokenURI != googleTokenURL {
		return nil, fmt.Errorf("Google service account token_uri must be %s", googleTokenURL)
	}
	key, err := parsePrivateKey(account.PrivateKey)
	if err != nil {
		return nil, err
	}
	audience = strings.TrimSpace(audience)
	if audience == "" {
		return nil, errors.New("Google ID token audience is empty")
	}
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &IDTokenSource{
		email:    account.ClientEmail,
		keyID:    account.PrivateKeyID,
		key:      key,
		audience: audience,
		tokenURL: googleTokenURL,
		httpClient: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		now: time.Now,
	}, nil
}

func parsePrivateKey(encoded string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(encoded))
	if block == nil {
		return nil, errors.New("Google service account private key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		if key, pkcs1Err := x509.ParsePKCS1PrivateKey(block.Bytes); pkcs1Err == nil {
			return key, nil
		}
		return nil, errors.New("Google service account private key cannot be parsed")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("Google service account private key must be RSA")
	}
	return key, nil
}

// Email is the service account identity, safe to log.
func (source *IDTokenSource) Email() string {
	return source.email
}

// Token returns a valid ID token, minting one when needed.
func (source *IDTokenSource) Token(ctx context.Context) (string, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.token != "" && source.now().Add(refreshMargin).Before(source.expires) {
		return source.token, nil
	}
	token, expires, err := source.mint(ctx)
	if err != nil {
		return "", err
	}
	source.token, source.expires = token, expires
	return token, nil
}

func (source *IDTokenSource) mint(ctx context.Context) (string, time.Time, error) {
	assertion, err := source.signAssertion()
	if err != nil {
		return "", time.Time{}, err
	}
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, source.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, errors.New("build Google token request")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := source.httpClient.Do(request)
	if err != nil {
		return "", time.Time{}, errors.New("Google token request failed")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxTokenBody))
	if err != nil {
		return "", time.Time{}, errors.New("read Google token response")
	}
	if response.StatusCode != http.StatusOK {
		// Google's error body names the failure (e.g. invalid_grant) without
		// echoing credentials; keep only the error code.
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &failure)
		return "", time.Time{}, fmt.Errorf("Google token endpoint returned %d %s", response.StatusCode, failure.Error)
	}
	var payload struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.IDToken == "" {
		return "", time.Time{}, errors.New("Google token response has no id_token")
	}
	expires, err := tokenExpiry(payload.IDToken)
	if err != nil {
		return "", time.Time{}, err
	}
	return payload.IDToken, expires, nil
}

func (source *IDTokenSource) signAssertion() (string, error) {
	now := source.now()
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	if source.keyID != "" {
		header["kid"] = source.keyID
	}
	claims := map[string]any{
		"iss":             source.email,
		"sub":             source.email,
		"aud":             source.tokenURL,
		"iat":             now.Unix(),
		"exp":             now.Add(assertionTTL).Unix(),
		"target_audience": source.audience,
	}
	encodedHeader, err := encodeSegment(header)
	if err != nil {
		return "", err
	}
	encodedClaims, err := encodeSegment(claims)
	if err != nil {
		return "", err
	}
	signingInput := encodedHeader + "." + encodedClaims
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, source.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", errors.New("sign Google token assertion")
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func encodeSegment(value any) (string, error) {
	var buffer bytes.Buffer
	if err := json.NewEncoder(&buffer).Encode(value); err != nil {
		return "", errors.New("encode Google token assertion")
	}
	return base64.RawURLEncoding.EncodeToString(bytes.TrimRight(buffer.Bytes(), "\n")), nil
}

// tokenExpiry reads the exp claim. The token comes straight from Google over
// TLS and is verified by the gateway, so its signature is not checked here.
func tokenExpiry(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, errors.New("Google id_token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, errors.New("Google id_token payload is not base64url")
	}
	var claims struct {
		Expires int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Expires == 0 {
		return time.Time{}, errors.New("Google id_token has no exp claim")
	}
	return time.Unix(claims.Expires, 0), nil
}
