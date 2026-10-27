package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/console"
)

func consolePython() string {
	home, _ := os.UserHomeDir()
	candidates, _ := filepath.Glob(filepath.Join(home, ".espressif", "python_env", "*", "bin", "python"))
	sort.Sort(sort.Reverse(sort.StringSlice(candidates)))
	for _, p := range candidates {
		if fileExists(p) {
			return p
		}
	}
	p, _ := exec.LookPath("python3")
	return p
}

func (b *consoleBridge) serialDiagnose(ctx context.Context, r console.Request) error {
	if r.Port == "" {
		return errors.New("请选择精确的设备串口")
	}
	python := consolePython()
	if python == "" {
		return errors.New("未找到 Python/串口工具")
	}
	op, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	// Enumerating port descriptors does not open the device. Require this
	// already-configured ESP32-S3 before the reset-capable serial read.
	code := "import json;from serial.tools import list_ports;print(json.dumps([{'port':p.device,'vid':p.vid,'pid':p.pid,'serial':p.serial_number} for p in list_ports.comports()]))"
	output, err := exec.CommandContext(op, python, "-c", code).Output()
	if err != nil {
		return errors.New("无法枚举 USB 串口身份，未打开设备")
	}
	var ports []struct {
		Port   string `json:"port"`
		VID    int    `json:"vid"`
		PID    int    `json:"pid"`
		Serial string `json:"serial"`
	}
	if json.Unmarshal(output, &ports) != nil {
		return errors.New("串口身份输出无效")
	}
	found := false
	for _, p := range ports {
		serial := strings.ToLower(strings.ReplaceAll(p.Serial, ":", ""))
		if p.Port == r.Port && p.VID == 0x303a && p.PID == 0x1001 && serial == b.id {
			found = true
		}
	}
	if !found {
		return errors.New("所选串口不匹配当前绑定的 ESP32-S3，未复位设备")
	}
	script := filepath.Join(consoleHostRepo(), "tools", "doctor.py")
	if !fileExists(script) {
		return errors.New("未找到宿主诊断工具")
	}
	b.pause(true)
	if err = b.state.Automatic(false); err != nil {
		return errors.New("无法保存暂停状态，未打开串口")
	}
	output, err = exec.CommandContext(op, python, script, "--json", "--probe-serial", "--serial", r.Port, "--serial-seconds", "8").Output()
	if err != nil {
		return errors.New("串口诊断未完成；设备可能已复位，请保持供电并重新读取状态")
	}
	var checks any
	if json.Unmarshal(output, &checks) != nil {
		return errors.New("串口诊断结果格式无效")
	}
	_ = b.state.DeviceResult(nil, false)
	return b.state.Result(r.Action, map[string]any{"checks": checks, "serial_opened": true, "automatic_paused": true, "port": r.Port})
}

func readLocalWifi(path string) (wifiDocument, string, []byte, error) {
	d := wifiDocument{Version: 1, Networks: []console.WiFiProfile{}}
	if path == "" {
		return d, "", nil, errors.New("请选择 Wi-Fi JSON 文件")
	}
	source := "main"
	selected := path
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		source = "backup"
		selected = path + ".bak"
	} else if err != nil {
		return d, "", nil, errors.New("无法访问配置文件")
	}
	st, err := os.Lstat(selected)
	if os.IsNotExist(err) {
		return d, "missing", nil, nil
	}
	if err != nil || !st.Mode().IsRegular() {
		return d, "", nil, errors.New("Wi-Fi 配置须为普通文件，不能是链接或目录")
	}
	raw, err := readBoundedRegular(selected, 8192)
	if err != nil {
		return d, "", nil, errors.New("Wi-Fi 文件读取失败或超过 8192 字节")
	}
	d, err = decodeWifi(raw)
	return d, source, raw, err
}
func saveLocalWifi(path string, d wifiDocument) error {
	if d.Networks == nil {
		d.Networks = []console.WiFiProfile{}
	}
	if err := validateWifi(d); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.New("无法创建配置目录")
	}
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(filepath.Dir(path), ".photopainter-wifi-*")
	if err != nil {
		return errors.New("无法创建临时配置")
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, err = temp.Write(data); err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return errors.New("临时配置写入失败，原文件保留")
	}
	existed := false
	if st, e := os.Lstat(path); e == nil {
		if !st.Mode().IsRegular() {
			return errors.New("目标已变为链接或目录，未覆盖")
		}
		if err = os.Rename(path, path+".bak"); err != nil {
			return errors.New("无法保存原配置，未发布新文件")
		}
		existed = true
	} else if !os.IsNotExist(e) {
		return errors.New("无法检查目标文件")
	}
	if err = os.Rename(name, path); err != nil {
		if existed {
			_ = os.Rename(path+".bak", path)
		}
		return errors.New("配置发布失败，已尝试保留原文件")
	}
	// Same .bak recovery semantics as the existing SD JSON CLI.
	_ = os.Remove(path + ".bak")
	return nil
}
func (b *consoleBridge) wifiFileOperation(r console.Request) error {
	d, source, raw, err := readLocalWifi(r.Path)
	if err != nil {
		return errors.New("本地 Wi-Fi JSON 无效或无法读取；没有回显密码，也没有改写文件")
	}
	if r.Digest != "" {
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != r.Digest {
			return errors.New("文件已在读取后变化，未修改")
		}
	}
	switch r.Action {
	case "wifi-file-status":
	case "wifi-file-upsert":
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
	case "wifi-file-remove", "wifi-file-move":
		if r.Index == nil || *r.Index < 1 || *r.Index > len(d.Networks) {
			return errors.New("请选择现有网络的序号")
		}
		if r.Action == "wifi-file-move" && (r.To == nil || *r.To < 1 || *r.To > len(d.Networks)) {
			return errors.New("目标优先级超出列表")
		}
		index := *r.Index - 1
		entry := d.Networks[index]
		d.Networks = append(d.Networks[:index], d.Networks[index+1:]...)
		if r.Action == "wifi-file-move" {
			to := *r.To - 1
			d.Networks = append(d.Networks, console.WiFiProfile{})
			copy(d.Networks[to+1:], d.Networks[to:])
			d.Networks[to] = entry
		}
	default:
		return errors.New("不支持的本地 Wi-Fi 操作")
	}
	if r.Action != "wifi-file-status" {
		if err = saveLocalWifi(r.Path, d); err != nil {
			return err
		}
		source = "main"
		raw, err = readBoundedRegular(r.Path, 8192)
		if err != nil {
			return errors.New("已写入，但无法确认最终文件")
		}
	}
	networks := make([]map[string]any, 0, len(d.Networks))
	for i, n := range d.Networks {
		networks = append(networks, map[string]any{"index": i + 1, "ssid": n.SSID, "password_set": n.Password != ""})
	}
	sum := sha256.Sum256(raw)
	return b.state.Result(r.Action, map[string]any{"ok": true, "path": r.Path, "source": source, "networks": networks, "digest": hex.EncodeToString(sum[:]), "device_modified": false})
}

