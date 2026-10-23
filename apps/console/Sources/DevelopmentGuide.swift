import SwiftUI
import AppKit

struct DevelopmentGuide:View {
    @Binding var starter:Int
    @State private var step=0
    private var example:GuideStarter {guideStarters[starter]}
    private let steps=["选入口","本地验证","编译 ELF","打包","暂存","激活与验证"]
    var body:some View {
        VStack(alignment:.leading,spacing:23) {
            GuideHeading(title:"从一个小应用开始",detail:"选已有例子作起点，先在电脑验证业务，再生成独立 ELF 与应用包。整个开发链路围绕当前 ABI，不需要把业务塞进 Host。")
            Picker("应用起点",selection:$starter) {ForEach(guideStarters) {Text($0.title).tag($0.id)}}.pickerStyle(.segmented).frame(maxWidth:570)
            GuideParagraph(title:example.app,text:example.intro)
            HStack(spacing:6) {
                ForEach(steps.indices,id:\.self) {i in
                    Button {step=i} label: {Text(steps[i]).font(.system(size:13,weight:.medium)).frame(maxWidth:.infinity).padding(.vertical,12)}
                        .buttonStyle(.plain).foregroundStyle(step==i ? .white : GuideTheme.blue).background(step==i ? GuideTheme.blue : .white,in:RoundedRectangle(cornerRadius:8))
                        .accessibilityValue(step==i ? "已选择" : "")
                }
            }
            stepContent
            Divider()
            GuideHeading(title:"把变化放到合适的一层",detail:"接口稳定后，业务迭代主要落在电脑端和独立应用；Host 保持通用。")
            GuideFacts(rows:[.init("换页面、取数据","电脑端采集 / 模板 / PNG 渲染；继续使用 photoframe 收图。"),.init("换首屏、做离线玩法","修改独立 ELF 的 START、状态机和逻辑帧生成。"),.init("新增交互命令","APP_MANIFEST_BLE + 受管特征；电脑客户端实现同一版本的业务协议。"),.init("接传感器、改总线","新增 Host 板级服务与 ABI 契约；同步 SDK、导入白名单和验证。"),.init("改显示方向","先核对逻辑坐标；ABI 2 的固定 180° 转向统一在 Host 的 board.c。")])
            DisclosureGroup("发布前逐项核对") {
                VStack(alignment:.leading,spacing:12) {
                    GuideParagraph(title:"身份与依赖",text:"包 ID、manifest ID、管理请求一致；SDK 副本与 Host 一致；使用固定 ESP-IDF 提交及未修改的 Registry elf_loader 1.3.3。")
                    GuideParagraph(title:"事件与失败",text:"START、坏输入、内存不足、定时合并、断线、STOP 和旧代次请求都有处理；不要只测成功路径。")
                    GuideParagraph(title:"版本与现场",text:"保留已确认 active，先写备用槽；区分暂存成功、加载成功、运行就绪和显示完成。实机刷新还需核对方向、颜色与内容。")
                }.padding(.top,14)
            }.font(.system(size:14,weight:.medium))
            GuideSources(sources:[.app("当前示例源码","examples/"+example.app+"/main/entry.c"),.app("示例构建说明","examples/"+example.app+"/README.md"),.host("运行层开发契约","docs-runtime-v2.md")])
        }
    }
    @ViewBuilder private var stepContent:some View {
        switch step {
        case 0:
            GuideParagraph(title:"先改哪里",text:example.change)
            GuideCode(title:"main/entry.c · 完整示例入口",code:example.code)
            GuideNote(text:"新建应用时使用独立 ID，同时更新 manifest、CMake 的 project 与打包参数。例子沿用仓内 ID；不要用另一个 ID 只改包头。")
        case 1:
            GuideParagraph(title:"先测试业务和契约",text:"从 photoframe 仓库根目录执行现有原生套件。它调用真实入口并模拟 Host 服务；新增应用需要加入自己的入口测试。测试通过不等于无线或实体屏幕已经验收。")
            GuideCode(title:"原生测试",code:"cmake -S tests -B tests/build\ncmake --build tests/build\nctest --test-dir tests/build --output-on-failure")
            GuideFacts(rows:[.init("显示应用","坏尺寸/颜色拒绝；每事件最多一次显示；STOP 不显示。"),.init("定时应用","tick 合并仍计算正确；停用后不再持有定时状态。"),.init("BLE 应用","长度/偏移/重复/断线/超时/STOP；旧 generation 与 service revision 的拒绝。")])
        case 2:
            GuideParagraph(title:"编译为宿主可加载的 ELF",text:"工具链固定 ESP-IDF 5e6f53cdb31fe5708eae3f55af9737be2822db22。以下从包含三个兄弟仓库的目录开始；先把 export.sh 路径替换为本机安装路径。")
            GuideCode(title:"交叉编译",code:". /path/to/esp-idf/export.sh\ngit -C \"$IDF_PATH\" rev-parse HEAD\ncd photoframe/examples/\(example.app)\nidf.py -B build-guide -DIDF_TARGET=esp32s3 build")
            GuideNote(text:"目标产物是 build-guide/\(example.artifact).app.elf。这个构建目录里的普通 .bin 是占位固件，不要拿来刷 Host。")
        case 3:
            GuideParagraph(title:"给 ELF 加身份、版本与校验",text:"继续在示例目录执行。APP_VERSION 仅是示例值，先读取设备状态，再改为高于该应用所有已有槽的版本。打包工具根据 manifest 校验 ABI 和导入符号。")
            GuideCode(title:"生成 .pkg",code:"APP_VERSION=\(starter==0 ? 5 : 1)\npython3 ../../../photopainter-host/tools/module.py package \\\n  --app \(example.app) --version \"$APP_VERSION\" \\\n  --input build-guide/\(example.artifact).app.elf \\\n  --output build-guide/\(example.app).pkg")
            GuideFacts(rows:[.init("包头","4096 字节，含格式、ABI、长度、版本、SHA-256 和应用 ID。"),.init("容量","整包 ≤1 MiB；ELF 与 manifest 都计入 payload。"),.init("兼容","不要手动修改 ABI 字段或 vendoring/patch Registry loader。")])
        case 4:
            GuideParagraph(title:"先暂存，保留原 active",text:"在运行总览暂停后续推图，等待当前刷新完成。到“设备与应用”选择“安装应用包…”，核对应用 ID 与版本后上传。状态中看到 pending 槽和目标版本，才证明暂存完成。")
            GuideCode(title:"已配置可信启动器时的 CLI 等价操作",code:"open -n -W \"$HOME/Applications/PhotoPainter Renderer.app\" \\\n  --args ble install --file /absolute/path/\(example.app).pkg")
            GuideNote(text:"安装不会自动运行新版本。凭据由已有启动器私下加载；本页不读取或复制令牌，也不执行这些命令。")
        default:
            GuideParagraph(title:"激活后读状态，不凭上传进度判断",text:"在目标槽旁选择激活，等待 START 和所需首屏。核对 running app、槽、version、runtime_ready 和 confirmed；ABI 1 则等待首次有效 PNG。蓝牙回复丢失时先读取状态，不盲目重复激活。")
            GuideCode(title:"激活与签名状态查询",code:"open -n -W \"$HOME/Applications/PhotoPainter Renderer.app\" \\\n  --args ble activate --app \(example.app)\nopen -n -W \"$HOME/Applications/PhotoPainter Renderer.app\" --args ble status")
            GuideFacts(rows:[.init("还原与回退","确认 previous 指向旧版本；使用控制台对应槽的回退入口。"),.init("显示证据","应用就绪不是任意一次图片已显示；图片操作须有匹配请求的完成回执。"),.init("最后验收","实机确认方向、颜色、内容；再恢复自动推图，观察重启与资源回收。")])
        }
    }
}

