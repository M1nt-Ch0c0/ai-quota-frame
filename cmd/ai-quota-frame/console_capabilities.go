package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/ble"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/console"
)

func isConsoleExtra(action string) bool {
	switch action {
	case "scan", "info", "diagnose", "doctor", "bind", "unbind", "pair", "pair-status", "forget-pairing", "remove", "push", "app-read", "app-write",
		"wifi-list", "wifi-set", "wifi-add", "wifi-remove", "wifi-move", "wifi-backup", "wifi-update", "package",
		"wifi-file-status", "wifi-file-upsert", "wifi-file-remove", "wifi-file-move", "serial-diagnose", "provision-generate":
		return true
	}
	return strings.HasPrefix(action, "usb-")
}
func consoleCapabilities() []console.Capability {
	rows := []console.Capability{
		{ID: "scan", Title: "扫描附近设备", Category: "设备", Available: true, Reason: "扫描结果尚未鉴权；连接后核对设备身份"},
		{ID: "info", Title: "读取设备信息", Category: "设备", Available: true, Reason: ""}, {ID: "status", Title: "读取完整状态", Category: "设备", Available: true, Reason: ""},
		{ID: "bind", Title: "配对相框", Category: "设备", Available: true, Reason: "核对配对码后，在相框上按键同意"},
		{ID: "pair", Title: "配对相框", Category: "设备", Available: true, Reason: "核对配对码后，在相框上按键同意"},
		{ID: "pair-status", Title: "查看配对状态", Category: "设备", Available: true},
		{ID: "forget-pairing", Title: "忘记本机配对", Category: "设备", Available: true, Reason: "设备端解绑请长按相框按键"},
		{ID: "unbind", Title: "忘记本机配对", Category: "设备", Available: true, Reason: "设备端解绑请长按相框按键"},
		{ID: "inspect", Title: "检查应用包", Category: "应用", Available: true, Reason: ""}, {ID: "install", Title: "安装到备用槽", Category: "应用", Available: true, Reason: "需再次核对文件摘要"},
		{ID: "activate", Title: "激活候选", Category: "应用", Available: true, Reason: ""}, {ID: "switch", Title: "切换应用", Category: "应用", Available: true, Reason: ""},
		{ID: "rollback", Title: "回退版本", Category: "应用", Available: true, Reason: ""}, {ID: "remove", Title: "移除非运行应用", Category: "应用", Available: true, Reason: "不能移除当前运行应用"},
		{ID: "use-slot", Title: "使用指定槽版本", Category: "应用", Available: true, Reason: ""}, {ID: "cancel-trial", Title: "结束未确认试运行", Category: "应用", Available: true, Reason: ""},
		{ID: "inspect-image", Title: "检查 PNG", Category: "图片", Available: true, Reason: ""}, {ID: "push", Title: "发送自选 PNG", Category: "图片", Available: true, Reason: "需 800×480 严格六色 PNG；结果不确定时只查询原回执"},
		{ID: "pause", Title: "暂停自动推图", Category: "图片", Available: true, Reason: "不打断已接纳的显示"}, {ID: "resume", Title: "恢复自动推图", Category: "图片", Available: true, Reason: ""},
		{ID: "refresh", Title: "立即采集更新", Category: "图片", Available: true, Reason: ""},
		{ID: "app-read", Title: "读取应用特征", Category: "应用通信", Available: true, Reason: "需要当前 APP 注册的 ABI 2.1 GATT 特征"},
		{ID: "app-write", Title: "写入应用特征", Category: "应用通信", Available: true, Reason: "索引 0–3、最多 128 字节；只发送一次"},
		{ID: "wifi-list", Title: "读取 Wi-Fi 列表", Category: "网络", Available: true, Reason: "需要设备可达私网 HTTP 和可用 SD 配置"},
		{ID: "wifi-add", Title: "新增或更新网络", Category: "网络", Available: true, Reason: "保存后下次重启生效"},
		{ID: "wifi-remove", Title: "删除网络", Category: "网络", Available: true, Reason: "保存后下次重启生效"},
		{ID: "wifi-move", Title: "调整网络优先级", Category: "网络", Available: true, Reason: "使用从 1 开始的序号"},
		{ID: "wifi-set", Title: "替换网络列表", Category: "网络", Available: true, Reason: "最多 10 个网络；密码不返回界面"},
		{ID: "wifi-backup", Title: "备份 Wi-Fi 配置", Category: "网络", Available: true, Reason: "创建新的 0600 私有文件，不覆盖现有文件"},
		{ID: "wifi-update", Title: "从文件更新 Wi-Fi", Category: "网络", Available: true, Reason: "读取受保护 JSON，保存后重启生效"},
		{ID: "diagnose", Title: "设备与后台诊断", Category: "诊断", Available: true, Reason: "不打开串口、不触发刷新"},
		{ID: "doctor", Title: "开发环境诊断", Category: "诊断", Available: true, Reason: "运行既有只读 doctor，需本机宿主源码"},
		{ID: "package", Title: "打包应用 ELF", Category: "开发", Available: true, Reason: "需本机宿主打包工具，输出新 .pkg 文件"},
		{ID: "wifi-file-status", Title: "读取本地 Wi-Fi JSON", Category: "SD与配置", Available: true, Reason: "不打开串口；密码不回显"},
		{ID: "wifi-file-upsert", Title: "编辑本地网络", Category: "SD与配置", Available: true, Reason: "SD 或私有 JSON 的原子更新"},
		{ID: "wifi-file-remove", Title: "删除本地网络", Category: "SD与配置", Available: true, Reason: "序号从 1 开始"},
		{ID: "wifi-file-move", Title: "调整本地优先级", Category: "SD与配置", Available: true, Reason: "序号从 1 开始"},
		{ID: "serial-diagnose", Title: "串口启动诊断", Category: "USB维护", Available: true, Reason: "精确核对目标 USB 身份；可能复位设备"},
		{ID: "provision-generate", Title: "生成私有 NVS 配置", Category: "USB维护", Available: true, Reason: "可信配置文件由工具读取，不回显凭据"},
		{ID: "usb-provision", Title: "写入 NVS 配置", Category: "USB维护", Available: true, Reason: "完整备份后只写 NVS；设备令牌可能改变"},
		{ID: "usb-ports", Title: "枚举 USB 设备", Category: "USB维护", Available: true, Reason: ""},
		{ID: "usb-inspect", Title: "检查固件或备份", Category: "USB维护", Available: true, Reason: ""},
		{ID: "usb-backup", Title: "完整 Flash 备份", Category: "USB维护", Available: true, Reason: "串口操作可能复位设备"},
		{ID: "usb-flash", Title: "更新宿主固件", Category: "USB维护", Available: true, Reason: "精确端口、完整备份、仅写 factory"},
		{ID: "usb-restore", Title: "恢复设备备份", Category: "USB维护", Available: true, Reason: "需明确确认设备身份和备份摘要"},
	}
	for i := range rows {
		if rows[i].ID == "doctor" || rows[i].ID == "package" || rows[i].ID == "serial-diagnose" || rows[i].ID == "provision-generate" {
			script := "doctor.py"
			if rows[i].ID == "package" {
				script = "module.py"
			}
			if rows[i].ID == "provision-generate" {
				script = "provision.py"
			}
			if _, err := os.Stat(filepath.Join(consoleHostRepo(), "tools", script)); err != nil {
				rows[i].Available = false
				rows[i].Reason = "本机未找到 photopainter-host 源码工具；设置 PHOTOFRAME_HOST_REPO 后重启后台"
			}
		}
	}
	return rows
}
func validateConsoleExtra(r console.Request, s console.Snapshot) error {
	switch r.Action {
	case "bind", "pair", "unbind", "forget-pairing", "remove", "push", "app-write", "wifi-set", "wifi-add", "wifi-remove", "wifi-move", "wifi-backup", "wifi-update", "package", "usb-backup", "usb-flash", "usb-restore", "usb-provision",
		"wifi-file-upsert", "wifi-file-remove", "wifi-file-move", "serial-diagnose", "provision-generate":
		if !r.Confirmed {
			return errors.New("请确认此操作后再执行")
		}
		if s.Transfer.Uncertain {
			return errors.New("图片结果尚未确定，先读取设备状态；未执行修改")
		}
	}
	if r.Action == "usb-provision" && !r.CredentialsConfirmed {
		return errors.New("请确认 NVS 写入可能改变 Wi-Fi 与设备令牌，使现有后台凭据失效")
	}
	if r.AllowSameVersion && !r.Confirmed {
		return errors.New("同版本备用槽重建需要明确确认")
	}
	if r.Action == "app-read" || r.Action == "app-write" {
		if r.App == "" || r.Characteristic == nil || *r.Characteristic > 3 {
			return errors.New("请选择应用和 0–3 的特征索引")
		}
	}
	if r.Action == "push" && (r.Path == "" || r.Digest == "") {
		return errors.New("请先检查 PNG 并确认文件摘要")
	}
	return nil
}
func readConsoleImage(path string) ([]byte, console.ImageInfo, error) {
	var info console.ImageInfo
	p, err := readBoundedRegular(path, ble.MaxImageSize)
	if err != nil {
		return nil, info, errors.New("无法读取普通 PNG 文件，或文件超过 5 MiB")
	}
	if err = ble.ValidatePNG(p); err != nil {
		return nil, info, fmt.Errorf("图片不符合设备格式：%w", err)
	}
	digest := sha256.Sum256(p)
	info = console.ImageInfo{Bytes: len(p), Digest: hex.EncodeToString(digest[:]), Width: 800, Height: 480}
	return p, info, nil
}
func readBoundedRegular(path string, limit int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > int64(limit) {
		return nil, errors.New("invalid file")
	}
	p, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if len(p) > limit {
		return nil, errors.New("oversized file")
	}
	return p, err
}
func (b *consoleBridge) executeExtra(ctx context.Context, r console.Request) error {
	switch r.Action {
	case "scan":
		op, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		devices, err := ble.Scan(op)
		if err != nil {
			return errors.New("扫描未完成；检查蓝牙开关和后台权限")
		}
		return b.state.Result(r.Action, map[string]any{"devices": devices, "authenticated": false})
	case "pair", "bind":
		id := r.DeviceID
		if id == "" {
			id = b.id
		}
		name := r.Text
		if name == "" {
			name = "PhotoPainter desktop"
		}
		op, cancel := context.WithTimeout(ctx, ble.PairingTimeout)
		defer cancel()
		bond, err := ble.PairDevice(op, id, name, func(progress ble.PairingProgress) { _ = b.state.Result(r.Action, progress) })
		if err != nil {
			return err
		}
		return b.state.Result(r.Action, map[string]any{"paired": true, "bond": bond, "requires_restart": id != b.id})
	case "pair-status":
		id := r.DeviceID
		if id == "" {
			id = b.id
		}
		op, cancel := context.WithTimeout(ctx, 40*time.Second)
		defer cancel()
		info, err := ble.PairingStatus(op, id)
		if err != nil {
			return err
		}
		return b.state.Result(r.Action, map[string]any{"authenticated": false, "pairing": info})
	case "info":
		id := r.DeviceID
		if id == "" {
			id = b.id
		}
		id, err := ble.NormalizeID(id)
		if err != nil {
			return errors.New("设备 ID 必须为 12 位十六进制")
		}
		op, cancel := context.WithTimeout(ctx, 40*time.Second)
		defer cancel()
		var info ble.DeviceInfo
		err = ble.WithClient(op, id, b.token, func(ctx context.Context, c *ble.Client) error { info = c.Info(); return nil })
		if err != nil {
			return errors.New("未完成指定设备的双向鉴权；未保存绑定")
		}
		return b.state.Result(r.Action, map[string]any{"info": info, "authenticated": true})
	case "unbind", "forget-pairing":
		id := r.DeviceID
		if id == "" {
			id = b.id
		}
		if err := ble.ForgetBond(id); err != nil {
			return err
		}
		return b.state.Result(r.Action, map[string]any{"ok": true, "message": "本机已忘记相框。更换连接方请长按相框按键重置配对。"})
	case "remove":
		return b.removeApplication(ctx, r.App)
	case "push":
		return b.pushImage(ctx, r)
	case "app-read", "app-write":
		return b.applicationCharacteristic(ctx, r)
	case "diagnose":
		return b.diagnose(ctx)
	case "doctor":
		return b.doctor(ctx)
	case "serial-diagnose":
		return b.serialDiagnose(ctx, r)
	case "provision-generate":
		return b.generateProvision(ctx, r)
	case "package":
		return b.packageApplication(ctx, r)
	}
	if strings.HasPrefix(r.Action, "wifi-file-") {
		return b.wifiFileOperation(r)
	}
	if strings.HasPrefix(r.Action, "wifi-") {
		return b.wifiOperation(ctx, r)
	}
	return errors.New("不支持的操作")
}
func (b *consoleBridge) removeApplication(ctx context.Context, id string) error {
	op, cancel := context.WithTimeout(ctx, 11*time.Minute)
	defer cancel()
	if err := b.readDevice(op); err != nil {
		return err
	}
	before := b.state.Snapshot().Device
	a := consoleApp(before, id)
	if a == nil {
		return errors.New("设备没有这个应用")
	}
	if before.TrialApp >= 0 || before.LoadedApp == a.Bank {
		return errors.New("不能移除运行中应用或未结束的试运行")
	}
	if err := b.manageOnce(op, ble.OpRemove, id); err != nil {
		return err
	}
	if consoleApp(b.state.Snapshot().Device, id) != nil {
		return errors.New("设备尚未确认应用已移除；没有重复删除")
	}
	return b.state.Result("remove", map[string]any{"ok": true, "app": id, "device": b.state.Snapshot().Device})
}
func (b *consoleBridge) pushImage(ctx context.Context, r console.Request) error {
	if b.sender == nil {
		return errors.New("当前后台没有可用的 BLE 图片发送器")
	}
	p, info, err := readConsoleImage(r.Path)
	if err != nil {
		return err
	}
	if info.Digest != r.Digest {
		return errors.New("PNG 在确认后已变化，未发送")
	}
	if err = b.sender.PrepareExplicitPush(p); err != nil {
		return errors.New("仍有未确定的显示请求，未建立新的显示任务")
	}
	op, cancel := context.WithTimeout(ctx, ble.ImageTimeout)
	defer cancel()
	b.state.Stage("rendering")
	b.state.Stage("connecting")
	err = b.sender.Push(op, p, b.state.Progress)
	if err == nil {
		b.state.Complete(p)
		return b.state.Result("push", map[string]any{"ok": true, "bytes": len(p), "digest": info.Digest, "physical_complete": true})
	}
	if errors.Is(err, ble.ErrImagePending) {
		b.state.Pending()
	}
	if errors.Is(err, ble.ErrImageOutcomeUnknown) {
		b.state.Unknown()
	}
	b.state.Failure(consoleTransferError(err))
	return errors.New(consoleTransferError(err))
}
func (b *consoleBridge) applicationCharacteristic(ctx context.Context, r console.Request) error {
	var value []byte
	if r.DataBase64 != "" {
		var err error
		value, err = base64.StdEncoding.DecodeString(r.DataBase64)
		if err != nil {
			return errors.New("数据不是合法 Base64")
		}
	} else {
		value = []byte(r.Text)
	}
	if len(value) > 128 {
		return errors.New("应用特征最多 128 字节；图片大数据请使用推图入口")
	}
	op, cancel := context.WithTimeout(ctx, 11*time.Minute)
	defer cancel()
	var result []byte
	err := ble.WithClient(op, b.id, b.token, func(ctx context.Context, c *ble.Client) error {
		if r.Action == "app-write" {
			return c.AppWrite(ctx, r.App, *r.Characteristic, value)
		}
		var err error
		result, err = c.AppRead(ctx, r.App, *r.Characteristic)
		return err
	})
	if err != nil {
		return errors.New("应用特征操作未确认；写入没有重复发送，请读取状态")
	}
	data := map[string]any{"ok": true, "app": r.App, "characteristic": *r.Characteristic}
	if r.Action == "app-read" {
		data["bytes_base64"] = base64.StdEncoding.EncodeToString(result)
		data["bytes"] = len(result)
		data["text"] = strings.ToValidUTF8(string(result), "�")
	}
	return b.state.Result(r.Action, data)
}
func consoleHostRepo() string {
	if p := os.Getenv("PHOTOFRAME_HOST_REPO"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "opensource", "photopainter-host")
}
func localDiagnostic() map[string]any {
	serial, _ := filepath.Glob("/dev/cu.*")
	binding, bindingErr := ble.LoadBinding()
	deviceID := os.Getenv("PHOTOFRAME_DEVICE_ID")
	if deviceID == "" {
		deviceID = binding
	}
	paired, pending := false, false
	if bond, err := ble.LoadBond(deviceID); err == nil {
		paired = !bond.Pending
		pending = bond.Pending
	}
	binary, _ := os.Executable()
	return map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH, "go_version": runtime.Version(), "binary": binary,
		"configured_device_id": os.Getenv("PHOTOFRAME_DEVICE_ID"), "binding_device_id": binding, "binding_valid": bindingErr == nil,
		"paired": paired, "pairing_pending": pending, "serial_ports": serial,
		"host_tools_present": fileExists(filepath.Join(consoleHostRepo(), "tools", "doctor.py")),
		"battery_observable": false, "battery_reason": "现有固件没有上报电压、电量或 PMIC 关机原因；不能从 POWERON 推断电池耗尽"}
}
func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}
func (b *consoleBridge) diagnose(ctx context.Context) error {
	result := localDiagnostic()
	result["device_id"] = b.id
	err := b.readDevice(ctx)
	v := b.state.Snapshot()
	result["device_observed_at"] = v.DeviceObservedAt
	result["device_read_ok"] = err == nil
	result["device"] = v.Device
	result["transfer"] = v.Transfer
	if err != nil {
		result["device_error"] = err.Error()
	}
	if err == nil {
		result["device_raw"] = v.DeviceRaw
	}
	return b.state.Result("diagnose", result)
}
func (b *consoleBridge) doctor(ctx context.Context) error {
	script := filepath.Join(consoleHostRepo(), "tools", "doctor.py")
	if !fileExists(script) {
		return errors.New("未找到宿主 doctor.py；请设置 PHOTOFRAME_HOST_REPO")
	}
	op, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	output, err := exec.CommandContext(op, "python3", script, "--json").Output()
	if err != nil {
		return errors.New("开发环境诊断未完成；未打开串口或读取凭据")
	}
	var checks any
	if json.Unmarshal(output, &checks) != nil {
		return errors.New("诊断输出格式无效")
	}
	return b.state.Result("doctor", map[string]any{"checks": checks, "serial_opened": false})
}
func (b *consoleBridge) packageApplication(ctx context.Context, r console.Request) error {
	if r.Path == "" || r.Output == "" || r.Version == nil {
		return errors.New("请选择 ELF、应用版本和新的输出路径")
	}
	if _, err := os.Lstat(r.Output); !os.IsNotExist(err) {
		return errors.New("输出文件已经存在或无法访问，未覆盖")
	}
	script := filepath.Join(consoleHostRepo(), "tools", "module.py")
	if !fileExists(script) {
		return errors.New("未找到宿主应用打包工具")
	}
	temp, err := os.CreateTemp(filepath.Dir(r.Output), ".photopainter-package-*")
	if err != nil {
		return errors.New("无法创建输出文件")
	}
	name := temp.Name()
	temp.Close()
	defer os.Remove(name)
	args := []string{script, "package", "--input", r.Path, "--output", name, "--version", strconv.FormatUint(uint64(*r.Version), 10)}
	if r.App != "" {
		args = append(args, "--app", r.App)
	}
	op, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err = exec.CommandContext(op, "python3", args...).Run(); err != nil {
		return errors.New("ELF/ABI 校验或打包失败，未发布输出文件")
	}
	_, info, err := readConsolePackage(name)
	if err != nil {
		return err
	}
	if err = os.Link(name, r.Output); err != nil {
		return errors.New("输出文件出现冲突，未覆盖")
	}
	return b.state.Result("package", map[string]any{"path": r.Output, "package": info})
}