func (b *consoleBridge) generateProvision(ctx context.Context, r console.Request) error {
	if r.Path == "" || r.Output == "" {
		return errors.New("请选择私有配置输入和新的 NVS 输出文件")
	}
	for _, p := range []string{r.Output, r.Output + ".json"} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			return errors.New("NVS 输出或配套清单已存在，未覆盖")
		}
	}
	script := filepath.Join(consoleHostRepo(), "tools", "provision.py")
	if !fileExists(script) {
		return errors.New("未找到可信 NVS 配置工具")
	}
	python := consolePython()
	if python == "" {
		return errors.New("未找到 Python 工具链")
	}
	temp, err := os.MkdirTemp(filepath.Dir(r.Output), ".photopainter-nvs-*")
	if err != nil {
		return errors.New("无法创建私有暂存目录")
	}
	defer os.RemoveAll(temp)
	binary := filepath.Join(temp, "config.bin")
	args := []string{script, "--config", r.Path, "--generate-only", binary}
	if r.WiFiPath != "" {
		args = append(args, "--wifi-json", r.WiFiPath)
	}
	op, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	command := exec.CommandContext(op, python, args...)
	idf := os.Getenv("IDF_PATH")
	if idf == "" {
		home, _ := os.UserHomeDir()
		idf = filepath.Join(home, "opensource", "esp-idf")
	}
	command.Env = append(os.Environ(), "IDF_PATH="+idf)
	// The trusted provisioning tool consumes the selected private configuration.
	// Its stdout/stderr and the resulting NVS content are never returned to UI.
	if err = command.Run(); err != nil {
		return errors.New("配置校验或 NVS 生成失败；请核对私有配置和固定版本 ESP-IDF，凭据未输出")
	}
	data, err := readBoundedRegular(binary, 0x6000)
	if err != nil || len(data) != 0x6000 {
		return errors.New("生成的 NVS 长度不符合固定分区")
	}
	digest := sha256.Sum256(data)
	manifest := map[string]any{"schema": "photopainter-nvs-v1", "device_id": b.id, "bytes": len(data), "sha256": hex.EncodeToString(digest[:])}
	metadata, _ := json.MarshalIndent(manifest, "", "  ")
	sidecar := filepath.Join(temp, "config.json")
	if err = os.WriteFile(sidecar, metadata, 0600); err != nil {
		return errors.New("无法生成安全清单")
	}
	if err = os.Chmod(binary, 0600); err != nil {
		return errors.New("无法设置 NVS 文件私有权限")
	}
	if err = os.Link(binary, r.Output); err != nil {
		return errors.New("NVS 输出发生冲突，未覆盖")
	}
	if err = os.Link(sidecar, r.Output+".json"); err != nil {
		_ = os.Remove(r.Output)
		return errors.New("清单发布失败，未留下未登记的 NVS 文件")
	}
	return b.state.Result(r.Action, map[string]any{"ok": true, "path": r.Output, "type": "nvs", "bytes": len(data), "digest": hex.EncodeToString(digest[:]), "device_id": b.id, "contains_credentials": true, "manifest": r.Output + ".json", "device_modified": false})
}
