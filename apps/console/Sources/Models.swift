import Foundation
import SwiftUI
import AppKit

struct ConsoleResponse: Decodable {
    let state: ServiceSnapshot?; let error: String?; let packageInfo: PackageMetadata?
    enum CodingKeys: String, CodingKey { case state, error, packageInfo = "package" }
}
struct PackageMetadata: Decodable { let app: String; let version: Int; let abi: Int; let bytes: Int; let digest: String }
struct PackageSelection: Identifiable { let path: String; let metadata: PackageMetadata; let target: SlotSelection?; var id: String { metadata.digest } }
struct SlotSelection: Identifiable {
    let app: DeviceApplication; let slot: Int
    var id: String { "\(app.id)-\(slot)" }
    var version: Int? { app.version(at: slot) }
    var label: String { "\(app.displayName) · \(slot == 0 ? "A" : "B") 槽 v\(version ?? 0)" }
    var needsPackage: Bool { slot != app.active && slot != app.pending && slot != app.previous }
}
struct ServiceSnapshot: Decodable {
    let schema: Int
    let startedAt: String
    let observedAt: String
    let automatic: Bool
    let intervalSeconds: Int
    let theme: String
    let transport: String
    let deviceId: String
    let sourceState: String
    let lastFetch: String?
    let nextFetch: String?
    let accountCount: Int
    let warningCount: Int
    let transfer: TransferState
    let device: DeviceState?
    let deviceObservedAt: String?
    let deviceError: String?
    let operation: String?
    let operationError: String?
    let operationDone: Int?
    let operationTotal: Int?
    let previewPath: String?
    let events: [ConsoleEvent]
}
struct TransferState: Decodable {
    let message: String?
    let stage: String
    let done: Int
    let total: Int
    let startedAt: String?
    let completedAt: String?
    let uncertain: Bool
    var active: Bool { ["rendering", "connecting", "uploading", "displaying", "recovering"].contains(stage) }
    var title: String {
        switch stage {
        case "rendering": return "正在生成图片"
        case "connecting": return "正在连接设备并检查收图应用"
        case "uploading": return "正在通过蓝牙上传"
        case "displaying": return "等待屏幕刷新完成"
        case "recovering": return "正在查询完成回执"
        case "complete": return "屏幕已确认刷新"
        case "unchanged": return "画面未变，无需重复刷新"
        case "unknown": return "这次刷新结果尚不确定"
        case "rejected": return "本轮推图已停止"
        case "failed": return "推图未成功，等待重试"
        default: return "等待下一次数据更新"
        }
    }
}
struct DeviceState: Decodable {
    let loadedApp: Int
    let loaded: Int?
    let ready: Bool?
    let trialApp: Int?
    let version: Int
    let payloadAbi: Int
    let runtimeReady: Bool
    let confirmed: Bool
    let generation: Int
    let uptimeMs: UInt64
    let resetReason: Int
    let freePsram: Int
    let freeInternalRam: Int
    let cacheBytes: Int
    let bleCharacteristics: Int
    let lastSwitchSource: String
    let apps: [DeviceApplication]
    var currentName: String { apps.first(where: { $0.bank == loadedApp })?.id ?? "未运行应用" }
}
struct DeviceApplication: Decodable, Identifiable {
    let id: String
    let bank: Int
    let active: Int
    let previous: Int
    let pending: Int
    let versions: [Int?]
    let abi: Int
    let buttonEligible: Bool
    func version(at slot: Int) -> Int? { versions.indices.contains(slot) ? versions[slot] : nil }
    var version: Int? { version(at: active) }
    var displayName: String { id == "photoframe" ? "相框" : id == "color-test" ? "六色色条" : id }
}
struct ConsoleEvent: Decodable, Identifiable {
    let at: String
    let kind: String
    let message: String
    var id: String { at + kind + message }
}
func parsedDate(_ value: String?) -> Date? {
    guard let value else { return nil }
    let formatter = ISO8601DateFormatter()
    formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    return formatter.date(from: value) ?? ISO8601DateFormatter().date(from: value)
}
func clockText(_ value: String?) -> String {
    guard let date = parsedDate(value) else { return "尚无记录" }
    return date.formatted(date: .omitted, time: .standard)
}
func bytesText(_ bytes: Int) -> String { ByteCountFormatter.string(fromByteCount: Int64(bytes), countStyle: .memory) }