type wifiDocument struct {
	Version  int                   `json:"version"`
	Networks []console.WiFiProfile `json:"networks"`
}

func validateWifi(d wifiDocument) error {
	if d.Version != 1 || d.Networks == nil || len(d.Networks) > 10 {
		return errors.New("Wi-Fi 配置必须为版本 1，最多 10 个网络")
	}
	seen := map[string]bool{}
	for _, n := range d.Networks {
		size := len([]byte(n.SSID))
		if size < 1 || size > 32 || !utf8.ValidString(n.SSID) || strings.ContainsAny(n.SSID, "\x00\r\n") || seen[n.SSID] {
			return errors.New("SSID 必须唯一且为 1–32 字节，不包含换行")
		}
		seen[n.SSID] = true
		p := n.Password
		for _, value := range []byte(p) {
			if value < 0x20 || value > 0x7e {
				return errors.New("Wi-Fi 密码须为可打印 ASCII，或留空用于开放网络")
			}
		}
		if strings.ContainsAny(p, "\x00\r\n") {
			return errors.New("Wi-Fi 密码不能包含换行")
		}
		if len(p) == 64 {
			if _, err := hex.DecodeString(p); err != nil {
				return errors.New("64 字节密码必须为十六进制 PSK")
			}
		} else if len(p) != 0 && (len(p) < 8 || len(p) > 63) {
			return errors.New("Wi-Fi 密码需为空或 8–63 字节，也可使用 64 位十六进制 PSK")
		}
	}
	return nil
}
func (b *consoleBridge) deviceOrigin(ctx context.Context, raw string) (string, error) {
	op, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	var authenticatedIP netip.Addr
	err := ble.WithClient(op, b.id, b.token, func(ctx context.Context, c *ble.Client) error { authenticatedIP = c.Info().IP; return nil })
	if err != nil {
		return "", errors.New("无法鉴权核对设备 IP；未发送 HTTP 令牌")
	}
	if !authenticatedIP.Is4() || !authenticatedIP.IsPrivate() {
		return "", errors.New("设备尚未取得可达的私网 IPv4，当前只能使用 BLE 管理")
	}
	if raw == "" {
		raw = "http://" + authenticatedIP.String()
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("请输入设备 HTTP(S) 原点地址，不含凭据、路径或查询参数")
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if err != nil || !ip.Is4() || !ip.IsPrivate() || ip != authenticatedIP {
		return "", errors.New("地址必须匹配刚刚鉴权读取的设备私网 IPv4；未向其他目标发送令牌")
	}
	return strings.TrimRight(u.String(), "/"), nil
}
func (b *consoleBridge) wifiRequest(ctx context.Context, origin string, body []byte) ([]byte, error) {
	method := "GET"
	if body != nil {
		method = "POST"
	}
	if len(body) > 8192 {
		return nil, errors.New("Wi-Fi 配置超过 8192 字节")
	}
	req, err := http.NewRequestWithContext(ctx, method, origin+"/api/wifi", bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("设备请求无效")
	}
	req.Header.Set("Authorization", "Bearer "+b.token)
	req.Header.Set("Content-Type", "application/json")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return nil, errors.New("设备 HTTP 连接失败；检查 Wi-Fi 连通性和地址")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 8193))
	if err != nil || len(data) > 8192 {
		return nil, errors.New("设备返回了无效的 Wi-Fi 配置")
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("设备 Wi-Fi API 返回 HTTP %d；确认鉴权、空闲状态和 SD 配置可用，未重复写入", response.StatusCode)
	}
	return data, nil
}
func decodeWifi(p []byte) (wifiDocument, error) {
	var d wifiDocument
	if json.Unmarshal(p, &d) != nil {
		return d, errors.New("Wi-Fi JSON 无效")
	}
	return d, validateWifi(d)
}
func (b *consoleBridge) wifiOperation(ctx context.Context, r console.Request) error {
	op, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	origin, err := b.deviceOrigin(op, r.URL)
	if err != nil {
		return err
	}
	var d wifiDocument
	var raw []byte
	switch r.Action {
	case "wifi-set":
		d = wifiDocument{1, r.Profiles}
		err = validateWifi(d)
	case "wifi-update":
		raw, err = readBoundedRegular(r.Path, 8192)
		if err == nil {
			d, err = decodeWifi(raw)
		}
	default:
		raw, err = b.wifiRequest(op, origin, nil)
		if err == nil {
			d, err = decodeWifi(raw)
		}
	}
	if err != nil {
		return errors.New("Wi-Fi 配置读取或校验失败；凭据未输出")
	}
	switch r.Action {
	case "wifi-backup":
		if r.Path == "" {
			return errors.New("请选择新的私有备份文件")
		}
		f, err := os.OpenFile(r.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return errors.New("备份文件已存在或无法创建，未覆盖")
		}
		_, err = f.Write(raw)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			os.Remove(r.Path)
			return errors.New("备份保存失败")
		}
		return b.state.Result(r.Action, map[string]any{"ok": true, "path": r.Path, "networks": len(d.Networks), "mode": "0600"})
	case "wifi-add":
		found := false
		for i := range d.Networks {
			if d.Networks[i].SSID == r.SSID {
				d.Networks[i].Password = r.Password
				found = true
				break
			}
		}
		if !found {
			d.Networks = append(d.Networks, console.WiFiProfile{SSID: r.SSID, Password: r.Password})
		}
	case "wifi-remove", "wifi-move":
		if r.Index == nil || *r.Index < 1 || *r.Index > len(d.Networks) {
			return errors.New("请选择现有网络的序号")
		}
		i := *r.Index - 1
		entry := d.Networks[i]
		if r.Action == "wifi-move" && (r.To == nil || *r.To < 1 || *r.To > len(d.Networks)) {
			return errors.New("目标优先级超出列表")
		}
		d.Networks = append(d.Networks[:i], d.Networks[i+1:]...)
		if r.Action == "wifi-move" {
			to := *r.To - 1
			d.Networks = append(d.Networks, console.WiFiProfile{})
			copy(d.Networks[to+1:], d.Networks[to:])
			d.Networks[to] = entry
		}
	}
	changed := r.Action != "wifi-list"
	if changed {
		if err = validateWifi(d); err != nil {
			return err
		}
		p, _ := json.Marshal(d)
		if _, err = b.wifiRequest(op, origin, p); err != nil {
			return err
		}
	}
	networks := make([]map[string]any, 0, len(d.Networks))
	for i, n := range d.Networks {
		networks = append(networks, map[string]any{"index": i + 1, "ssid": n.SSID, "password_set": n.Password != ""})
	}
	return b.state.Result(r.Action, map[string]any{"ok": true, "url": origin, "networks": networks, "reboot_required": changed})
}
