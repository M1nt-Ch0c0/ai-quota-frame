package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/ble"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/console"
)

type consoleDeviceClient interface {
	Status(context.Context) (json.RawMessage, error)
	Manage(context.Context, byte, string) error
	Install(context.Context, []byte, func(int, int)) error
}

func consoleApp(d *console.Device, id string) *console.App {
	if d != nil {
		for i := range d.Apps {
			if d.Apps[i].ID == id {
				return &d.Apps[i]
			}
		}
	}
	return nil
}
func slotVersion(a *console.App, slot int) (uint32, bool) {
	if a == nil || slot < 0 || slot > 1 || slot >= len(a.Versions) || a.Versions[slot] == nil {
		return 0, false
	}
	return *a.Versions[slot], true
}

// Slot selection is based on a new signed catalog, never button_eligible or
// the age of the UI snapshot. The requested version binds the user's choice.
func planConsoleSlot(r console.Request, d *console.Device) (console.App, int, uint32, byte, error) {
	a := consoleApp(d, r.App)
	if a == nil {
		return console.App{}, 0, 0, 0, errors.New("设备上没有这个应用，请重新读取状态")
	}
	slot := a.Active
	switch r.Action {
	case "activate":
		slot = a.Pending
	case "rollback":
		slot = a.Previous
	case "use-slot":
		if r.Slot == nil || r.Version == nil {
			return *a, 0, 0, 0, errors.New("请选择具体的 A/B 槽版本")
		}
		slot = *r.Slot
	}
	version, ok := slotVersion(a, slot)
	if !ok {
		return *a, slot, 0, 0, errors.New("目标槽没有有效应用版本")
	}
	if r.Version != nil && *r.Version != version {
		return *a, slot, 0, 0, errors.New("设备上的版本已变化，请重新选择")
	}
	if d.TrialApp >= 0 {
		return *a, slot, version, 0, errors.New("设备有尚未确认的应用试运行，请先完成确认或回退")
	}
	var op byte
	switch slot {
	case a.Active:
		op = ble.OpSwitch
	case a.Pending:
		op = ble.OpActivate
	case a.Previous:
		if a.Pending >= 0 {
			return *a, slot, version, 0, errors.New("设备还有待激活更新，暂不能切换回退版本")
		}
		op = ble.OpRollback
	default:
		if a.Pending >= 0 || a.Active < 0 || slot != 1-a.Active {
			return *a, slot, version, 0, errors.New("备用槽状态已变化，请重新读取设备状态")
		}
		// No host API selects an untracked spare. Restage an explicitly
		// reviewed matching package, preserving the active slot.
		if r.Path == "" {
			return *a, slot, version, 0, errors.New("此版本需要重新安装后才能使用，请选择对应应用包重新安装并激活")
		}
	}
	return *a, slot, version, op, nil
}

func selectionReached(d *console.Device, a console.App, slot int, version uint32) bool {
	if d == nil || d.LoadedApp != a.Bank || d.Loaded != slot || d.Version != version || !d.ELFReady {
		return false
	}
	// ABI 1 has no START screen. Loading it is a successful manual selection;
	// the UI separately states that PNG input is still needed for confirmation.
	if d.ABI == 1 {
		return true
	}
	current := consoleApp(d, a.ID)
	return d.Ready && d.Confirmed && current != nil && current.Active == slot
}

func (b *consoleBridge) manageOnce(ctx context.Context, op byte, app string) error {
	// A missing GATT response must never replay a mutation. Reconnect only to
	// read its signed outcome, including when Manage reports a cleanup error.
	_ = b.withClient(ctx, func(ctx context.Context, c consoleDeviceClient) error { return c.Manage(ctx, op, app) })
	if err := b.readDevice(ctx); err != nil {
		return errors.New("操作结果暂不确定；没有重复执行，请重新读取设备状态")
	}
	return nil
}

func (b *consoleBridge) cancelTrial(ctx context.Context) error {
	if b.state.Snapshot().Transfer.Uncertain {
		return errors.New("图片结果尚未确定，未结束试运行")
	}
	ctx, cancel := context.WithTimeout(ctx, 11*time.Minute)
	defer cancel()
	if err := b.readDevice(ctx); err != nil {
		return err
	}
	d := b.state.Snapshot().Device
	if d.TrialApp < 0 {
		return nil
	}
	for _, a := range d.Apps {
		if a.Bank == d.TrialApp {
			if err := b.manageOnce(ctx, ble.OpRollback, a.ID); err != nil {
				return err
			}
			if b.state.Snapshot().Device.TrialApp < 0 {
				return nil
			}
			break
		}
	}
	return errors.New("设备尚未确认试运行已结束，请重新读取状态")
}

