import SwiftUI

struct HardwareGuide: View {
    @ObservedObject var model:ConsoleModel
    @State private var topic=0
    @State private var stage=0
    @State private var x=0.0
    @State private var y=0.0
    @State private var color=4
    @State private var pngKB=1024.0
    @State private var cached=true
    @State private var bank=0
    private let colors:[Color]=[.black,.white,.yellow,.red,.blue,.green]
    private let names=["黑","白","黄","红","蓝","绿"]
    private let codes=[0,1,2,3,5,6]
    private let stages=["整帧校验","供电与总线","E6 初始化","传输像素","刷新与断电"]
    private let stageTexts=[
        "Host 先确认帧长恰为 384000 字节，每个像素索引都在 0…5。整帧通过后才生成硬件帧，验证失败不会触碰显示硬件。",
        "通过 I²C 识别 AXP2101，将 ALDO4 配为 3.3 V 并确认开启；随后设置控制引脚和 SPI3。ALDO3 是音频电源轨。",
        "复位屏幕、等待 BUSY 释放，发送固定 E6 初始化表，再执行 0x04 POWER_ON 并等待。电源与刷新时序由 Host 维护。",
        "发送 0x10 后，写入 192000 字节打包帧。驱动用 4096 字节 DMA 缓冲分块传输，SPI 时钟配置为 40 MHz。",
        "再次 POWER_ON，更新设置，再发 0x12 刷新并等待；最后发 0x02 POWER_OFF 并等待 BUSY。只有最后一步完成才返回成功。"
    ]
    var body:some View {
        VStack(alignment:.leading,spacing:23) {
            GuideHeading(title:"像素如何落到电子纸上",detail:"目标板是 ESP32-S3 N16R8：16 MiB Flash、8 MiB PSRAM，连接 7.3 英寸 800 × 480 Spectra 6。下面可探索驱动、引脚和内存边界。")
            Picker("硬件主题",selection:$topic) {Text("刷新与像素").tag(0);Text("引脚与供电").tag(1);Text("存储与内存").tag(2)}.pickerStyle(.segmented).frame(maxWidth:600)
            switch topic {case 0:display;case 1:pins;default:memory}
        }
    }
    private var display:some View {
        VStack(alignment:.leading,spacing:24) {
            HStack(spacing:7) {
                ForEach(0..<stages.count,id:\.self) {i in
                    Button {stage=i} label: {Text(stages[i]).font(.system(size:13,weight:.medium)).frame(maxWidth:.infinity).padding(.vertical,12)}
                        .buttonStyle(.plain).foregroundStyle(stage==i ? .white : GuideTheme.blue)
                        .background(stage==i ? GuideTheme.blue : .white,in:RoundedRectangle(cornerRadius:8))
                        .accessibilityValue(stage==i ? "已选择" : "")
                    if i<stages.count-1 {Image(systemName:"chevron.right").font(.system(size:10)).foregroundStyle(GuideTheme.muted)}
                }
            }
            GuideParagraph(title:stages[stage],text:stageTexts[stage])
            Divider()
            GuideHeading(title:"试着移动一个像素",detail:"ABI 2 应用始终按左上原点作画。Host 统一旋转 180° 并转换 E6 色码，不要在应用里再旋转一次。")
            HStack(alignment:.top,spacing:25) {
                VStack(alignment:.leading,spacing:13) {
                    pixelFrame(title:"应用逻辑坐标",px:x,py:y)
                    Text("(\(Int(x)), \(Int(y))) · 索引 \(Int(y)*800+Int(x))").font(.system(size:13,design:.monospaced))
                }.frame(maxWidth:.infinity)
                VStack(alignment:.leading,spacing:13) {
                    pixelFrame(title:"硬件帧坐标",px:799-x,py:479-y)
                    Text("(\(799-Int(x)), \(479-Int(y))) · E6 色码 0x\(String(codes[color],radix:16))").font(.system(size:13,design:.monospaced))
                }.frame(maxWidth:.infinity)
            }
            HStack(spacing:24) {
                VStack(alignment:.leading) {Text("X · \(Int(x))");Slider(value:$x,in:0...799,step:1).accessibilityLabel("逻辑像素 X 坐标")}
                VStack(alignment:.leading) {Text("Y · \(Int(y))");Slider(value:$y,in:0...479,step:1).accessibilityLabel("逻辑像素 Y 坐标")}
                Picker("颜色",selection:$color) {ForEach(0..<6,id:\.self) {Text(names[$0]).tag($0)}}.frame(width:125)
            }.font(.system(size:13)).tint(GuideTheme.blue)
            GuideFacts(rows:[
                .init("逻辑帧","800 × 480 × 1 byte = 384000 字节；索引 0黑 / 1白 / 2黄 / 3红 / 4蓝 / 5绿"),
                .init("硬件帧","两个 4 bit 像素合为 1 byte，共 192000 字节；E6 代码为 0 / 1 / 2 / 3 / 5 / 6"),
                .init("当前像素打包","写入 wire[\(191999-(Int(y)*800+Int(x))/2)] 的 \((Int(y)*800+Int(x))%2==0 ? "低" : "高") 4 bit")
            ])
            GuideNote(text:"这是坐标与打包演示，不是屏幕实时预览。当前显示 API 是同步整屏刷新，每事件最多一次；STOP 事件不能刷新。")
            GuideSources(sources:[.host("board.c：像素转换","components/photopainter_board/src/board.c"),.host("E6：完整时序","components/photopainter_board/src/epd_e6.c")])
        }
    }
    private func pixelFrame(title:String,px:Double,py:Double)->some View {
        VStack(alignment:.leading,spacing:10) {
            Text(title).font(.system(size:14,weight:.medium))
            GeometryReader {g in
                ZStack(alignment:.topLeading) {
                    Rectangle().fill(.white)
                    Path {p in p.move(to:CGPoint(x:8,y:8));p.addLine(to:CGPoint(x:g.size.width-8,y:8));p.move(to:CGPoint(x:8,y:8));p.addLine(to:CGPoint(x:8,y:g.size.height-8))}.stroke(GuideTheme.line,lineWidth:1)
                    Circle().fill(colors[color]).overlay(Circle().stroke(GuideTheme.ink,lineWidth:1)).frame(width:11,height:11)
                        .position(x:8+CGFloat(px)/799*(g.size.width-16),y:8+CGFloat(py)/479*(g.size.height-16))
                }.overlay(Rectangle().stroke(GuideTheme.line,lineWidth:1))
            }.aspectRatio(5/3,contentMode:.fit).accessibilityLabel("\(title)，X \(Int(px))，Y \(Int(py))，\(names[color])色")
        }
    }
    private var pins:some View {
        VStack(alignment:.leading,spacing:25) {
            GuideParagraph(title:"两条总线，两个职责",text:"SPI 把命令和像素送进 E6；I²C 配置 AXP2101 的面板电源。BUSY 是完成握手信号，不能用固定延时替代所有等待。ABI 2 应用通过 SDK 请求显示，不直接占用这些总线。")
            GuideFacts(rows:[.init("SPI3 · SCLK","GPIO 10 · 时钟，配置 40 MHz"),.init("SPI3 · MOSI","GPIO 11 · 单向发送，无 MISO"),.init("DC / CS","GPIO 8 / 9 · 命令与数据选择 / 软件控制片选"),.init("RST / BUSY","GPIO 12 / 13 · 复位 / 就绪；BUSY 为低时持续等待"),.init("I²C · SDA / SCL","GPIO 47 / 48 · AXP2101，地址 0x34，100 kHz"),.init("面板供电","ALDO4 → EPD_VCC，3.3 V；ALDO3 属于 Audio_VCC"),.init("功能键","GPIO 4 · 上拉、低有效；30 ms 消抖，稳定按下 50–小于 600 ms 后释放触发"),.init("其他按键","BOOT 为 GPIO 0，电源键为 GPIO 5；保留原功能")])
            GuideParagraph(title:"时序错误会表现成什么",text:"字节上传完毕但画面没变，要继续看刷新与 POWER_OFF 是否完成。BUSY 每 10 ms 轮询，单次等待上限 120 秒；更外层的宿主显示调用预算为 650 秒。供电重启要结合 uptime 和现场条件判断，POWERON 复位码本身不能定位原因。")
            GuideParagraph(title:"给应用增加传感器",text:"当前 SDK 没有 GPIO / I²C / SPI 通用访问接口。接环境传感器需要新增 Host 板级服务、定义受管 ABI、更新导入白名单及验证；或先由电脑/外部设备采集，通过现有 BLE 给应用送数据。")
            GuideSources(sources:[.host("AXP2101 供电实现","components/photopainter_board/src/axp2101.c"),.host("按键策略","main/app_button.c"),.host("板级组件说明","components/photopainter_board/README.md")])
        }
    }
    private var memory:some View {
        VStack(alignment:.leading,spacing:24) {
            GuideParagraph(title:"Flash 放代码，PSRAM 放工作数据",text:"Host 位于 factory 固件区。应用槽是数据分区，不是 ESP-IDF 的整机 OTA 分区；加载时由 ELF loader 完成重定位。应用包的 4096 字节头也占用 1 MiB 槽，ELF 最大为 1044480 字节。")
            Picker("查看应用 bank",selection:$bank) {ForEach(0..<5) {Text("Bank \($0)").tag($0)}}.pickerStyle(.segmented)
            HStack(spacing:15) {
                ForEach(0..<2) {slot in
                    VStack(alignment:.leading,spacing:9) {
                        Text("Bank \(bank) / \(slot==0 ? "A" : "B") 槽").font(.system(size:16,weight:.semibold))
                        Text("1 MiB = 包头 + 独立应用 ELF").font(.system(size:13)).foregroundStyle(GuideTheme.muted)
                    }.padding(20).frame(maxWidth:.infinity,alignment:.leading).background(.white,in:RoundedRectangle(cornerRadius:10))
                }
            }
            Text("槽位结构示意；A/B 是同一应用的两个版本，最多 5 个应用，而不是 10 个应用。").font(.system(size:13)).foregroundStyle(GuideTheme.muted)
            GuideFacts(rows:[.init("factory","0x10000 · 5 MiB · Host 固件"),.init("elf_a / elf_b","0x510000 / 0x610000 · 各 1 MiB · Bank 0"),.init("app_slots","0x720000 · 8 MiB · 其余 4 个 bank 的 A/B"),.init("目录与配置","elf_state 保存目录/启动日志；NVS 保存宿主配置。业务应用无需 SD 卡。")])
            Divider()
            GuideHeading(title:"估算一张图片的缓冲成本",detail:"拖动输入大小，观察 PNG、逻辑帧、硬件帧和可选缓存的合计。它是内存预算示例，不代替实机堆测量。")
            HStack {Text("PNG 输入：\(Int(pngKB)) KiB").monospacedDigit();Spacer();Toggle("保留一帧缓存",isOn:$cached).toggleStyle(.checkbox)}.font(.system(size:13))
            Slider(value:$pngKB,in:0...5120,step:64).accessibilityLabel("PNG 缓冲大小 KiB")
            GuideFacts(rows:[.init("固定显示缓冲","逻辑帧 384000 B + 硬件帧 192000 B"),.init("本例合计",bytesText(Int(pngKB)*1024+576000+(cached ? 384000 : 0))),.init("缓存限制","每应用 ≤512 KiB，总计 ≤1 MiB；可失败、可被回收，重启即丢失")])
            GuideNote(text:"总量还要加上 ELF、协议栈、解码器临时分配与堆开销。8 MiB 是物理 PSRAM 容量，不是应用可独占额度；所有分配都要处理失败。")
            if let s=model.snapshot,let d=s.device {Text("上次实机观测 \(clockText(s.deviceObservedAt))：可用 PSRAM \(bytesText(d.freePsram))，内部 RAM \(bytesText(d.freeInternalRam))。").font(.system(size:12)).foregroundStyle(GuideTheme.muted)}
            GuideSources(sources:[.host("分区表","partitions.csv"),.host("缓存与内存服务","main/app_services.c")])
        }
    }
}
