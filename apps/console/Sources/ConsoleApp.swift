import SwiftUI
import AppKit

private let ink = Color(red: 0.12, green: 0.20, blue: 0.30)
private let accent = Color(red: 0.18, green: 0.39, blue: 0.67)
private let canvas = Color(red: 0.95, green: 0.97, blue: 0.99)
private let good = Color(red: 0.12, green: 0.43, blue: 0.36)

enum Page: String, CaseIterable, Identifiable {
    case overview = "运行总览", architecture = "工作原理", device = "设备与应用", diagnostics = "诊断记录"
    var id: String { rawValue }
    var symbol: String {
        switch self { case .overview: "rectangle.grid.1x2"; case .architecture: "point.3.connected.trianglepath.dotted"; case .device: "cpu"; case .diagnostics: "list.bullet.rectangle" }
    }
}

@main
struct PhotoPainterConsoleApp: App {
    @StateObject private var model = ConsoleModel()
    var body: some Scene {
        WindowGroup("PhotoPainter 控制台") {
            ConsoleView(model: model)
                .frame(minWidth: 1000, minHeight: 700)
                .preferredColorScheme(.light)
                .task { await model.runPolling() }
        }
        .defaultSize(width: 1200, height: 820)
        .commands {
            CommandGroup(replacing: .newItem) {}
            CommandMenu("设备") {
                Button("读取设备状态") { model.command("status") }.keyboardShortcut("r", modifiers: [.command]).disabled(!model.connected || model.busy)
                Button("立即采集更新") { model.command("refresh") }.disabled(!model.connected || model.busy)
            }
        }
    }
}

