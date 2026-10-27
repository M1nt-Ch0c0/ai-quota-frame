// Package console exposes deliberately limited, credential-free operational
// state to the local desktop UI. It never reads source logs or configuration.
package console

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Event struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Message string    `json:"message"`
}
type Transfer struct {
	Channel           string     `json:"channel,omitempty"`
	UploadStartedAt   *time.Time `json:"upload_started_at,omitempty"`
	UploadCompletedAt *time.Time `json:"upload_completed_at,omitempty"`
	UploadDurationMs  int64      `json:"upload_duration_ms,omitempty"`
	BytesPerSecond    float64    `json:"bytes_per_second,omitempty"`
	Message           string     `json:"message,omitempty"`
	Stage             string     `json:"stage"`
	Done              int        `json:"done"`
	Total             int        `json:"total"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
	Uncertain         bool       `json:"uncertain"`
}
type App struct {
	ID             string    `json:"id"`
	Bank           int       `json:"bank"`
	Active         int       `json:"active"`
	Previous       int       `json:"previous"`
	Pending        int       `json:"pending"`
	Versions       []*uint32 `json:"versions"`
	ABI            int       `json:"abi"`
	ButtonEligible bool      `json:"button_eligible"`
}
type Device struct {
	Phase            string  `json:"phase,omitempty"`
	ABIMinor         *uint32 `json:"payload_abi_minor,omitempty"`
	CoCVersion       uint32  `json:"coc_version,omitempty"`
	CoCState         uint32  `json:"coc_state,omitempty"`
	LoadedApp        int     `json:"loaded_app"`
	Loaded           int     `json:"loaded"`
	ELFReady         bool    `json:"ready"`
	TrialApp         int     `json:"trial_app"`
	Version          uint32  `json:"version"`
	ABI              int     `json:"payload_abi"`
	Ready            bool    `json:"runtime_ready"`
	Confirmed        bool    `json:"confirmed"`
	Generation       uint32  `json:"generation"`
	Uptime           uint64  `json:"uptime_ms"`
	ResetReason      int     `json:"reset_reason"`
	FreePSRAM        uint32  `json:"free_psram"`
	FreeInternal     uint32  `json:"free_internal_ram"`
	CacheBytes       uint32  `json:"cache_bytes"`
	Characteristics  int     `json:"ble_characteristics"`
	LastSwitchSource string  `json:"last_switch_source"`
	Apps             []App   `json:"apps"`
}
type Snapshot struct {
	Capabilities          []Capability    `json:"capabilities,omitempty"`
	DeviceRaw             json.RawMessage `json:"device_raw,omitempty"`
	OperationResult       json.RawMessage `json:"operation_result,omitempty"`
	OperationResultAction string          `json:"operation_result_action,omitempty"`
	OperationResultAt     *time.Time      `json:"operation_result_at,omitempty"`
	Schema                int             `json:"schema"`
	StartedAt             time.Time       `json:"started_at"`
	ObservedAt            time.Time       `json:"observed_at"`
	Automatic             bool            `json:"automatic"`
	IntervalSeconds       int             `json:"interval_seconds"`
	Theme                 string          `json:"theme"`
	Transport             string          `json:"transport"`
	DeviceID              string          `json:"device_id"`
	SourceState           string          `json:"source_state"`
	LastFetch             *time.Time      `json:"last_fetch,omitempty"`
	NextFetch             *time.Time      `json:"next_fetch,omitempty"`
	AccountCount          int             `json:"account_count"`
	WarningCount          int             `json:"warning_count"`
	Transfer              Transfer        `json:"transfer"`
	Device                *Device         `json:"device,omitempty"`
	DeviceObservedAt      *time.Time      `json:"device_observed_at,omitempty"`
	DeviceError           string          `json:"device_error,omitempty"`
	Operation             string          `json:"operation,omitempty"`
	OperationError        string          `json:"operation_error,omitempty"`
	OperationDone         int             `json:"operation_done"`
	OperationTotal        int             `json:"operation_total"`
	PreviewPath           string          `json:"preview_path,omitempty"`
	Events                []Event         `json:"events"`
}
type State struct {
	mu          sync.Mutex
	value       Snapshot
	preview     string
	preferences string
}

func NewState(interval time.Duration, theme, transport, deviceID, directory string) *State {
	s := &State{value: Snapshot{Schema: 1, StartedAt: time.Now(), Automatic: true, IntervalSeconds: int(interval.Seconds()), Theme: theme, Transport: transport, DeviceID: deviceID, SourceState: "starting", Transfer: Transfer{Stage: "idle"}, Events: []Event{}}, preview: filepath.Join(directory, "console-last-frame.png")}
	s.preferences = filepath.Join(directory, "console", "preferences.json")
	if p, err := os.ReadFile(s.preferences); err == nil {
		var preference struct {
			Automatic bool `json:"automatic"`
		}
		if json.Unmarshal(p, &preference) == nil {
			s.value.Automatic = preference.Automatic
		} else {
			s.value.Automatic = false
			s.event("service", "本地暂停设置无法读取，后续推图保持暂停")
		}
	}
	s.event("service", "自动更新服务已启动")
	return s
}
func (s *State) event(kind, message string) {
	s.value.Events = append(s.value.Events, Event{time.Now(), kind, message})
	if len(s.value.Events) > 60 {
		s.value.Events = s.value.Events[len(s.value.Events)-60:]
	}
}
func (s *State) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Copy every slice and pointer so JSON encoding never races an update.
	p, _ := json.Marshal(s.value)
	var v Snapshot
	_ = json.Unmarshal(p, &v)
	v.ObservedAt = time.Now()
	if at := v.Transfer.UploadStartedAt; at != nil {
		end := v.ObservedAt
		if v.Transfer.UploadCompletedAt != nil {
			end = *v.Transfer.UploadCompletedAt
		}
		v.Transfer.UploadDurationMs = end.Sub(*at).Milliseconds()
		if v.Transfer.UploadDurationMs > 0 {
			v.Transfer.BytesPerSecond = float64(v.Transfer.Done) * 1000 / float64(v.Transfer.UploadDurationMs)
		}
	}
	return v
}
func (s *State) Automatic(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value.Automatic = enabled
	if enabled {
		s.event("service", "自动推图已恢复")
	} else {
		s.event("service", "已暂停后续推图，正在进行的刷新会完成")
	}
	if err := os.MkdirAll(filepath.Dir(s.preferences), 0700); err != nil {
		return err
	}
	p, _ := json.Marshal(struct {
		Automatic bool `json:"automatic"`
	}{enabled})
	if err := os.WriteFile(s.preferences+".tmp", p, 0600); err != nil {
		return err
	}
	return os.Rename(s.preferences+".tmp", s.preferences)
}
func (s *State) Fetch(ok, stale bool, at, next time.Time, accounts, warnings int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value.LastFetch = &at
	s.value.NextFetch = &next
	s.value.AccountCount = accounts
	s.value.WarningCount = warnings
	if ok && stale {
		s.value.SourceState = "stale"
		s.event("source", "采集已返回，但额度信息已过期")
	} else if ok {
		s.value.SourceState = "ready"
		s.event("source", "额度采集完成")
	} else {
		s.value.SourceState = "failed"
		s.event("source", "采集失败；未推送过期数据")
	}
}
func (s *State) Stage(stage string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.value.Transfer.Uncertain {
		return
	}
	s.value.Transfer.Stage = stage
	if stage == "rendering" {
		s.value.Transfer.Channel = ""
		s.value.Transfer.UploadStartedAt = nil
		s.value.Transfer.UploadCompletedAt = nil
		s.value.Transfer.Message = ""
		now := time.Now()
		s.value.Transfer.StartedAt = &now
		s.value.Transfer.Done = 0
		s.value.Transfer.Total = 0
	}
	labels := map[string]string{"rendering": "正在生成六色图片", "unchanged": "画面没有变化，跳过刷新", "failed": "本轮推图失败，等待重试", "rejected": "本轮图片已停止重试"}
	if label := labels[stage]; label != "" {
		s.event("transfer", label)
	}
}
func (s *State) TransportSelected(channel string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.value.Transfer.Channel = channel
	s.value.Transfer.UploadStartedAt = &now
	s.value.Transfer.UploadCompletedAt = nil
	label := "GATT 兼容通道"
	if channel == "l2cap-coc" {
		label = "L2CAP CoC 数据通道"
	}
	s.event("transfer", "本轮使用 "+label)
}
func (s *State) Failure(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value.Transfer.Message = message
	if s.value.Transfer.UploadStartedAt != nil && s.value.Transfer.UploadCompletedAt == nil {
		now := time.Now()
		s.value.Transfer.UploadCompletedAt = &now
	}
	if !s.value.Transfer.Uncertain {
		s.value.Transfer.Stage = "failed"
	}
	s.event("transfer", message)
}
func (s *State) PackageProgress(done, total int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value.OperationDone = done
	s.value.OperationTotal = total
}
func (s *State) Progress(done, total int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value.Transfer.Done = done
	s.value.Transfer.Total = total
	if done < total {
		s.value.Transfer.Stage = "uploading"
	} else {
		s.value.Transfer.Stage = "displaying"
		if s.value.Transfer.UploadCompletedAt == nil {
			now := time.Now()
			s.value.Transfer.UploadCompletedAt = &now
		}
	}
}
func (s *State) Pending() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value.Transfer.Uncertain = true
	s.value.Transfer.Stage = "recovering"
	s.event("transfer", "完成回复未收到，正在查询原请求；不会重发显示命令")
}
func (s *State) Unknown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value.Transfer.Uncertain = true
	s.value.Transfer.Stage = "unknown"
	s.event("transfer", "原请求完成结果无法确认，已停止该图片的自动重试")
}
func (s *State) Complete(png []byte) {
	// This method is called only after the sender authenticates completion.
	path := ""
	if err := os.WriteFile(s.preview+".tmp", png, 0600); err == nil {
		if os.Rename(s.preview+".tmp", s.preview) == nil {
			path = s.preview
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.value.Transfer.CompletedAt = &now
	s.value.Transfer.Stage = "complete"
	s.value.Transfer.Uncertain = false
	s.value.PreviewPath = path
	s.event("transfer", "设备已确认屏幕刷新与面板断电均已完成")
}
func (s *State) DeviceResult(raw json.RawMessage, success bool) error {
	var device Device
	if success {
		if err := json.Unmarshal(raw, &device); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !success {
		s.value.DeviceError = "未取得新的鉴权状态；下方保留上次观测"
		s.event("device", "设备状态读取失败；未重复执行设备操作")
		return nil
	}
	if old := s.value.Device; old != nil && device.Uptime < old.Uptime {
		s.event("reset", "设备运行时间回退，检测到重启；复位码本身不能确定原因")
	}
	now := time.Now()
	s.value.Device = &device
	s.value.DeviceRaw = append(json.RawMessage(nil), raw...)
	s.value.DeviceObservedAt = &now
	s.value.DeviceError = ""
	s.event("device", "已读取真机鉴权状态")
	return nil
}

// Result contains only explicitly sanitized operational output. Credentials,
// HTTP response bodies and source logs must never be passed to this method.
func (s *State) Result(action string, value any) error {
	p, err := json.Marshal(value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.value.OperationResult = p
	s.value.OperationResultAction = action
	s.value.OperationResultAt = &now
	return nil
}
func (s *State) Operation(name, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value.Operation = name
	s.value.OperationError = message
	if name == "" || name == "install" {
		s.value.OperationDone = 0
		s.value.OperationTotal = 0
	}
}
