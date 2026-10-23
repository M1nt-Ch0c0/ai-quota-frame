package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/ble"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/console"
)

func runConsole(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 || len(args) > 3 {
		fmt.Fprintln(stderr, "usage: console snapshot|status|refresh|pause|resume|switch|activate|rollback|cancel-trial|inspect|install [app-or-file] [digest], or console request JSON")
		return 2
	}
	r := console.Request{Action: args[0]}
	if r.Action == "request" {
		if len(args) != 2 || json.Unmarshal([]byte(args[1]), &r) != nil {
			fmt.Fprintln(stderr, "invalid console request")
			return 2
		}
	} else if len(args) >= 2 {
		if r.Action == "inspect" || r.Action == "install" {
			r.Path = args[1]
		} else {
			r.App = args[1]
		}
	}
	if len(args) == 3 {
		r.Digest = args[2]
	}
	path, err := console.SocketPath()
	if err != nil {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := console.Call(ctx, path, r)
	if err != nil {
		response.Error = "后台服务未连接；请启动 PhotoPainter Renderer"
	}
	_ = json.NewEncoder(stdout).Encode(response)
	if err != nil || response.Error != "" {
		return 1
	}
	return 0
}

// consoleBridge owns a single operation queue shared with the image sender.
// Native adapter serialization alone would not protect a multi-session receipt
// recovery from an intervening application switch, so the gate spans Send.
type consoleBridge struct {
	state      *console.State
	gate       chan struct{}
	jobs       chan console.Request
	id, token  string
	pause      func(bool)
	refresh    func(context.Context) error
	withClient func(context.Context, func(context.Context, consoleDeviceClient) error) error
}

func newConsoleBridge(state *console.State, id, token string, pause func(bool), refresh func(context.Context) error) *consoleBridge {
	b := &consoleBridge{state: state, gate: make(chan struct{}, 1), jobs: make(chan console.Request, 1), id: id, token: token, pause: pause, refresh: refresh}
	b.withClient = func(ctx context.Context, work func(context.Context, consoleDeviceClient) error) error {
		return ble.WithClient(ctx, id, token, func(ctx context.Context, client *ble.Client) error { return work(ctx, client) })
	}
	return b
}
func (b *consoleBridge) acquire(ctx context.Context) error {
	select {
	case b.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (b *consoleBridge) release() { <-b.gate }
func (b *consoleBridge) handle(r console.Request) console.Response {
	response := console.Response{}
	switch r.Action {
	case "snapshot":
	case "inspect":
		_, metadata, err := readConsolePackage(r.Path)
		if err != nil {
			response.Error = err.Error()
		} else {
			response.Package = &metadata
		}
	case "pause", "resume":
		b.pause(r.Action == "pause")
		if err := b.state.Automatic(r.Action == "resume"); err != nil {
			response.Error = "本次操作已生效，但无法保存设置；重启后请检查自动推图状态"
		}
	case "status", "refresh", "switch", "install", "activate", "rollback", "use-slot", "cancel-trial":
		s := b.state.Snapshot()
		if s.Operation != "" {
			response.Error = "已有操作正在执行，请等待完成"
			break
		}
		if r.Action == "cancel-trial" && s.Transfer.Uncertain {
			response.Error = "图片结果尚未确定，暂不能结束试运行"
			break
		}
		if r.Action == "switch" || r.Action == "activate" || r.Action == "rollback" || r.Action == "use-slot" {
			if s.Transfer.Uncertain {
				response.Error = "图片完成结果尚未确定，暂不能切换应用"
				break
			}
			if r.App == "" || (r.Action == "use-slot" && (r.Slot == nil || r.Version == nil)) {
				response.Error = "请选择应用与具体版本"
				break
			}
		}
		if r.Action == "install" {
			if s.Transfer.Uncertain {
				response.Error = "图片结果尚未确定，暂不能更新应用"
				break
			}
			_, metadata, err := readConsolePackage(r.Path)
			if err != nil {
				response.Error = err.Error()
				break
			}
			if r.Digest == "" || metadata.Digest != r.Digest {
				response.Error = "安装包与确认时不同，请重新选择文件"
				break
			}
		}

		b.state.Operation("queued_"+r.Action, "")
		select {
		case b.jobs <- r:
		default:
			b.state.Operation("", "")
			response.Error = "操作队列已满"
		}
	default:
		response.Error = "不支持的操作"
	}
	s := b.state.Snapshot()
	response.State = &s
	return response
}
func (b *consoleBridge) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case r := <-b.jobs:
			if err := b.acquire(ctx); err != nil {
				return
			}
			b.state.Operation(r.Action, "")
			err := b.execute(ctx, r)
			message := ""
			if err != nil {
				message = err.Error()
			}
			b.state.Operation("", message)
			b.release()
		}
	}
}
func (b *consoleBridge) readDevice(ctx context.Context) error {
	var raw json.RawMessage
	// Queries are safe to repeat. Mutations are never retried here.
	for attempt := 0; attempt < 2; attempt++ {
		readCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
		err := b.withClient(readCtx, func(ctx context.Context, c consoleDeviceClient) error {
			var err error
			raw, err = c.Status(ctx)
			return err
		})
		cancel()
		if err == nil {
			return b.state.DeviceResult(raw, true)
		}
		if ctx.Err() != nil {
			break
		}
	}
	_ = b.state.DeviceResult(nil, false)
	return errors.New("未读到设备状态。确认设备供电、蓝牙开启，再重新读取")
}
func (b *consoleBridge) execute(ctx context.Context, r console.Request) error {
	switch r.Action {
	case "refresh":
		if err := b.refresh(ctx); err != nil {
			return errors.New("额度采集未成功。保留原屏幕，检查网络与数据源")
		}
		return nil
	case "status":
		return b.readDevice(ctx)
	case "install":
		return b.installPackage(ctx, r)
	case "switch", "activate", "rollback", "use-slot":
		return b.selectSlot(ctx, r)
	case "cancel-trial":
		return b.cancelTrial(ctx)
	}
	return errors.New("不支持的操作")
}