struct ConsoleView: View {
    @ObservedObject var model: ConsoleModel
    @State private var page: Page? = .overview
    @State private var switchApp: DeviceApplication?
    @State private var selectedSlot: SlotSelection?
    var body: some View {
        NavigationSplitView {
            VStack(alignment: .leading, spacing: 0) {
                HStack(spacing: 11) {
                    Image(systemName: "photo.artframe").font(.system(size: 29, weight: .medium)).foregroundStyle(accent)
                    VStack(alignment: .leading, spacing: 3) {
                        Text("PhotoPainter").font(.system(size: 17, weight: .semibold))
                        Text("让设备状态看得见").font(.system(size: 11)).foregroundStyle(.secondary)
                    }
                }.padding(.horizontal, 20).padding(.vertical, 25)
                List(Page.allCases, selection: $page) { item in
                    Label(item.rawValue, systemImage: item.symbol).tag(item).padding(.vertical, 7)
                }.listStyle(.sidebar)
                VStack(alignment: .leading, spacing: 8) {
                    statusLabel(model.connected ? "后台已连接" : "后台未连接", color: model.connected ? good : .secondary)
                    Text(model.connected ? "状态来自本机服务\n设备信息需要通过蓝牙读取" : "启动后台后即可查看真机\n工作原理可以离线浏览")
                        .font(.system(size: 11)).foregroundStyle(.secondary).lineSpacing(4)
                }.padding(20)
            }
            .navigationSplitViewColumnWidth(min: 205, ideal: 220, max: 250)
        } detail: {
            ScrollViewReader { scrollProxy in
            ScrollView {
                VStack(alignment: .leading, spacing: 26) {
                    header.id("page-heading")
                    if !model.error.isEmpty { notice(model.error, symbol: "exclamationmark.circle", color: .orange) }
                    if let error = model.snapshot?.operationError, !error.isEmpty { notice(error, symbol: "exclamationmark.circle", color: .orange) }
                    switch page ?? .overview {
                    case .overview: overview
                    case .architecture: ArchitectureView(model: model, onNavigate: { scrollProxy.scrollTo("page-heading", anchor: .top) })
                    case .device: device
                    case .diagnostics: diagnostics
                    }
                }.padding(32).frame(maxWidth: 1320, alignment: .leading).frame(maxWidth: .infinity)
            }.background(canvas)
            }
        }
        .tint(accent)
        .confirmationDialog("切换到\(switchApp?.displayName ?? "应用")？", isPresented: Binding(get: {switchApp != nil}, set: {if !$0 {switchApp = nil}}), titleVisibility: .visible) {
            if let app = switchApp { Button("切换到\(app.displayName)") { model.command("use-slot", target: SlotSelection(app: app, slot: app.active)); switchApp = nil } }
            Button("取消", role: .cancel) { switchApp = nil }
        } message: {
            Text(switchApp?.abi == 1 ? "此应用的当前版本使用 ABI 1。切入后需收到图片才能刷新屏幕并确认，且不支持当前的 BLE 收图方式。" : "设备会显示该应用的画面。正在进行的图片传输会先完成，随后重新核对目标版本。")
        }
        .alert(model.pendingPackage?.target == nil ? "安装应用包？" : "重新安装并激活？", isPresented: Binding(get: { model.pendingPackage != nil }, set: { if !$0 { model.pendingPackage = nil } })) {
            if let selection = model.pendingPackage {
                Button(selection.target == nil ? "安装到备用槽" : "重新安装并激活") { model.command(selection.target == nil ? "install" : "use-slot", app: selection.path, digest: selection.metadata.digest, target: selection.target); model.pendingPackage = nil }
            }
            Button("取消", role: .cancel) { model.pendingPackage = nil }
        } message: {
            if let selection = model.pendingPackage {
                Text(selection.target.map { "将 \($0.label) 的应用包重新写入该备用槽，核对成功后激活。当前活动版本会保留用于回退。" } ?? "\(selection.metadata.app) v\(selection.metadata.version)，\(bytesText(selection.metadata.bytes))。安装到备用槽后，可在该槽旁点击激活。")
            }
        }
        .alert("使用所选版本？", isPresented: Binding(get: { selectedSlot != nil }, set: { if !$0 { selectedSlot = nil } })) {
            if let choice = selectedSlot { Button("使用此版本") { model.command("use-slot", target: choice); selectedSlot = nil } }
            Button("取消", role: .cancel) { selectedSlot = nil }
        } message: {
            if let choice = selectedSlot { Text("设备将切换到 \(choice.label)。操作前会重读设备状态；ABI 2 应用会显示首屏，旧 ABI 1 应用需收到图片后完成确认。") }
        }
    }
    private var header: some View {
        HStack(alignment: .top) {
            VStack(alignment: .leading, spacing: 8) {
                Text(page?.rawValue ?? "运行总览").font(.system(size: 28, weight: .bold)).foregroundStyle(ink)
                Text(subtitle).font(.system(size: 13)).foregroundStyle(.secondary)
            }
            Spacer()
            if model.connected {
                Button { model.command("status") } label: { Label(model.busy ? "操作进行中" : "读取设备状态", systemImage: model.busy ? "hourglass" : "arrow.clockwise") }
                    .disabled(model.busy).controlSize(.large)
            } else {
                Button("启动后台") { model.startService() }.buttonStyle(.borderedProminent).controlSize(.large)
            }
        }
    }
    private var subtitle: String {
        switch page ?? .overview {
        case .overview: "先看是否在工作，再看图片走到了哪里。"
        case .architecture: "拆解软硬件架构，查阅应用接口，从示例走向自己的玩法。"
        case .device: "这里显示鉴权读取的设备状态，不根据屏幕残留画面猜测。"
        case .diagnostics: "按时间看发生了什么；技术细节用于解释现象。"
        }
    }
    private var overview: some View {
        VStack(alignment: .leading, spacing: 24) {
            if let d = model.snapshot?.device, d.currentName == "photoframe", d.payloadAbi < 2 || d.bleCharacteristics == 0 {
                notice("上次读取的相框 v\(d.version) 没有 BLE 收图服务。后台运行不代表图片能送达；请到“设备与应用”安装并激活兼容的新相框应用。", symbol: "exclamationmark.triangle", color: .orange)
            }
            HStack(alignment: .top, spacing: 20) {
                VStack(alignment: .leading, spacing: 12) {
                    statusLabel(!model.connected ? "尚未取得后台状态" : model.snapshot?.automatic == true ? "自动更新已开启" : "后续推图已暂停", color: model.connected && model.snapshot?.automatic == true ? good : .secondary)
                    Text(headline).font(.system(size: 22, weight: .semibold)).foregroundStyle(ink)
                    Text(summary).font(.system(size: 13)).foregroundStyle(.secondary).lineSpacing(5).fixedSize(horizontal: false, vertical: true)
                    if let state = model.snapshot {
                        HStack(spacing: 12) {
                            Button(state.automatic ? "暂停后续推图" : "恢复自动推图") { model.command(state.automatic ? "pause" : "resume") }
                                .disabled(!model.connected || model.submitting)
                            Button("立即采集更新") { model.command("refresh") }
                                .buttonStyle(.borderedProminent).disabled(!model.connected || model.busy || state.transfer.active)
                        }.padding(.top, 6)
                    }
                }.frame(maxWidth: .infinity, alignment: .leading)
                VStack(alignment: .trailing, spacing: 9) {
                    Text("下次采集").font(.system(size: 12)).foregroundStyle(.secondary)
                    Text(clockText(model.snapshot?.nextFetch)).font(.system(size: 19, weight: .medium)).monospacedDigit()
                    Text(model.snapshot.map { "每 \($0.intervalSeconds / 60) 分钟采集一次" } ?? "等待后台提供计划").font(.system(size: 11)).foregroundStyle(.secondary)
                }
            }.padding(24).background(.white, in: RoundedRectangle(cornerRadius: 14))
            HStack(alignment: .top, spacing: 28) {
                VStack(alignment: .leading, spacing: 19) {
                    Text("一张图片的进度").font(.system(size: 17, weight: .semibold))
                    flowRow("云端数据", detail: sourceDetail, icon: "cloud", active: model.snapshot?.sourceState == "ready")
                    flowRow("电脑生成图片", detail: "把额度页面变成 800 × 480 六色 PNG", icon: "desktopcomputer", active: ["rendering", "uploading", "displaying", "complete"].contains(model.snapshot?.transfer.stage ?? ""))
                    flowRow("蓝牙送到相框应用", detail: transferDetail, icon: "antenna.radiowaves.left.and.right", active: ["uploading", "displaying", "complete"].contains(model.snapshot?.transfer.stage ?? ""))
                    if let t = model.snapshot?.transfer, t.total > 0 {
                        ProgressView(value: Double(t.done), total: Double(t.total)).tint(accent).accessibilityLabel("图片上传进度")
                        Text("\(t.done) / \(t.total) 字节已上传").font(.system(size: 11)).foregroundStyle(.secondary).monospacedDigit()
                    }
                    flowRow("屏幕完成刷新", detail: model.snapshot?.transfer.completedAt == nil ? "只有设备返回完成回执，才会记录成功" : "最近确认：\(clockText(model.snapshot?.transfer.completedAt))", icon: "checkmark.seal", active: model.snapshot?.transfer.stage == "complete")
                    Button("看懂这条链路") { page = .architecture }.buttonStyle(.link)
                }.frame(maxWidth: .infinity, alignment: .leading)
                VStack(alignment: .leading, spacing: 13) {
                    Text("最近确认完成的画面").font(.system(size: 17, weight: .semibold))
                    ZStack {
                        RoundedRectangle(cornerRadius: 9).fill(Color.white)
                        if let image = model.preview {
                            Image(nsImage: image).resizable().interpolation(.none).aspectRatio(5/3, contentMode: .fit).padding(12)
                        } else {
                            VStack(spacing: 14) {
                                Image(systemName: "photo.artframe").font(.system(size: 42, weight: .ultraLight)).foregroundStyle(.secondary)
                                Text("等待一张确认完成的图片").font(.system(size: 13))
                                Text("这里不会用测试图片冒充真机画面").font(.system(size: 11)).foregroundStyle(.secondary)
                            }.padding(20)
                        }
                    }.aspectRatio(5/3, contentMode: .fit)
                    Text("这是收到完成回执时保存的图片，不是屏幕摄像头。屏幕方向、颜色仍以目测为准。")
                        .font(.system(size: 11)).foregroundStyle(.secondary).lineSpacing(4)
                    Divider()
                    HStack { Label(model.snapshot?.theme ?? "主题未读取", systemImage: "paintpalette"); Spacer(); Text("800 × 480") }.font(.system(size: 12)).foregroundStyle(.secondary)
                }.frame(maxWidth: .infinity, alignment: .leading)
            }
            operationStatus
        }
    }
    private var headline: String {
        guard model.connected, let state = model.snapshot else { return "先连接后台，才能知道更新情况" }
        if !state.automatic { return state.transfer.active ? "当前刷新会完成，后续推图已暂停" : "采集继续，屏幕暂不更新" }
        if state.sourceState == "failed" { return "数据采集遇到问题，保留原画面" }
        if state.sourceState == "stale" { return "额度信息已过期，请检查数据源" }
        if let d = state.device, model.deviceFresh, d.currentName == "photoframe", d.payloadAbi < 2 || d.bleCharacteristics == 0 { return "设备上的旧相框应用无法接收 BLE 图片" }
        return state.transfer.title
    }
    private var summary: String {
        guard let state = model.snapshot, model.connected else { return "工作原理可以离线查看。启动配套后台后，这里会出现实际采集、传输和显示记录。" }
        if state.transfer.uncertain { return "上传完成不等于显示成功。系统会查询原请求；没有可信回执时，不会盲目重复刷屏。" }
        if let message = state.transfer.message, !message.isEmpty { return message }
        return "后台按计划采集并推图；切到其他设备应用时，相框收图服务会停止。关闭此窗口不会关闭后台。"
    }
    private var sourceDetail: String {
        guard let s = model.snapshot else { return "等待后台提供采集结果" }
        if s.sourceState == "stale" { return "采集已返回，但额度信息已过期" }
        return s.sourceState == "ready" ? "\(s.accountCount) 个账号，\(s.warningCount) 条采集提示；\(clockText(s.lastFetch))" : s.sourceState == "failed" ? "采集未成功，未推送过期数据" : "等待首轮采集"
    }
    private var transferDetail: String { model.snapshot?.transfer.title ?? "未取得实时进度" }
    private var device: some View {
        VStack(alignment: .leading, spacing: 25) {
            if let state = model.snapshot, let d = state.device {
                HStack {
                    statusLabel(model.deviceFresh ? "最近已读取真机" : "上次观测，可能已变化", color: model.deviceFresh ? good : .orange)
                    Spacer(); Text("读取于 \(clockText(state.deviceObservedAt))").font(.system(size: 12)).foregroundStyle(.secondary)
                }
                if let error = state.deviceError, !error.isEmpty { notice(error, symbol: "clock.badge.exclamationmark", color: .orange) }
                HStack(alignment: .top, spacing: 30) {
                    VStack(alignment: .leading, spacing: 14) {
                        Text("当前运行").font(.system(size: 14)).foregroundStyle(.secondary)
                        Text("\(d.currentName)  v\(d.version)").font(.system(size: 26, weight: .semibold)).foregroundStyle(ink)
                        Label(d.runtimeReady ? "应用已就绪" : d.payloadAbi == 1 && d.ready == true ? "已载入，等待图片确认" : "应用未就绪", systemImage: d.runtimeReady ? "checkmark.circle" : "hourglass").foregroundStyle(d.runtimeReady ? good : .orange)
                        Text("设备 \(state.deviceId)").font(.system(size: 12)).textSelection(.enabled)
                        Text("宿主管理通信和硬件；下面的应用可以独立更新与切换。").font(.system(size: 12)).foregroundStyle(.secondary).lineSpacing(4)
                    }.frame(maxWidth: .infinity, alignment: .leading)
                    Grid(alignment: .leading, horizontalSpacing: 24, verticalSpacing: 12) {
                        detailRow("本次运行时间", "\(d.uptimeMs / 1000 / 60) 分 \(d.uptimeMs / 1000 % 60) 秒")
                        detailRow("应用运行代次", String(d.generation))
                        detailRow("可用内部内存", bytesText(d.freeInternalRam))
                        detailRow("可用外部内存", bytesText(d.freePsram))
                        detailRow("最近复位码", d.resetReason == 1 ? "1 · POWERON" : String(d.resetReason))
                    }.font(.system(size: 12))
                }.padding(24).background(.white, in: RoundedRectangle(cornerRadius: 14))
                HStack {
                    Text("已安装的应用").font(.system(size: 18, weight: .semibold))
                    Spacer()
                    Button("安装应用包…") { model.choosePackage() }.disabled(!model.connected || model.busy || state.transfer.uncertain)
                }
                if !model.deviceFresh {
                    Text("操作前会自动重新读取设备状态，无需先手动刷新。").font(.system(size: 12)).foregroundStyle(.secondary)
                }
                if state.transfer.uncertain {
                    notice("图片完成结果尚未确定，版本操作暂不可用。请先读取设备状态并核对诊断记录。", symbol: "hourglass", color: .orange)
                }
                if let trial = d.trialApp, trial >= 0 {
                    HStack {
                        Text("当前应用仍在试运行。旧 ABI 1 需收到图片后确认，也可结束试运行返回原应用。").font(.system(size: 12))
                        Spacer()
                        Button("结束试运行") { model.command("cancel-trial") }.disabled(model.busy || state.transfer.uncertain)
                    }
                }
                VStack(spacing: 0) {
                    ForEach(d.apps) { app in
                        VStack(alignment: .leading, spacing: 14) {
                            HStack(spacing: 14) {
                                Image(systemName: app.id == "photoframe" ? "photo.artframe" : "rectangle.split.3x1")
                                    .font(.system(size: 24)).foregroundStyle(app.bank == d.loadedApp ? accent : .secondary).frame(width: 34)
                                VStack(alignment: .leading, spacing: 5) {
                                    HStack {
                                        Text(app.displayName).font(.system(size: 16, weight: .semibold))
                                        if app.bank == d.loadedApp { Text("运行中").font(.system(size: 12)).foregroundStyle(good) }
                                    }
                                    Text("\(app.id) · 已激活版本 ABI \(app.abi)").font(.system(size: 12)).foregroundStyle(.secondary)
                                }
                                Spacer()
                                Button(app.bank == d.loadedApp ? "正在运行" : "切换到此应用") { switchApp = app }
                                    .disabled(!model.connected || model.busy || app.bank == d.loadedApp || app.version == nil || state.transfer.uncertain || (d.trialApp ?? -1) >= 0)
                            }
                            ForEach(0..<2, id: \.self) { index in
                                slotRow(app, index: index, device: d, uncertain: state.transfer.uncertain)
                            }
                            if app.abi == 1 {
                                Text("已激活的旧版可手动切入；按键只切换 ABI 2 应用。").font(.system(size: 12)).foregroundStyle(.secondary)
                            }
                        }.padding(.vertical, 20)
                        Divider()
                    }
                }
                Text("A/B 保存同一应用的两个版本。“已激活”是该应用下次切入时使用的版本。").font(.system(size: 12)).foregroundStyle(.secondary)
                operationStatus
            } else {
                emptyState("还没有读取设备", detail: "点击“读取设备状态”。控制台会排队使用后台的蓝牙连接，不会与自动推图抢连接。", symbol: "antenna.radiowaves.left.and.right")
            }
        }
    }
    private func slotRow(_ app: DeviceApplication, index: Int, device: DeviceState, uncertain: Bool) -> some View {
        let choice = SlotSelection(app: app, slot: index)
        let disabled = !model.connected || model.busy || uncertain || (device.trialApp ?? -1) >= 0
        return VStack(alignment: .leading, spacing: 7) {
            HStack(spacing: 16) {
                Text(index == 0 ? "A 槽" : "B 槽").font(.system(size: 13, weight: .medium)).frame(width: 40, alignment: .leading)
                Text(choice.version.map { "v\($0)" } ?? "空").font(.system(size: 13)).monospacedDigit().frame(width: 65, alignment: .leading)
                Text(slotRole(app, index: index)).font(.system(size: 12)).foregroundStyle(.secondary)
                Spacer()
                if choice.version != nil && index != app.active {
                    Button(choice.needsPackage ? "重新安装并激活…" : index == app.pending ? "激活此版本" : "切换到此版本") {
                        if choice.needsPackage { model.choosePackage(restoring: choice) } else { selectedSlot = choice }
                    }.disabled(disabled).accessibilityLabel("\(choice.label)，\(choice.needsPackage ? "重新安装并激活" : "使用此版本")")
                }
            }
            if choice.version != nil && choice.needsPackage {
                Text("此版本需要重新安装后才能使用。请选择同一版本的 .pkg 应用包。").font(.system(size: 12)).foregroundStyle(.secondary)
            }
        }.padding(.leading, 48)
    }
    private var operationStatus: some View {
        VStack(alignment: .leading, spacing: 10) {
            if let operation = model.snapshot?.operation, !operation.isEmpty {
                notice(operationText(operation), symbol: "hourglass", color: accent)
                if let total = model.snapshot?.operationTotal, total > 0 {
                    ProgressView(value: Double(model.snapshot?.operationDone ?? 0), total: Double(total)).tint(accent)
                    Text("应用包：\(model.snapshot?.operationDone ?? 0) / \(total) 字节").font(.system(size: 11)).foregroundStyle(.secondary).monospacedDigit()
                }
            }
        }
    }
    private var diagnostics: some View {
        VStack(alignment: .leading, spacing: 22) {
            notice("重启码只能说明复位类型。POWERON 本身不能证明是 USB、供电线或程序造成的。", symbol: "info.circle", color: accent)
            if let events = model.snapshot?.events, !events.isEmpty {
                ForEach(events.reversed()) { event in
                    HStack(alignment: .top, spacing: 18) {
                        Text(clockText(event.at)).font(.system(size: 12)).monospacedDigit().foregroundStyle(.secondary).frame(width: 80, alignment: .leading)
                        Image(systemName: eventSymbol(event.kind)).foregroundStyle(event.kind == "reset" ? Color.orange : accent).frame(width: 20)
                        Text(event.message).font(.system(size: 13)).lineSpacing(4).frame(maxWidth: .infinity, alignment: .leading)
                    }.padding(.vertical, 9)
                    Divider()
                }
            } else { emptyState("尚无诊断记录", detail: "连接后台后，采集、传输、完成回执和设备读取会在这里出现。只显示状态摘要，不显示密钥和源端日志。", symbol: "list.bullet.rectangle") }
            DisclosureGroup("几个容易混淆的状态") {
                VStack(alignment: .leading, spacing: 13) {
                    Text("上传 100%：图片字节已送达，还不代表墨水屏刷新完成。")
                    Text("屏幕已确认刷新：收到鉴权完成回执，包含刷新和面板断电完成。")
                    Text("应用已就绪：当前应用能够处理事件，不代表最近一次图片已经显示。")
                    Text("屏幕仍有画面：墨水屏断电后也会保留画面，不能据此判断设备在线。")
                }.font(.system(size: 13)).lineSpacing(5).padding(.top, 12)
            }
        }
    }
}

