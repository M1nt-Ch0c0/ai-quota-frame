//go:build js && wasm

// The browser uses the same Go quota clients, service, templates and PNG
// quantizer as the native application. It has no device or filesystem access.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"net/http"
	"net/url"
	"sync"
	"syscall/js"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/dashboard"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/frame"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/webframe"
)

var functions []js.Func
var requests = struct {
	sync.Mutex
	cancel map[string]context.CancelFunc
}{cancel: make(map[string]context.CancelFunc)}
var current = struct {
	sync.Mutex
	fingerprint [32]byte
	collector   *webframe.Collector
}{}

// Go's browser transport delegates to fetch. The outer API stays same-origin,
// disallows redirects and cookies, and exposes only the native client's three
// existing read/forward endpoints. Provider secrets remain on CPA.
type originTransport struct {
	origin string
	inner  *http.Transport
}

func (transport originTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme+"://"+request.URL.Host != transport.origin {
		return nil, errors.New("management origin mismatch")
	}
	if request.Method == http.MethodGet && request.URL.Path == "/v0/management/auth-files/download" {
		// The native xAI adapter needs only its public account subject. Its
		// private-file download is replaced by a server-side projection so no
		// OAuth access/refresh token is ever returned to the browser.
		request = request.Clone(request.Context())
		request.URL.Path = "/v0/management/plugins/photopainter/account-subject"
	}
	allowed := request.Method == http.MethodGet &&
		(request.URL.Path == "/v0/management/auth-files" || request.URL.Path == "/v0/management/dashboard/summary" || request.URL.Path == "/v0/management/plugins/photopainter/account-subject") ||
		request.Method == http.MethodPost && request.URL.Path == "/v0/management/api-call"
	if !allowed {
		return nil, errors.New("unsupported management endpoint")
	}
	request.Header.Set("js.fetch:credentials", "omit")
	request.Header.Set("js.fetch:redirect", "error")
	request.Header.Set("js.fetch:mode", "same-origin")
	return transport.inner.RoundTrip(request)
}

func bind(object js.Value, name string, callback func(js.Value, []js.Value) any) {
	function := js.FuncOf(callback)
	functions = append(functions, function)
	object.Set(name, function)
}

func result(value any, err error) string {
	if err != nil {
		encoded, _ := json.Marshal(map[string]any{"ok": false, "error": err.Error()})
		return string(encoded)
	}
	encoded, err := json.Marshal(map[string]any{"ok": true, "value": value})
	if err != nil {
		return `{"ok":false,"error":"无法编码渲染结果"}`
	}
	return string(encoded)
}

