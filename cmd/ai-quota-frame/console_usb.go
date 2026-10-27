package main

import (
	"bufio"
	"context"
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

// USB commands are fixed workflows, never a shell or arbitrary esptool command.
// They execute behind the same gate as BLE and pause automatic image pushes.
func (b *consoleBridge) executeUSB(ctx context.Context, r console.Request) error {
	readonly := r.Action == "usb-ports" || r.Action == "usb-inspect"
	if !readonly && !r.Confirmed {
		return errors.New("请确认 USB 操作会重启设备；写入前将完整备份")
	}
	if !readonly && b.state.Snapshot().Transfer.Uncertain {
		return errors.New("图片结果尚未确定，未执行 USB 复位")
	}
	directory, err := console.Directory()
	if err != nil {
		return err
	}
	script := filepath.Join(directory, "usb-maintenance.py")
	if _, err = os.Stat(script); err != nil {
		return errors.New("USB 维护组件尚未安装，请更新 PhotoPainter 后台")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	candidates, _ := filepath.Glob(filepath.Join(home, ".espressif", "python_env", "*", "bin", "python"))
	sort.Sort(sort.Reverse(sort.StringSlice(candidates)))
	python := ""
	for _, p := range candidates {
		if st, e := os.Stat(p); e == nil && !st.IsDir() {
			python = p
			break
		}
	}
	if python == "" {
		python, _ = exec.LookPath("python3")
	}
	if python == "" {
		return errors.New("未找到 Python 和 esptool，请先安装设备工具链")
	}
	if !readonly {
		b.pause(true)
		if err = b.state.Automatic(false); err != nil {
			return errors.New("无法保存暂停状态，未执行 USB 维护")
		}
	}
	timeout := 30 * time.Minute
	if readonly {
		timeout = 30 * time.Second
	}
	operation, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := []string{script, "--action", r.Action, "--expected-id", b.id, "--output-root", filepath.Join(directory, "console", "usb-backups")}
	if r.Port != "" {
		args = append(args, "--port", r.Port)
	}
	if r.Path != "" {
		args = append(args, "--path", r.Path)
	}
	if r.Digest != "" {
		args = append(args, "--digest", r.Digest)
	}
	if r.CredentialsConfirmed {
		args = append(args, "--credentials-confirmed")
	}
	command := exec.CommandContext(operation, python, args...)
	pipe, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	// Tool stderr can include local paths; it is not copied to the GUI or log.
	command.Stderr = nil
	if err = command.Start(); err != nil {
		return errors.New("无法启动 USB 维护组件")
	}
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 4096), 256*1024)
	var last map[string]any
	for scanner.Scan() {
		var value map[string]any
		if json.Unmarshal(scanner.Bytes(), &value) == nil {
			last = value
			if err = b.state.Result(r.Action, value); err != nil {
				break
			}
		}
	}
	waitErr := command.Wait()
	if operation.Err() != nil {
		return errors.New("USB 维护等待已结束；请保持连接并检查操作前备份，勿盲目重试写入")
	}
	if scanner.Err() != nil {
		return errors.New("USB 维护结果读取失败，请检查备份目录")
	}
	if message, ok := last["error"].(string); ok && message != "" {
		if restored, ok := last["restored"].(bool); ok && restored {
			message += "；已恢复操作前内容"
		}
		return errors.New(strings.TrimSpace(message))
	}
	if waitErr != nil || last == nil {
		return errors.New("USB 维护组件未成功返回；请检查 Python/esptool 安装")
	}
	return nil
}