func (b *consoleBridge) selectSlot(ctx context.Context, r console.Request) error {
	if b.state.Snapshot().Transfer.Uncertain {
		return errors.New("图片完成结果尚未确定，未执行版本切换")
	}
	operationCtx, cancel := context.WithTimeout(ctx, 11*time.Minute)
	defer cancel()
	if err := b.readDevice(operationCtx); err != nil {
		return err
	}
	a, slot, version, op, err := planConsoleSlot(r, b.state.Snapshot().Device)
	if err != nil {
		return err
	}
	if op == 0 {
		pkg, metadata, err := readConsolePackage(r.Path)
		if err != nil {
			return err
		}
		if r.Digest == "" || metadata.Digest != r.Digest || metadata.App != a.ID || metadata.Version != version {
			return errors.New("请选择同一应用、同一版本且已确认的应用包；未写入设备")
		}
		b.state.Operation("restaging", "")
		_ = b.withClient(operationCtx, func(ctx context.Context, c consoleDeviceClient) error {
			return c.Install(ctx, pkg, b.state.PackageProgress)
		})
		if err := b.readDevice(operationCtx); err != nil {
			return errors.New("重新安装结果暂不确定；没有重传或激活，请重新读取状态")
		}
		after := b.state.Snapshot().Device
		staged := consoleApp(after, a.ID)
		v, valid := slotVersion(staged, slot)
		if !valid || v != version || staged.Bank != a.Bank || staged.Active != a.Active || staged.Pending != slot || after.TrialApp >= 0 {
			return errors.New("设备未确认目标版本已暂存，未继续激活")
		}
		op = ble.OpActivate
	}
	if op == ble.OpRollback {
		b.state.Operation("rollback", "")
		if err := b.manageOnce(operationCtx, op, a.ID); err != nil {
			return err
		}
		d := b.state.Snapshot().Device
		updated := consoleApp(d, a.ID)
		v, ok := slotVersion(updated, slot)
		if !ok || v != version || updated.Bank != a.Bank || updated.Active != slot {
			return errors.New("设备未确认目标回退版本，未继续切换应用")
		}
		if selectionReached(d, a, slot, version) {
			return nil
		}
		// Rollback of an inactive app only changes its catalog. Selecting that
		// app is a distinct command, sent only after the catalog is verified.
		if d.LoadedApp == a.Bank {
			return errors.New("目标版本尚未就绪，请检查设备状态")
		}
		op = ble.OpSwitch
	}
	if selectionReached(b.state.Snapshot().Device, a, slot, version) {
		return nil
	}
	if op == ble.OpActivate {
		b.state.Operation("activate", "")
	} else {
		b.state.Operation("switch", "")
	}
	if err := b.manageOnce(operationCtx, op, a.ID); err != nil {
		return err
	}
	if !selectionReached(b.state.Snapshot().Device, a, slot, version) {
		return errors.New("设备尚未运行所选槽的版本，请检查当前应用与重启记录")
	}
	return nil
}

func (b *consoleBridge) installPackage(ctx context.Context, r console.Request) error {
	if b.state.Snapshot().Transfer.Uncertain {
		return errors.New("图片结果尚未确定，未安装应用")
	}
	pkg, metadata, err := readConsolePackage(r.Path)
	if err != nil {
		return err
	}
	if r.Digest == "" || metadata.Digest != r.Digest {
		return errors.New("安装包已改变，未上传")
	}
	operationCtx, cancel := context.WithTimeout(ctx, 90*time.Minute)
	defer cancel()
	if err := b.readDevice(operationCtx); err != nil {
		return err
	}
	d := b.state.Snapshot().Device
	if d.TrialApp >= 0 {
		return errors.New("设备有尚未确认的应用试运行，未安装应用")
	}
	target := 0
	if a := consoleApp(d, metadata.App); a != nil {
		if a.Pending >= 0 {
			return errors.New("此应用已有待激活版本，请先处理该版本")
		}
		if a.Active >= 0 {
			target = 1 - a.Active
		}
		for _, v := range a.Versions {
			if v != nil && metadata.Version <= *v {
				return errors.New("新包版本必须高于已保存版本；恢复已有版本请使用该槽的重新安装入口")
			}
		}
	}
	_ = b.withClient(operationCtx, func(ctx context.Context, c consoleDeviceClient) error {
		return c.Install(ctx, pkg, b.state.PackageProgress)
	})
	if err := b.readDevice(operationCtx); err != nil {
		return errors.New("未确认应用暂存结果；没有自动重传，请重新读取设备状态")
	}
	a := consoleApp(b.state.Snapshot().Device, metadata.App)
	version, ok := slotVersion(a, target)
	if !ok || version != metadata.Version || a.Pending != target {
		return errors.New("设备未确认新包暂存，请检查版本与设备状态")
	}
	return nil
}