func readConsolePackage(path string) ([]byte, console.PackageInfo, error) {
	var result console.PackageInfo
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, result, errors.New("请选择普通应用包文件")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, result, errors.New("无法读取应用包，请重新选择文件")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > ble.MaxPackageSize {
		return nil, result, errors.New("请选择大小合规的普通应用包文件")
	}
	p, err := io.ReadAll(io.LimitReader(f, ble.MaxPackageSize+1))
	if err != nil {
		return nil, result, errors.New("读取应用包失败")
	}
	metadata, err := ble.InspectPackage(p)
	if err != nil {
		return nil, result, errors.New("应用包格式或校验不正确；不能使用宿主固件或原始 ELF 文件")
	}
	digest := sha256.Sum256(p)
	result = console.PackageInfo{App: metadata.App, Version: metadata.Version, ABI: metadata.ABI, Bytes: metadata.Bytes, Digest: fmt.Sprintf("%x", digest)}
	return p, result, nil
}

func consoleTransferError(err error) string {
	if errors.Is(err, ble.ErrAppImageUnavailable) {
		return "设备当前没有可用的相框 BLE 收图服务，请读取状态并检查应用版本"
	}
	if errors.Is(err, ble.ErrImageOutcomeUnknown) {
		return "原请求回执不可用，已停止该图片的自动重试"
	}
	if errors.Is(err, ble.ErrImagePending) {
		return "尚未收到显示完成回执，下一次只查询原请求"
	}
	if errors.Is(err, ble.ErrAuthentication) {
		return "蓝牙鉴权未通过，请检查设备绑定和已有凭据"
	}
	message := err.Error()
	if strings.Contains(message, "DiscoverServices") {
		return "未完成蓝牙服务发现，请确认设备在线后重读状态"
	}
	if strings.Contains(message, "challenge") {
		return "未读到蓝牙鉴权挑战，请重新读取设备状态"
	}
	if strings.Contains(message, "timeout") || errors.Is(err, context.DeadlineExceeded) {
		return "蓝牙操作超时；未取得本轮显示成功证据"
	}
	return "本轮蓝牙推图未完成，请读取设备状态查看当前应用"
}
