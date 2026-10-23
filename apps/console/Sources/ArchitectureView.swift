import SwiftUI

struct ArchitectureView: View {
    @ObservedObject var model: ConsoleModel
    var onNavigate: () -> Void = {}
    @State private var section: GuideSection = .map
    @State private var starter = 0
    var body: some View {
        VStack(alignment: .leading, spacing: 25) {
            Picker("探索内容", selection: $section) { ForEach(GuideSection.allCases) { Text($0.rawValue).tag($0) } }
                .pickerStyle(.segmented).controlSize(.large).onChange(of: section) { _, _ in onNavigate() }
            switch section {
            case .map: SystemMapGuide(model: model)
            case .hardware: HardwareGuide(model: model)
            case .api: ApplicationGuide()
            case .develop: DevelopmentGuide(starter: $starter)
            case .ideas: IdeasGuide { index in starter = index; section = .develop }
            }
            Divider()
            Text("讲解基于项目源码；交互演示不会操作设备。Host b4709c0 · 应用 SDK 2.1").font(.system(size: 12)).foregroundStyle(GuideTheme.muted)
        }
    }
}
struct SystemNode: Identifiable {
    let id: Int; let title: String; let subtitle: String; let symbol: String
    let purpose: String; let input: String; let output: String; let change: String; let source: GuideSource
    var column: Int { id / 3 }; var row: Int { id % 3 }
}
let systemNodes: [SystemNode] = [
    .init(id:0,title:"云端数据源",subtitle:"额度、天气、日程等",symbol:"cloud",purpose:"当前后台从 CPA/CMP 获取额度与用量。其他数据源可在电脑端接入，再变成图片或应用命令。",input:"电脑持有的访问凭据；设备不读取云端密钥。",output:"数据快照，经电脑整理后才进入设备通道。",change:"接入新数据源：改电脑端采集和渲染。天气、日程是可扩展方向。",source:.pc("数据采集实现","internal/sources")),
    .init(id:1,title:"Go 后台",subtitle:"采集 · 渲染 · 发送",symbol:"desktopcomputer",purpose:"采集数据，用 Chrome 将页面渲染为严格六色 PNG；串行执行图片推送和应用管理。",input:"数据源、页面主题、本机控制台指令。",output:"BLE 已鉴权应用命令；也保留 HTTP 推图通道。",change:"改主题、排版、更新间隔或接入业务：优先改 ai-quota-frame。",source:.pc("电脑端入口","cmd/ai-quota-frame/main.go")),
    .init(id:2,title:"SwiftUI 控制台",subtitle:"本机交互与状态",symbol:"macwindow",purpose:"显示观测，发起读取、安装与切换。通过受文件权限保护的 Unix socket 连接本机后台。",input:"后台提供的状态摘要，不解析普通服务日志。",output:"送入后台同一队列的本机请求。窗口关闭后后台继续工作。",change:"增加配置界面或开发工具：改 apps/console 与 console 协议，不需要改显示时序。",source:.pc("电脑端项目","README.md")),
    .init(id:3,title:"通信与鉴权",subtitle:"BLE 常驻 / HTTP 可选",symbol:"antenna.radiowaves.left.and.right",purpose:"Host 常驻提供发现、鉴权和应用安装。BLE 管理不依赖相框运行，也不要求设备有 Wi-Fi 地址。",input:"BLE 的签名命令或端口 80 的 Bearer 请求。",output:"经过大小、身份和接纳检查的管理请求与应用事件。",change:"应用业务使用 ABI 2.1 受管 BLE。HMAC 提供鉴权与完整性；当前链路不加密。",source:.host("BLE 协议","docs/ble-protocol.md")),
    .init(id:4,title:"App Manager",subtitle:"唯一应用调度任务",symbol:"arrow.triangle.branch",purpose:"把 HTTP、BLE、GPIO 4、定时和联网变化串行交给当前 ELF。同一时间只运行一个应用。",input:"已接纳作业、短按切换与宿主事件。",output:"START / INPUT / TIMER / NETWORK / STOP / BLE，附事件 ID 和运行代次。",change:"应用用状态机响应事件，不自行创建后台任务。功能键只切已激活的 ABI 2 应用。",source:.host("调度与按键","main/app_manager.c")),
    .init(id:5,title:"Flash 与目录",subtitle:"5 个应用，各有 A/B",symbol:"externaldrive",purpose:"每个应用有两个 1 MiB 数据槽，保存独立 ELF 包。目录记录 active、pending、previous 和试运行状态。",input:"包头、应用身份、版本、SHA-256 与 ELF。",output:"被选中的已验证 ELF；失败时按目录和启动保护记录处理回退。",change:"兼容 ABI 的应用只更新备用槽。分区或 Host 服务变化才涉及宿主部署。",source:.host("槽位与回退","main/module_slots.c")),
    .init(id:6,title:"应用 ELF",subtitle:"内容与业务状态",symbol:"square.stack.3d.up",purpose:"相框负责收图、校验和缓存；色条直接生成逻辑帧。应用决定展示什么、何时请求显示。",input:"事件上下文和当前事件内借用的数据。",output:"恰好一次完成报告；按需要调用显示、缓存、定时或受管 BLE。",change:"新玩法通常从 ELF 开始。应用与 Host 共享地址空间，接口约束不是恶意代码沙箱。",source:.app("相框应用入口","main/entry.c")),
    .init(id:7,title:"板级显示服务",subtitle:"逻辑帧 → E6 时序",symbol:"point.3.connected.trianglepath.dotted",purpose:"ABI 2 的硬件操作统一归 Host：校验整帧、180° 转向、4 bit 打包、AXP2101 电源与 SPI 显示。",input:"从左上逐行排列的 384000 字节六色索引帧。",output:"192000 字节 E6 数据；最终 POWER_OFF 完成后返回。",change:"改 GPIO、电源或 E6 时序：改 Host 的 photopainter_board。应用不再自行旋转或操作引脚。",source:.host("逻辑帧打包","components/photopainter_board/src/board.c")),
    .init(id:8,title:"Spectra 6 屏幕",subtitle:"7.3 英寸 · 800 × 480",symbol:"photo.artframe",purpose:"六色电子纸完成物理刷新后保留画面。慢刷新适合低频信息、静态艺术和回合式交互。",input:"SPI 像素、复位/控制信号、面板供电。",output:"BUSY 状态与真实画面。断电后画面仍在，不能单凭画面判断在线。",change:"做低频、整屏设计；当前没有局部刷新 API，持续动画不是已有能力。",source:.host("E6 驱动时序","components/photopainter_board/src/epd_e6.c"))
]
private enum SystemFlow: String, CaseIterable, Identifiable {
    case all="全景", image="图片路径", install="应用安装", switchApp="按键切换"
    var id: Self {self}
    var path:[Int] {switch self {case .all:[];case .image:[0,1,3,4,6,7,8];case .install:[2,1,3,4,5];case .switchApp:[4,6,7,8]}}
    var caption:String {switch self {
    case .all:"点选模块，查看输入输出、职责与修改边界。"
    case .image:"PNG 在电脑生成，由相框 APP 接收、校验，再调用宿主显示服务。"
    case .install:"安装先暂存到备用槽；显式激活后试运行，成功就绪后确认。"
    case .switchApp:"GPIO 4 → 管理器 → 原应用 STOP → 新应用 START → 所需首屏 → 确认。"
    }}
}
private struct SystemMapGuide: View {
    @ObservedObject var model:ConsoleModel
    @State private var selected=4
    @State private var flow:SystemFlow = .all
    @State private var step=0
    private var node:SystemNode {systemNodes[selected]}
    var body:some View {
        VStack(alignment:.leading,spacing:23) {
            GuideHeading(title:"从一块屏幕，拆开整个系统",detail:"电脑负责业务数据，Host 提供运行环境，应用决定内容；硬件服务把逻辑像素变成真实画面。")
            HStack {
                Picker("高亮路径",selection:$flow) {ForEach(SystemFlow.allCases) {Text($0.rawValue).tag($0)}}
                    .pickerStyle(.segmented).frame(maxWidth:460).onChange(of:flow) {_,v in step=0;if let first=v.path.first {selected=first}}
                Spacer()
                if !flow.path.isEmpty {Button("下一环节",systemImage:"arrow.right") {step=(step+1)%flow.path.count;selected=flow.path[step]}}
            }
            diagram
            Text(flow.caption).font(.system(size:13)).foregroundStyle(GuideTheme.muted)
            HStack(alignment:.top,spacing:30) {
                VStack(alignment:.leading,spacing:15) {
                    Label(node.title,systemImage:node.symbol).font(.system(size:21,weight:.semibold)).foregroundStyle(GuideTheme.blue)
                    Text(node.purpose).font(.system(size:14)).lineSpacing(6).textSelection(.enabled)
                    GuideParagraph(title:"想改这个模块",text:node.change)
                    GuideSources(sources:[node.source])
                }.frame(maxWidth:.infinity,alignment:.leading)
                VStack(alignment:.leading,spacing:18) {GuideParagraph(title:"输入",text:node.input);GuideParagraph(title:"输出",text:node.output)}.frame(maxWidth:.infinity,alignment:.leading)
            }.padding(23).background(.white,in:RoundedRectangle(cornerRadius:12))
            if let s=model.snapshot,let d=s.device {GuideNote(text:"真机上次观测（\(clockText(s.deviceObservedAt))）：\(d.currentName) v\(d.version)，\(d.runtimeReady ? "应用已就绪" : "应用未就绪")。图中高亮是讲解演示，不代表当前传输。")}
        }
    }
    private var diagram:some View {
        GeometryReader {geo in
            let w=geo.size.width
            let bw=max(150,(w-100)/3)
            let xs:[CGFloat]=[w/6,w/2,w*5/6]
            let ys:[CGFloat]=[80,190,300]
            ZStack {
                ForEach(0..<3) {col in
                    RoundedRectangle(cornerRadius:12).fill(col==1 ? GuideTheme.blue.opacity(0.05) : .white.opacity(0.65)).frame(width:w/3-14,height:355).position(x:xs[col],y:180)
                    Text(["云端与 Mac","ESP32-S3 · Host","应用与显示硬件"][col]).font(.system(size:13,weight:.semibold)).foregroundStyle(GuideTheme.muted).position(x:xs[col],y:23)
                }
                Canvas {ctx,_ in
                    for (from,to) in [(0,1),(2,1),(1,3),(3,4),(4,5),(4,6),(6,7),(7,8)] {
                        let a=systemNodes[from],b=systemNodes[to],vertical=a.column==b.column
                        let dir:CGFloat=b.row>a.row ? 1 : -1
                        let start=CGPoint(x:xs[a.column]+(vertical ? 0 : bw/2),y:ys[a.row]+(vertical ? dir*32 : 0))
                        let end=CGPoint(x:xs[b.column]-(vertical ? 0 : bw/2),y:ys[b.row]-(vertical ? dir*32 : 0))
                        var p=Path();p.move(to:start)
                        if !vertical {let mid=(start.x+end.x)/2;p.addLine(to:CGPoint(x:mid,y:start.y));p.addLine(to:CGPoint(x:mid,y:end.y))};p.addLine(to:end)
                        let active=flow == .all || (flow.path.contains(from)&&flow.path.contains(to))
                        ctx.stroke(p,with:.color(active ? GuideTheme.blue.opacity(0.65) : GuideTheme.line),style:StrokeStyle(lineWidth:active ? 2 : 1,lineCap:.round))
                        var arrow=Path()
                        if vertical {arrow.move(to:CGPoint(x:end.x-4,y:end.y-dir*6));arrow.addLine(to:end);arrow.addLine(to:CGPoint(x:end.x+4,y:end.y-dir*6))}
                        else {arrow.move(to:CGPoint(x:end.x-6,y:end.y-4));arrow.addLine(to:end);arrow.addLine(to:CGPoint(x:end.x-6,y:end.y+4))}
                        ctx.stroke(arrow,with:.color(active ? GuideTheme.blue : GuideTheme.line),lineWidth:1.5)
                    }
                }.accessibilityHidden(true)
                ForEach(systemNodes) {item in
                    Button {selected=item.id;if let i=flow.path.firstIndex(of:item.id){step=i}} label: {
                        HStack(spacing:10) {
                            Image(systemName:item.symbol).font(.system(size:20)).frame(width:25)
                            VStack(alignment:.leading,spacing:5) {
                                Text(item.title).font(.system(size:13,weight:.semibold))
                                Text(item.subtitle).font(.system(size:11)).foregroundStyle(selected==item.id ? .white.opacity(0.9) : GuideTheme.muted)
                            }.frame(maxWidth:.infinity,alignment:.leading)
                        }.padding(.horizontal,12).frame(width:bw,height:64).foregroundStyle(selected==item.id ? .white : GuideTheme.ink)
                            .background(selected==item.id ? GuideTheme.blue : .white,in:RoundedRectangle(cornerRadius:8))
                            .overlay(RoundedRectangle(cornerRadius:8).stroke(GuideTheme.line,lineWidth:selected==item.id ? 0 : 1))
                    }.buttonStyle(.plain).position(x:xs[item.column],y:ys[item.row]).accessibilityLabel(item.title+"，"+item.subtitle).accessibilityValue(selected==item.id ? "已选择" : "")
                }
            }
        }.frame(height:360)
    }
}