func main() {
	origin := js.Global().Get("location").Get("origin").String()
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return
	}
	http.DefaultTransport = originTransport{origin: origin, inner: &http.Transport{}}
	api := js.Global().Get("Object").New()
	bind(api, "fingerprint", func(_ js.Value, args []js.Value) any {
		var snapshot quota.Snapshot
		if len(args) != 1 || json.Unmarshal([]byte(args[0].String()), &snapshot) != nil {
			return result(nil, errors.New("额度快照格式错误"))
		}
		key, err := frame.ContentKey(snapshot)
		return result(hex.EncodeToString(key[:]), err)
	})
	bind(api, "html", func(_ js.Value, args []js.Value) any {
		var input struct {
			Snapshot quota.Snapshot   `json:"snapshot"`
			Options  webframe.Options `json:"options"`
		}
		if len(args) != 1 || json.Unmarshal([]byte(args[0].String()), &input) != nil {
			return result(nil, errors.New("额度快照格式错误"))
		}
		html, err := webframe.HTML(input.Snapshot, input.Options)
		return result(html, err)
	})
	bind(api, "quantize", func(_ js.Value, args []js.Value) any {
		if len(args) != 2 || args[0].Type() != js.TypeObject || !args[0].InstanceOf(js.Global().Get("Uint8Array")) || args[0].Get("byteLength").Int() > 5<<20 {
			return result(nil, errors.New("图片过大"))
		}
		var bounds []struct{ Left, Top, Right, Bottom int }
		if json.Unmarshal([]byte(args[1].String()), &bounds) != nil || len(bounds) > 1024 {
			return result(nil, errors.New("图片边界无效"))
		}
		input := make([]byte, args[0].Get("byteLength").Int())
		js.CopyBytesToGo(input, args[0])
		regions := make([]image.Rectangle, 0, len(bounds))
		for _, bound := range bounds {
			regions = append(regions, image.Rect(bound.Left, bound.Top, bound.Right, bound.Bottom))
		}
		png, err := dashboard.QuantizeBrowserRaster(input, regions)
		if err != nil {
			return result(nil, err)
		}
		output := js.Global().Get("Uint8Array").New(len(png))
		js.CopyBytesToJS(output, png)
		return output
	})
	bind(api, "collect", func(_ js.Value, args []js.Value) any {
		var input struct {
			ManagementKey string           `json:"managementKey"`
			Options       webframe.Options `json:"options"`
			ID            string           `json:"id"`
		}
		var parseErr error
		if len(args) != 1 {
			parseErr = errors.New("采集参数无效")
		} else {
			parseErr = json.Unmarshal([]byte(args[0].String()), &input)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		requests.Lock()
		if _, exists := requests.cancel[input.ID]; exists || input.ID == "" || parseErr != nil {
			parseErr = errors.New("采集参数无效或请求重复")
			cancel()
		} else {
			requests.cancel[input.ID] = cancel
		}
		requests.Unlock()
		executor := js.FuncOf(func(_ js.Value, callbacks []js.Value) any {
			resolve, reject := callbacks[0], callbacks[1]
			go func() {
				if parseErr != nil || input.ID == "" {
					reject.Invoke(js.Global().Get("Error").New("采集参数无效"))
					return
				}
				defer func() {
					cancel()
					requests.Lock()
					delete(requests.cancel, input.ID)
					requests.Unlock()
				}()
				settings, _ := json.Marshal(input.Options)
				fingerprint := sha256.Sum256([]byte(origin + "\x00" + input.ManagementKey + "\x00" + string(settings)))
				var initErr error
				current.Lock()
				if current.collector == nil || current.fingerprint != fingerprint {
					current.collector, initErr = webframe.NewCollector(origin, input.ManagementKey, input.Options)
					current.fingerprint = fingerprint
				}
				collector := current.collector
				current.Unlock()
				input.ManagementKey = ""
				if initErr != nil {
					reject.Invoke(js.Global().Get("Error").New(initErr.Error()))
					return
				}
				snapshot, fetchErr := collector.Collect(ctx)
				if fetchErr != nil {
					reject.Invoke(js.Global().Get("Error").New(fetchErr.Error()))
					return
				}
				encoded, marshalErr := json.Marshal(snapshot)
				if marshalErr != nil {
					reject.Invoke(js.Global().Get("Error").New("额度回复格式错误"))
					return
				}
				resolve.Invoke(string(encoded))
			}()
			return nil
		})
		promise := js.Global().Get("Promise").New(executor)
		executor.Release()
		return promise
	})
	bind(api, "cancel", func(_ js.Value, args []js.Value) any {
		if len(args) == 1 {
			requests.Lock()
			if cancel := requests.cancel[args[0].String()]; cancel != nil {
				cancel()
			}
			requests.Unlock()
		}
		return nil
	})
	bind(api, "reset", func(_ js.Value, _ []js.Value) any {
		requests.Lock()
		for _, cancel := range requests.cancel {
			cancel()
		}
		requests.Unlock()
		current.Lock()
		current.collector = nil
		current.fingerprint = [32]byte{}
		current.Unlock()
		return nil
	})
	js.Global().Set("photoPainterFrameBridge", api)
	select {}
}