private func slotRole(_ app: DeviceApplication, index: Int) -> String {
    guard app.version(at: index) != nil else { return "未安装" }
    return app.active == index ? "已激活" : app.pending == index ? "待激活" : app.previous == index ? "可回退版本" : "备用版本"
}
private func operationText(_ operation: String) -> String {
    if operation.hasPrefix("queued_") { return "操作已排队；正在进行的图片传输会先完成。" }
    switch operation { case "status": return "正在通过蓝牙读取设备状态…"; case "switch": return "正在切换应用并核对结果，请等待屏幕完成刷新。"; case "install": return "正在把应用包安装到备用槽，当前版本保持运行。"; case "activate": return "正在激活所选版本，请等待设备确认与屏幕刷新。"; case "use-slot": return "正在核对设备上的目标版本…"; case "restaging": return "正在重新安装所选版本，完成后激活。"; case "rollback": return "正在切换 A/B 版本并核对结果…"; case "cancel-trial": return "正在结束未确认的试运行…"; case "refresh": return "正在重新采集额度数据…"; default: return "正在执行操作…" }
}
private func eventSymbol(_ kind: String) -> String {
    switch kind { case "source": "cloud"; case "transfer": "arrow.up.doc"; case "device": "cpu"; case "reset": "exclamationmark.triangle"; default: "gearshape" }
}
private func statusLabel(_ title: String, color: Color) -> some View {
    Label { Text(title).font(.system(size: 12, weight: .medium)) } icon: { Circle().fill(color).frame(width: 7, height: 7) }.foregroundStyle(color)
}
private func notice(_ text: String, symbol: String, color: Color) -> some View {
    HStack(alignment: .top, spacing: 10) { Image(systemName: symbol).foregroundStyle(color); Text(text).font(.system(size: 12)).lineSpacing(4); Spacer(minLength: 0) }
        .padding(15).background(color.opacity(0.07), in: RoundedRectangle(cornerRadius: 12))
}
private func flowRow(_ title: String, detail: String, icon: String, active: Bool) -> some View {
    HStack(alignment: .top, spacing: 13) {
        Image(systemName: icon).font(.system(size: 19)).foregroundStyle(active ? accent : .secondary).frame(width: 28, height: 26)
        VStack(alignment: .leading, spacing: 5) { Text(title).font(.system(size: 13, weight: .semibold)); Text(detail).font(.system(size: 12)).foregroundStyle(.secondary).lineSpacing(3) }
    }
}
private func emptyState(_ title: String, detail: String, symbol: String) -> some View {
    VStack(spacing: 15) {
        Image(systemName: symbol).font(.system(size: 42, weight: .light)).foregroundStyle(accent)
        Text(title).font(.system(size: 20, weight: .semibold))
        Text(detail).font(.system(size: 13)).foregroundStyle(.secondary).multilineTextAlignment(.center).lineSpacing(5).frame(maxWidth: 430)
    }.frame(maxWidth: .infinity).padding(.vertical, 70)
}
private func detailRow(_ name: String, _ value: String) -> some View {
    GridRow { Text(name).foregroundStyle(.secondary); Text(value).monospacedDigit() }
}