@MainActor
final class ConsoleModel: ObservableObject {
    @Published var snapshot: ServiceSnapshot?
    @Published var connected = false
    @Published var error = ""
    @Published var submitting = false
    @Published var hasPolled = false
    @Published var preview: NSImage?
    @Published var pendingPackage: PackageSelection?
    private var previewStamp = ""
    private var polling = false
    private var connectionError = false
    let binary = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Application Support/ai-quota-frame/ai-quota-frame")
    var busy: Bool { submitting || !(snapshot?.operation ?? "").isEmpty }
    var deviceFresh: Bool {
        guard connected, let at = parsedDate(snapshot?.deviceObservedAt), (snapshot?.deviceError ?? "").isEmpty else { return false }
        return Date().timeIntervalSince(at) < 120
    }
    func runPolling() async {
        guard !polling else { return }; polling = true
        defer { polling = false }
        while !Task.isCancelled {
            await request("snapshot")
            try? await Task.sleep(for: .seconds(2))
        }
    }
    func command(_ action: String, app: String? = nil, digest: String? = nil, target: SlotSelection? = nil) {
        guard !submitting else { return }
        submitting = true
        Task { await request(action, app: app, digest: digest, target: target); submitting = false }
    }
    func request(_ action: String, app: String? = nil, digest: String? = nil, target: SlotSelection? = nil) async {
        let path = binary.path
        var payload: [String: Any] = ["action": action]
        if let app { payload[action == "inspect" || action == "install" || (action == "use-slot" && digest != nil) ? "path" : "app"] = app }
        if let digest { payload["digest"] = digest }
        if let target {
            payload["app"] = target.app.id; payload["slot"] = target.slot
            if let version = target.version { payload["version"] = version }
        }
        guard let requestData = try? JSONSerialization.data(withJSONObject: payload), let requestText = String(data: requestData, encoding: .utf8) else { return }
        let result = await Task.detached(priority: .utility) { () -> (Data?, String?) in
            let process = Process(); let output = Pipe(); let errors = Pipe()
            process.executableURL = URL(fileURLWithPath: path)
            process.arguments = ["console", "request", requestText]
            process.standardOutput = output; process.standardError = errors
            do {
                try process.run()
                let data = output.fileHandleForReading.readDataToEndOfFile()
                process.waitUntilExit()
                return (data, nil)
            } catch { return (nil, "无法打开本地后台程序。请检查 PhotoPainter Renderer 是否已安装。") }
        }.value
        hasPolled = true
        guard let data = result.0 else { connected = false; connectionError = true; error = result.1 ?? "后台未连接"; return }
        do {
            let decoder = JSONDecoder(); decoder.keyDecodingStrategy = .convertFromSnakeCase
            let response = try decoder.decode(ConsoleResponse.self, from: data)
            if let state = response.state {
                snapshot = state; connected = true
                if connectionError { error = ""; connectionError = false }
                let stamp = state.transfer.completedAt ?? ""
                if stamp != previewStamp, let path = state.previewPath {
                    // Only the backend's own completion artifact may be shown.
                    let expected = binary.deletingLastPathComponent().appendingPathComponent("console-last-frame.png").path
                    if path == expected { preview = NSImage(contentsOfFile: path); previewStamp = stamp }
                }
            } else { connected = false; connectionError = true }
            if action != "snapshot" || response.error != nil { error = response.error ?? "" }
            if action == "inspect", let metadata = response.packageInfo, let path = app {
                if let target, metadata.app != target.app.id || metadata.version != target.version {
                    error = "请选择 \(target.app.id) v\(target.version ?? 0) 的应用包；所选文件是 \(metadata.app) v\(metadata.version)。"
                } else { pendingPackage = PackageSelection(path: path, metadata: metadata, target: target) }
            }
        } catch { connected = false; connectionError = true; self.error = "后台尚未提供控制台状态。请使用配套版本的 PhotoPainter Renderer。" }
    }
    func choosePackage(restoring target: SlotSelection? = nil) {
        let panel = NSOpenPanel()
        panel.title = "选择 PhotoPainter 应用包"
        panel.message = target.map { "选择 \($0.app.id) v\($0.version ?? 0) 的 .pkg 应用包。确认后重新安装到备用槽并激活，保留当前活动版本。" } ?? "选择 .pkg 应用包。安装只写入备用槽，激活是单独操作。"
        panel.canChooseDirectories = false; panel.allowsMultipleSelection = false
        panel.begin { response in
            guard response == .OK, let url = panel.url else { return }
            Task { @MainActor in self.command("inspect", app: url.path, target: target) }
        }
    }
    func startService() {
        let home = FileManager.default.homeDirectoryForCurrentUser
        let label = "gui/\(getuid())/com.m1ntch0c0.ai-quota-frame"
        Task {
            let result = await Task.detached { () -> Bool in
                func launch(_ args: [String]) -> Int32 {
                    let p = Process(); p.executableURL = URL(fileURLWithPath: "/bin/launchctl"); p.arguments = args
                    p.standardOutput = FileHandle.nullDevice; p.standardError = FileHandle.nullDevice
                    do { try p.run(); p.waitUntilExit(); return p.terminationStatus } catch { return -1 }
                }
                guard launch(["enable",label]) == 0 else { return false }
                if launch(["print",label]) == 0 { return launch(["kickstart",label]) == 0 }
                return launch(["bootstrap","gui/\(getuid())",home.appendingPathComponent("Library/LaunchAgents/com.m1ntch0c0.ai-quota-frame.plist").path]) == 0
            }.value
            if !result { error = "后台启动失败。请检查现有 LaunchAgent 是否已安装。" }
            await request("snapshot")
        }
    }
}
