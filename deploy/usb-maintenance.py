#!/usr/bin/env python3
"""Fixed PhotoPainter USB workflows; JSON output never contains Flash contents."""
import argparse
import contextlib
import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import struct
import sys
import tempfile

FLASH_BYTES = 16 * 1024 * 1024
FACTORY_OFFSET = 0x10000
FACTORY_BYTES = 0x500000
SCHEMA = "photopainter-usb-backup-v1"
control = sys.stdout


def emit(**value):
    print(json.dumps(value, ensure_ascii=False), file=control, flush=True)


def digest(data):
    return hashlib.sha256(data).hexdigest()


def normalize_id(value):
    value = value.replace(":", "").replace("-", "").lower()
    return value if re.fullmatch(r"[0-9a-f]{12}", value) else ""


def inspect_file(name, expected):
    p = Path(name).expanduser().resolve(strict=True)
    if not p.is_file():
        raise ValueError("请选择普通固件文件或本工具生成的备份清单")
    manifest = p if p.name == "manifest.json" else p.parent / "manifest.json"
    if p.name in ("manifest.json", "flash.bin") and manifest.is_file():
        m = json.loads(manifest.read_text())
        if m.get("schema") != SCHEMA or m.get("device_id") != expected:
            raise ValueError("备份格式或设备身份不匹配")
        flash = manifest.parent / "flash.bin"
        if flash.stat().st_size != FLASH_BYTES:
            raise ValueError("备份不是完整的 16 MiB Flash")
        data = flash.read_bytes()
        sha = digest(data)
        if sha != m.get("sha256"):
            raise ValueError("备份校验失败，未操作设备")
        return {"type": "backup", "path": str(manifest), "bytes": len(data),
                "digest": sha, "device_id": expected}, data
    sidecar = Path(str(p) + ".json")
    if p.suffix.lower() == ".bin" and sidecar.is_file():
        m = json.loads(sidecar.read_text())
        if m.get("schema") == "photopainter-nvs-v1":
            if m.get("device_id") != expected or p.stat().st_size != 0x6000:
                raise ValueError("NVS 配置的设备身份或大小不匹配")
            data = p.read_bytes()
            sha = digest(data)
            if m.get("sha256") != sha:
                raise ValueError("NVS 配置校验失败")
            return {"type": "nvs", "path": str(p), "bytes": len(data),
                    "digest": sha, "device_id": expected}, data
    if p.suffix.lower() != ".bin" or not 32 <= p.stat().st_size <= FACTORY_BYTES:
        raise ValueError("宿主更新需要不超过 factory 容量的 .bin 文件")
    data = p.read_bytes()
    from esptool import bin_image
    with contextlib.redirect_stdout(sys.stderr):
        image = bin_image.LoadFirmwareImage("esp32s3", data)
    if image.chip_id != 9:
        raise ValueError("固件目标不是 ESP32-S3")
    return {"type": "host-firmware", "path": str(p), "bytes": len(data),
            "digest": digest(data), "device_id": expected}, data


def assert_layout(data):
    found = False
    found_nvs = False
    for offset in range(0x8000, 0x9000, 32):
        magic, kind, subtype, start, size, label, flags = struct.unpack_from("<HBBII16sI", data, offset)
        if magic != 0x50AA:
            break
        if kind == 0 and subtype == 0 and label.rstrip(b"\0") == b"factory":
            found = start == FACTORY_OFFSET and size == FACTORY_BYTES
        if kind == 1 and subtype == 2 and label.rstrip(b"\0") == b"nvs":
            found_nvs = start == 0x9000 and size == 0x6000
    if not found or not found_nvs:
        raise ValueError("设备分区布局不匹配，未写入固件")