private struct ProjectIdea:Identifiable {
    let id:Int;let title:String;let kind:String;let level:String;let pitch:String;let modules:String;let first:String;let limits:String;let starter:Int
}
private let projectIdeas:[ProjectIdea]=[
    .init(id:0,title:"六色生成艺术",kind:"离线应用",level:"入门",pitch:"每次启动由种子生成一幅棋盘、色块或几何海报，让屏幕成为一件缓慢变化的作品。",modules:"APP：逻辑像素生成。Host：直接复用显示服务。",first:"从 color-test 把六条色带改为 20×12 网格；种子先用固定值，能重复得到同一画面。",limits:"只输出 0…5 六色索引。先做每次 START 一幅，之后才考虑低频定时更新；当前没有局部刷新。",starter:0),
    .init(id:1,title:"纸面专注钟",kind:"离线应用",level:"进阶",pitch:"屏幕显示当前专注阶段与剩余分钟；在阶段切换时更新画面，让计时不依赖电脑常驻。",modules:"APP：单调时钟、定时状态机、逻辑帧。可选 BLE 用于开始/暂停。",first:"先扩展 lifecycle，模拟“专注→休息”两状态；用经过时间计算剩余值，再加入显示。",limits:"tick 会合并，不能按事件次数计秒。重启后单调时间与 RAM 状态丢失；每秒刷屏不适合此屏。",starter:1),
    .init(id:2,title:"天气与日程晨报",kind:"电脑协作",level:"入门",pitch:"每天生成一页天气、日程和待办，用现有相框展示，无需先写新的设备应用。",modules:"电脑：数据适配、模板、定时渲染。设备：现有 photoframe v18 收图。",first:"把一份本地示例数据渲染成 800×480 六色 PNG，再接实际数据源。",limits:"数据获取、登录与凭据留在电脑。网页渐变和抗锯齿需要量化到精确六色；失败不推过期信息。",starter:0),
    .init(id:3,title:"桌面消息旗",kind:"BLE 交互",level:"进阶",pitch:"在 Mac 选“开会、专注、可打扰”，设备切换对应的整屏标识。传状态比传整张 PNG 更轻。",modules:"APP：ABI 2.1 命令特征、状态机与画面。电脑：轻量控制界面或已有签名 app-write。",first:"从 ble-echo 接收一个状态编号，验证 0…2；再将三种状态映射为三幅预定义画面。",limits:"每特征值最多 128 byte。设备文本字形需自行准备，中文长文也可改由电脑渲染成图片。",starter:2),
    .init(id:4,title:"回合制迷宫",kind:"BLE 交互",level:"进阶",pitch:"从电脑发一个方向指令，应用更新迷宫和角色位置；每个回合一幅稳定画面。",modules:"APP：棋盘状态、碰撞规则、BLE 命令。电脑：方向控制与回执展示。",first:"先做 8×6 格固定地图，定义带序号的移动命令；重复序号不执行第二次。",limits:"GPIO 4 当前归 Host 切应用，不是游戏输入。整屏刷新较慢，必须按回合等待；切出后清理命令状态。",starter:2),
    .init(id:5,title:"低频像素伙伴",kind:"离线应用",level:"进阶",pitch:"让角色按几十分钟或阶段变化呈现不同姿态，作为陪伴物，而非连续帧动画。",modules:"APP：少量六色帧、定时器、状态切换。可选 BLE 送入“完成任务”等事件。",first:"先准备三幅小图，绘制到同一背景；START 显示一幅，定时在状态变化时刷新。",limits:"一帧逻辑像素 384 KB，完整图片多帧会挤占 ELF 槽。宜存小块或程序绘图；重启恢复策略需另设计。",starter:1),
    .init(id:6,title:"环境观测仪",kind:"扩展 Host",level:"硬件进阶",pitch:"接入温湿度或空气质量传感器，让设备采集并呈现长期趋势。",modules:"Host：板级总线服务与 ABI。APP：解析采样和低频绘图。也可先用电脑/外部节点采集再走 BLE。",first:"先用模拟采样值做 APP，再核对具体传感器电气条件与现有总线占用，定义只读受管采样接口。",limits:"当前 SDK 没有通用 I²C/GPIO 导入；不能在 ELF 中直接创建驱动。新增 Host 服务需要兼容设计、测试与宿主部署。",starter:2),
    .init(id:7,title:"接口实验台",kind:"BLE 交互",level:"入门",pitch:"把回显、计时、缓存与资源清理变成看得见的实验，学习每个事件如何进入应用。",modules:"APP：ble-echo / lifecycle。电脑：读写特征、观察代次和状态。",first:"写入 hello 并读回；切出后再次尝试旧服务，确认服务与旧代次请求失效。",limits:"回显示例不要求首屏，因此激活成功不会改变电子纸。要看数字，需要再加入显示和首屏声明。",starter:2)
]
struct IdeasGuide:View {
    let openStarter:(Int)->Void
    @State private var filter="全部"
    @State private var selected=0
    @State private var copied=false
    private let filters=["全部","离线应用","电脑协作","BLE 交互","扩展 Host"]
    private var visible:[ProjectIdea] {projectIdeas.filter {filter=="全部" || $0.kind==filter}}
    private var idea:ProjectIdea {visible.first(where:{$0.id==selected}) ?? visible[0]}
    var body:some View {
        VStack(alignment:.leading,spacing:23) {
            GuideHeading(title:"把可用接口，变成好玩的东西",detail:"这些是开发构想，不是已安装应用。先做一个小而完整的体验，再增加联网、持久化或硬件能力。")
            Picker("按实现路径筛选",selection:$filter) {ForEach(filters,id:\.self) {Text($0).tag($0)}}.pickerStyle(.segmented)
            HStack(alignment:.top,spacing:28) {
                VStack(spacing:3) {ForEach(visible) {p in GuideChoice(title:p.title,subtitle:p.level+" · "+p.kind,selected:p.id==idea.id) {selected=p.id;copied=false}}}.frame(width:205)
                VStack(alignment:.leading,spacing:21) {
                    GuideHeading(title:idea.title,detail:idea.pitch)
                    GuideParagraph(title:"需要改哪一层",text:idea.modules)
                    GuideParagraph(title:"第一个可完成的小目标",text:idea.first)
                    GuideParagraph(title:"动手前考虑",text:idea.limits)
                    HStack(spacing:14) {
                        Button("查看代码起点",systemImage:"curlybraces") {openStarter(idea.starter)}.buttonStyle(.borderedProminent)
                        Button(copied ? "已复制清单" : "复制开发清单",systemImage:copied ? "checkmark" : "doc.on.doc") {
                            let text="# \(idea.title)\n\n\(idea.pitch)\n\n实现范围：\(idea.modules)\n\n第一步：\(idea.first)\n\n约束：\(idea.limits)\n"
                            NSPasteboard.general.clearContents();NSPasteboard.general.setString(text,forType:.string);copied=true
                        }
                    }
                }.frame(maxWidth:.infinity,alignment:.leading).padding(22).background(.white,in:RoundedRectangle(cornerRadius:12))
            }
            GuideNote(text:"挑方向时先问：数据从哪里来、多久需要更新、切出后释放什么、重启后如何恢复。若已有相框就能承载，先做电脑端内容；只有新的设备行为才需要独立 ELF。")
        }
    }
}
