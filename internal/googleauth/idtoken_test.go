package googleauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func writeServiceAccount(t *testing.T, key *rsa.PrivateKey, tokenURI string) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey() error = %v", err)
	}
	account := map[string]string{
		"type":           "service_account",
		"client_email":   "frame@example.iam.gserviceaccount.com",
		"private_key_id": "kid-1",
		"private_key":    string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"token_uri":      tokenURI,
	}
	raw, _ := json.Marshal(account)
	path := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func fakeIDToken(expires time.Time) string {
	segment := func(value any) string {
		raw, _ := json.Marshal(value)
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	return segment(map[string]string{"alg": "RS256"}) + "." + segment(map[string]int64{"exp": expires.Unix()}) + ".sig"
}

func TestTokenSignsAssertionAndCachesUntilNearExpiry(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	now := time.Unix(1_800_000_000, 0)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm() error = %v", err)
		}
		if got := r.PostForm.Get("grant_type"); got != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
			t.Errorf("grant_type = %q", got)
		}
		parts := strings.Split(r.PostForm.Get("assertion"), ".")
		if len(parts) != 3 {
			t.Fatalf("assertion has %d parts", len(parts))
		}
		signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
		digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
			t.Errorf("assertion signature invalid: %v", err)
		}
		payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var claims map[string]any
		_ = json.Unmarshal(payload, &claims)
		if claims["iss"] != "frame@example.iam.gserviceaccount.com" || claims["target_audience"] != "client-id" {
			t.Errorf("unexpected claims %v", claims)
		}
		fmt.Fprintf(w, `{"id_token":%q}`, fakeIDToken(now.Add(time.Hour)))
	}))
	defer server.Close()

	source, err := NewIDTokenSource(writeServiceAccount(t, key, ""), "client-id", time.Second)
	if err != nil {
		t.Fatalf("NewIDTokenSource() error = %v", err)
	}
	source.tokenURL = server.URL
	source.now = func() time.Time { return now }

	first, err := source.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	now = now.Add(50 * time.Minute)
	second, err := source.Token(context.Background())
	if err != nil || second != first || requests.Load() != 1 {
		t.Fatalf("cached Token() = %v, requests = %d; want cached token", err, requests.Load())
	}
	now = now.Add(6 * time.Minute)
	if _, err := source.Token(context.Background()); err != nil || requests.Load() != 2 {
		t.Fatalf("Token() near expiry error = %v, requests = %d; want refresh", err, requests.Load())
	}
}

func TestTokenReportsEndpointErrorWithoutBody(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant","error_description":"secret-ish detail"}`)
	}))
	defer server.Close()
	source, err := NewIDTokenSource(writeServiceAccount(t, key, ""), "client-id", time.Second)
	if err != nil {
		t.Fatalf("NewIDTokenSource() error = %v", err)
	}
	source.tokenURL = server.URL
	_, err = source.Token(context.Background())
	if err == nil || !strings.Contains(err.Error(), "invalid_grant") || strings.Contains(err.Error(), "detail") {
		t.Fatalf("Token() error = %v, want status and error code only", err)
	}
}

func TestNewIDTokenSourceRejectsForeignTokenURI(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	if _, err := NewIDTokenSource(writeServiceAccount(t, key, "https://evil.example/token"), "aud", time.Second); err == nil {
		t.Fatal("NewIDTokenSource() accepted a non-Google token_uri")
	}
}