def check_device_hash(esp, offset, data):
    actual = esp.flash_md5sum(offset, len(data))
    if isinstance(actual, bytes):
        actual = actual.hex()
    if actual.lower() != hashlib.md5(data).hexdigest():
        raise ValueError("设备回读摘要不一致")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--action", required=True, choices=["usb-ports", "usb-inspect", "usb-backup", "usb-flash", "usb-restore", "usb-provision"])
    parser.add_argument("--expected-id", required=True)
    parser.add_argument("--port", default="")
    parser.add_argument("--path", default="")
    parser.add_argument("--digest", default="")
    parser.add_argument("--credentials-confirmed", action="store_true")
    parser.add_argument("--output-root", required=True)
    args = parser.parse_args()
    expected = normalize_id(args.expected_id)
    if not expected:
        raise ValueError("请先绑定目标 PhotoPainter")
    from serial.tools import list_ports
    ports = list(list_ports.comports())
    if args.action == "usb-ports":
        emit(complete=True, ports=[{"port": p.device, "description": p.description,
              "device_id": normalize_id(p.serial_number or ""),
              "matches_expected": normalize_id(p.serial_number or "") == expected}
             for p in ports if p.vid is not None], device_id=expected)
        return
    if args.action == "usb-inspect":
        info, _ = inspect_file(args.path, expected)
        emit(complete=True, **info)
        return
    if args.port not in [p.device for p in ports if p.vid is not None]:
        raise ValueError("所选 USB 串口已不可用，请重新扫描")
    info, candidate = None, None
    if args.action in ("usb-flash", "usb-restore", "usb-provision"):
        info, candidate = inspect_file(args.path, expected)
        wanted = {"usb-flash": "host-firmware", "usb-restore": "backup", "usb-provision": "nvs"}[args.action]
        if info["type"] != wanted or not args.digest or info["digest"] != args.digest:
            raise ValueError("文件与确认时不一致，未写入设备")
        if args.action == "usb-provision" and not args.credentials_confirmed:
            raise ValueError("请明确确认此操作会更改 Wi-Fi/设备令牌，现有连接可能失效")
    import esptool
    root = Path(args.output_root).resolve()
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    root.chmod(0o700)
    prefix = datetime.datetime.now().strftime("%Y%m%dT%H%M%S-")
    work = Path(tempfile.mkdtemp(prefix=prefix, dir=root))
    log_path = work / "maintenance.log"
    esp, before, writing, restored = None, None, False, False
    error = None
    with log_path.open("w") as log, contextlib.redirect_stdout(log), contextlib.redirect_stderr(log):
        try:
            emit(phase="connecting", message="正在识别设备；串口连接会触发复位")
            esp = esptool.detect_chip(args.port, 460800)
            if esp.CHIP_NAME != "ESP32-S3" or bytes(esp.read_mac()).hex() != expected:
                raise ValueError("串口设备身份不匹配，未写入设备")
            esp = esp.run_stub()
            emit(phase="backup", message="正在保存完整 16 MiB Flash 备份")
            flash = work / "flash.bin"
            esptool.read_flash(esp, 0, FLASH_BYTES, str(flash), no_progress=True)
            flash.chmod(0o600)
            before = flash.read_bytes()
            if len(before) != FLASH_BYTES:
                raise ValueError("完整备份未完成，未写入设备")
            emit(phase="verify-backup", message="正在核对备份与设备摘要")
            check_device_hash(esp, 0, before)
            manifest = {"schema": SCHEMA, "device_id": expected, "bytes": FLASH_BYTES,
                        "sha256": digest(before), "created_at": datetime.datetime.now(datetime.timezone.utc).isoformat()}
            manifest_path = work / "manifest.json"
            manifest_path.write_text(json.dumps(manifest, indent=2) + "\n")
            manifest_path.chmod(0o600)
            if args.action == "usb-flash":
                assert_layout(before)
                emit(phase="write-host", message="正在更新宿主 factory 分区")
                writing = True
                esptool.write_flash(esp, [(FACTORY_OFFSET, candidate)], compress=True)
                esptool.verify_flash(esp, [(FACTORY_OFFSET, candidate)])
                check_device_hash(esp, 0, before[:FACTORY_OFFSET])
                tail = FACTORY_OFFSET + FACTORY_BYTES
                check_device_hash(esp, tail, before[tail:])
                writing = False
            elif args.action == "usb-restore":
                emit(phase="restore", message="正在恢复所选设备备份")
                writing = True
                esptool.write_flash(esp, [(0, candidate)], compress=True)
                esptool.verify_flash(esp, [(0, candidate)])
                writing = False
            elif args.action == "usb-provision":
                assert_layout(before)
                emit(phase="provision", message="正在更新 NVS 配置；现有连接凭据可能失效")
                writing = True
                esptool.write_flash(esp, [(0x9000, candidate)], compress=True)
                esptool.verify_flash(esp, [(0x9000, candidate)])
                check_device_hash(esp, 0, before[:0x9000])
                check_device_hash(esp, 0xF000, before[0xF000:])
                writing = False
            emit(phase="restart", message="正在重新启动设备")
        except Exception as exc:
            error = str(exc)
            if writing and esp is not None and before is not None:
                try:
                    emit(phase="rollback", message="更新失败，正在恢复操作前备份")
                    if args.action == "usb-flash":
                        original = before[FACTORY_OFFSET:FACTORY_OFFSET + FACTORY_BYTES]
                        esptool.write_flash(esp, [(FACTORY_OFFSET, original)], compress=True)
                        esptool.verify_flash(esp, [(FACTORY_OFFSET, original)])
                    elif args.action == "usb-provision":
                        original = before[0x9000:0xF000]
                        esptool.write_flash(esp, [(0x9000, original)], compress=True)
                        esptool.verify_flash(esp, [(0x9000, original)])
                    else:
                        esptool.write_flash(esp, [(0, before)], compress=True)
                        esptool.verify_flash(esp, [(0, before)])
                    restored = True
                except Exception:
                    error += "；自动恢复未完成，请保持 USB 连接并使用本次备份恢复"
        finally:
            if esp is not None:
                try:
                    esptool.reset_chip(esp, "hard-reset")
                    esp._port.close()
                except Exception:
                    pass
    log_path.chmod(0o600)
    if error:
        emit(complete=False, error=error, restored=restored, backup_directory=str(work))
        raise SystemExit(1)
    emit(complete=True, phase="complete", device_id=expected,
         backup_manifest=str(work / "manifest.json"), backup_directory=str(work),
         bytes=FLASH_BYTES, digest=digest(before), operation=args.action,
         credentials_may_have_changed=args.action == "usb-provision")


if __name__ == "__main__":
    try:
        main()
    except SystemExit:
        raise
    except Exception as exc:
        emit(complete=False, error=str(exc))
        raise SystemExit(1)
