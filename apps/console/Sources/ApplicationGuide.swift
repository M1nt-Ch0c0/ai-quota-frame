import SwiftUI

struct ApplicationGuide:View {
    @State private var topic=0
    @State private var eventIndex=0
    @State private var apiID="display"
    @State private var query=""
    @State private var needsDisplay=true
    @State private var completedDisplay=true
    private var event:GuideEvent {guideEvents[eventIndex]}
    private var matches:[GuideAPI] {guideAPIs.filter {query.isEmpty || ($0.title+" "+$0.group+" "+$0.signature).localizedCaseInsensitiveContains(query)}}
    private var api:GuideAPI? {matches.first(where:{$0.id==apiID}) ?? matches.first}
    var body:some View {
        VStack(alignment:.leading,spacing:24) {
            GuideHeading(title:"应用是一段被事件驱动的 ELF",detail:"Host 每次调用入口时交付上下文；应用处理一个事件、报告一次结果，然后返回。用小状态机组合功能，不写常驻的 while(1)。")
            Picker("应用接口主题",selection:$topic) {Text("生命周期").tag(0);Text("SDK 查询").tag(1);Text("ABI 与规范").tag(2)}.pickerStyle(.segmented).frame(maxWidth:570)
            switch topic {case 0:lifecycle;case 1:reference;default:contract}
        }
    }
    private var lifecycle:some View {
        VStack(alignment:.leading,spacing:24) {
            HStack(spacing:6) {
                ForEach(guideEvents.indices,id:\.self) {i in
                    Button {eventIndex=i} label: {Text(guideEvents[i].name).font(.system(size:12,weight:.semibold,design:.monospaced)).frame(maxWidth:.infinity).padding(.vertical,13)}
                        .buttonStyle(.plain).foregroundStyle(eventIndex==i ? .white : GuideTheme.blue)
                        .background(eventIndex==i ? GuideTheme.blue : .white,in:RoundedRectangle(cornerRadius:8)).accessibilityValue(eventIndex==i ? "已选择" : "")
                }
            }
            GuideParagraph(title:"\(event.name) · \(event.title)",text:event.work)
            GuideFacts(rows:[.init("收到什么",event.input),.init("如何结束",event.completion)])
            if eventIndex==0 {
                VStack(alignment:.leading,spacing:15) {
                    Text("试一试：这个 START 能确认吗？").font(.system(size:16,weight:.semibold))
                    Toggle("manifest 声明需要首屏",isOn:$needsDisplay).toggleStyle(.checkbox)
                    Toggle("本次显示已完成 POWER_OFF",isOn:$completedDisplay).toggleStyle(.checkbox).disabled(!needsDisplay)
                    Label(needsDisplay && !completedDisplay ? "不能确认：APP_READY 还缺少首屏完成凭据" : "可以确认：已满足 START 的就绪条件",systemImage:needsDisplay && !completedDisplay ? "hourglass" : "checkmark.circle")
                        .foregroundStyle(needsDisplay && !completedDisplay ? Color.orange : GuideTheme.blue)
                    Text("演示假设事件已正确调用一次 app_complete，并返回 APP_OK + APP_READY。").font(.system(size:12)).foregroundStyle(GuideTheme.muted)
                }.font(.system(size:13)).padding(20).background(.white,in:RoundedRectangle(cornerRadius:12))
            } else {
                GuideCode(title:"事件分支片段",code:"if (c->event == APP_EVENT_\(event.name)) {\n    // \(event.title)；处理完成后统一报告结果。\n}\napp_complete(c, status, 0);\nreturn status;")
            }
            GuideParagraph(title:"切换时的所有权",text:"原应用 STOP → 清理 timer / BLE / 旧代次事件 → 卸载 ELF → 加载目标 → START → 就绪确认。Host 可以保留按应用 ID 隔离的可选 RAM 缓存；应用自己的全局变量、接收缓冲与服务生命周期不能跨卸载假定有效。")
            GuideSources(sources:[.host("运行层契约","docs-runtime-v2.md"),.host("Host 应用服务","main/app_services.c"),.app("真实入口示例","main/entry.c")])
        }
    }
    private var reference:some View {
        VStack(alignment:.leading,spacing:18) {
            TextField("搜索接口：display、缓存、timer、BLE…",text:$query).textFieldStyle(.roundedBorder).font(.system(size:14)).accessibilityLabel("搜索 SDK 接口")
            if let api {
                HStack(alignment:.top,spacing:24) {
                    VStack(spacing:3) {
                        ForEach(matches) {item in GuideChoice(title:item.title,subtitle:item.group,selected:api.id==item.id) {apiID=item.id}}
                    }.frame(width:190)
                    VStack(alignment:.leading,spacing:20) {
                        GuideHeading(title:api.title,detail:api.group + " · ABI " + (api.group=="BLE 2.1" ? "2.1" : "2.0 / 2.1"))
                        GuideCode(title:"函数签名",code:api.signature)
                        GuideParagraph(title:"调用契约",text:api.contract)
                        GuideCode(title:"事件内调用片段",code:api.example)
                        GuideSources(sources:[.host("SDK 原件","sdk/photopainter_app.h"),.host("导入白名单","sdk/abi2_imports.inc")])
                    }.frame(maxWidth:.infinity,alignment:.leading)
                }
            } else {
                GuideNote(text:"没有匹配接口。当前 SDK 不包含通用 GPIO、I²C、文件系统或 HTTP 客户端；可清空搜索查看全部接口。")
                Button("查看全部接口") {query=""}
            }
        }
    }
    private var contract:some View {
        VStack(alignment:.leading,spacing:24) {
            GuideFacts(rows:[
                .init("ABI 1","兼容旧 PNG 桥接，ELF 自带显示驱动；没有 START 首屏。可由管理工具选择，功能键跳过。"),
                .init("ABI 2.0","协作式事件、Host 显示/缓存/定时/时间；使用 APP_MANIFEST。"),
                .init("ABI 2.1","保持 2.0 布局，增加受管 BLE 导入和事件；使用 APP_MANIFEST_BLE。"),
                .init("状态解释","payload_abi=2 只表示主版本；次版本由 manifest 声明。顶层 abi=1 是旧管理封套，不是 Host 版本。")
            ])
            GuideCode(title:"声明身份与能力",code:"// 普通显示应用：ABI 2.0\nAPP_MANIFEST(\"hello-panel\", \"Hello panel\", 0, APP_REQUIRES_DISPLAY);\n\n// 使用 BLE 导入：ABI 2.1\nAPP_MANIFEST_BLE(\"my-ble-app\", \"My BLE app\", 0, APP_REQUIRES_DISPLAY);")
            GuideParagraph(title:"包与 manifest 必须一致",text:"唯一只读 .app_manifest，固定 128 字节；应用 ID 为 1–31 个小写字母、数字、下划线或连字符，显示名称最多 63 个可打印 ASCII 字符。包头、manifest 和管理请求的 ID 必须一致。打包器根据 manifest 识别 ABI，不要手改包头绕过校验。")
            GuideParagraph(title:"写应用时守住这几条",text:"每事件恰好一次完成报告；先验证数据再显示；处理分配与缓存失败；借用指针不跨事件保存；STOP 释放资源；不导入任务、GPIO、SPI、I²C 或系统回调。需要新硬件能力时，先定义受管 Host 服务与 ABI。")
            GuideParagraph(title:"错误与时间预算",text:"参数、输入、尺寸、颜色、内存、显示、超时、运行错误和不支持分别为 APP_ERR_* 的 -1…-9。普通事件看门狗 5 秒，显示/旧 ABI 1 调用预算 650 秒。同步显示可等待硬件，其余计算要有界；不要用忙等等待网络。")
            GuideParagraph(title:"更新与确认是不同阶段",text:"stage 写备用槽，activate 开始试运行。ABI 2 在 START 就绪且完成所声明首屏后确认；ABI 1 等首次有效 PNG 显示。previous 是可回退版本；没有 pending/previous 标记的备用包需要重新暂存后才能激活。")
            GuideNote(text:"ELF 是同一地址空间里的可信原生代码。白名单限制可导入的接口，但不提供进程隔离或恶意代码沙箱。")
            GuideSources(sources:[.host("manifest 校验器","main/app_manifest.c"),.host("安装与版本槽","docs-module-slots.md")])
        }
    }
}
