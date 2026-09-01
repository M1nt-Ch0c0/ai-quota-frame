package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/dashboard"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

type SnapshotSource interface {
	Snapshot() (quota.Snapshot, bool)
}

type FrameRenderer interface {
	Render(quota.Snapshot) ([]byte, error)
}

type Server struct {
	source       SnapshotSource
	renderer     FrameRenderer
	accessToken  string
	allowNoToken bool
	mux          *http.ServeMux
	frameMu      sync.Mutex
	frameCache   cachedFrame
}

type cachedFrame struct {
	key     [sha256.Size]byte
	payload []byte
	etag    string
	valid   bool
}

func New(source SnapshotSource, renderer FrameRenderer, accessToken string, allowNoToken bool) *Server {
	server := &Server{
		source:       source,
		renderer:     renderer,
		accessToken:  accessToken,
		allowNoToken: allowNoToken,
		mux:          http.NewServeMux(),
	}
	server.mux.HandleFunc("GET /healthz", server.health)
	server.mux.HandleFunc("GET /api/v1/quota", server.authorized(server.quotaJSON))
	server.mux.HandleFunc("GET /api/v1/frame.png", server.authorized(server.framePNG))
	return server
}

func (server *Server) Handler() http.Handler {
	return securityHeaders(server.mux)
}

func (server *Server) health(response http.ResponseWriter, _ *http.Request) {
	snapshot, ok := server.source.Snapshot()
	status := "starting"
	statusCode := http.StatusServiceUnavailable
	if ok {
		status = "ok"
		statusCode = http.StatusOK
		if snapshot.Stale {
			status = "degraded"
		}
	}
	writeJSON(response, statusCode, map[string]any{"status": status, "stale": ok && snapshot.Stale})
}

func (server *Server) quotaJSON(response http.ResponseWriter, request *http.Request) {
	snapshot, ok := server.source.Snapshot()
	if !ok {
		writeJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "quota data is not ready"})
		return
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]string{"error": "encode quota snapshot"})
		return
	}
	etag := contentETag(payload)
	response.Header().Set("ETag", etag)
	response.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
	if matchesETag(request.Header.Get("If-None-Match"), etag) {
		response.WriteHeader(http.StatusNotModified)
		return
	}
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(payload)
}

func (server *Server) framePNG(response http.ResponseWriter, request *http.Request) {
	if !displayDimensionMatches(request, "X-Display-Width", dashboard.Width) {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "this build targets an 800x480 PhotoPainter display"})
		return
	}
	if !displayDimensionMatches(request, "X-Display-Height", dashboard.Height) {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "this build targets an 800x480 PhotoPainter display"})
		return
	}
	snapshot, ok := server.source.Snapshot()
	if !ok {
		writeJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "quota data is not ready"})
		return
	}
	payload, etag, err := server.renderFrame(snapshot)
	if err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]string{"error": "render frame"})
		return
	}
	response.Header().Set("ETag", etag)
	response.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
	if matchesETag(request.Header.Get("If-None-Match"), etag) {
		response.WriteHeader(http.StatusNotModified)
		return
	}
	response.Header().Set("Content-Type", "image/png")
	response.Header().Set("Content-Length", strconv.Itoa(len(payload)))
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(payload)
}

func (server *Server) renderFrame(snapshot quota.Snapshot) ([]byte, string, error) {
	key, err := frameContentKey(snapshot)
	if err != nil {
		return nil, "", err
	}
	server.frameMu.Lock()
	defer server.frameMu.Unlock()
	if server.frameCache.valid && server.frameCache.key == key {
		return server.frameCache.payload, server.frameCache.etag, nil
	}
	payload, err := server.renderer.Render(snapshot)
	if err != nil {
		return nil, "", err
	}
	server.frameCache = cachedFrame{
		key:     key,
		payload: payload,
		etag:    contentETag(payload),
		valid:   true,
	}
	return server.frameCache.payload, server.frameCache.etag, nil
}

func frameContentKey(snapshot quota.Snapshot) ([sha256.Size]byte, error) {
	accounts := append([]quota.Account(nil), snapshot.Accounts...)
	for accountIndex := range accounts {
		accounts[accountIndex].Name = ""
		accounts[accountIndex].LastFreshAt = nil
		accounts[accountIndex].Windows = append([]quota.Window(nil), accounts[accountIndex].Windows...)
		for windowIndex := range accounts[accountIndex].Windows {
			window := &accounts[accountIndex].Windows[windowIndex]
			window.UsedPercent = nil
			window.ObservedAt = nil
			window.Source = ""
			if window.ResetsAt != nil {
				value := window.ResetsAt.UTC().Truncate(time.Minute)
				window.ResetsAt = &value
			}
		}
	}
	payload, err := json.Marshal(struct {
		DataUpdatedAt time.Time       `json:"data_updated_at"`
		Stale         bool            `json:"stale"`
		Accounts      []quota.Account `json:"accounts"`
		Usage         *quota.Usage    `json:"usage,omitempty"`
		Errors        []string        `json:"errors,omitempty"`
	}{
		DataUpdatedAt: snapshot.DataUpdatedAt,
		Stale:         snapshot.Stale,
		Accounts:      accounts,
		Usage:         snapshot.Usage,
		Errors:        snapshot.Errors,
	})
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(payload), nil
}

func (server *Server) authorized(next http.HandlerFunc) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		authorized := server.allowNoToken && server.accessToken == ""
		if server.accessToken != "" {
			provided := strings.TrimSpace(request.Header.Get("Authorization"))
			if len(provided) >= 7 && strings.EqualFold(provided[:7], "Bearer ") {
				provided = strings.TrimSpace(provided[7:])
			} else {
				provided = ""
			}
			authorized = subtle.ConstantTimeCompare([]byte(provided), []byte(server.accessToken)) == 1
		}
		if !authorized {
			response.Header().Set("WWW-Authenticate", `Bearer realm="ai-quota-frame"`)
			writeJSON(response, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next(response, request)
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(response, request)
	})
}

func contentETag(payload []byte) string {
	sum := sha256.Sum256(payload)
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

func matchesETag(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		if candidate = strings.TrimSpace(candidate); candidate == etag || candidate == "*" {
			return true
		}
	}
	return false
}

func displayDimensionMatches(request *http.Request, name string, expected int) bool {
	value := strings.TrimSpace(request.Header.Get(name))
	if value == "" {
		return true
	}
	parsed, err := strconv.Atoi(value)
	return err == nil && parsed == expected
}

func writeJSON(response http.ResponseWriter, status int, payload any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(payload)
}
