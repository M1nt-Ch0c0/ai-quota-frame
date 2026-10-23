import Foundation

struct GuideAPI: Identifiable {
    let id:String; let title:String; let group:String; let signature:String; let contract:String; let example:String
}
let guideAPIs:[GuideAPI]=[
    .init(id:"context",title:"取得事件上下文",group:"生命周期",signature:"const app_context_v2_t *app_host_context_v2(void);",contract:"每次入口重新取得。event、event_id、generation 标识本次调用；data 只借用到入口返回，不要跨事件保存它的指针。ELF32 上 context 为 44 字节。",example:"const app_context_v2_t *c = app_host_context_v2();\nif (!c) return APP_ERR_RUNTIME;\n// 依据 c->event 分派；只在本次调用读取 c->data。"),
    .init(id:"complete",title:"报告事件完成",group:"生命周期",signature:"void app_host_complete_v2(const app_result_v2_t *result);\n// SDK 提供便捷包装：app_complete(c, status, flags)",contract:"每个入口必须恰好报告一次，事件 ID 和运行代次必须匹配。result 固定 20 字节。START 成功才带 APP_READY；声明需要首屏的应用，还必须有本次真实显示完成凭据。",example:"uint32_t flags = c->event == APP_EVENT_START && status == APP_OK\n    ? APP_READY : 0;\napp_complete(c, status, flags);\nreturn status;"),
    .init(id:"display",title:"显示一帧",group:"显示",signature:"int32_t app_host_display_v2(const uint8_t *pixels, uint32_t bytes);",contract:"精确 800×480、每像素 1 byte、索引 0…5。同步等到刷新与最终 POWER_OFF 完成。每事件最多调用一次；STOP 禁止显示。应用按左上坐标绘图，不自行旋转。",example:"int status = app_host_display_v2(frame, APP_FRAME_BYTES);\n// 返回前 frame 必须保持有效。\nfree(frame);"),
    .init(id:"cache-put",title:"写入应用缓存",group:"缓存",signature:"int32_t app_host_cache_put_v2(uint32_t version,\n    const uint8_t *data, uint32_t bytes);",contract:"Host 复制数据，按应用 ID 隔离。每应用最多 512 KiB，总计 1 MiB，可回收且重启丢失。bytes=0 清除本应用缓存；分配失败应降级，不把缓存失败当显示失败。",example:"// 1 是应用自己定义的数据格式版本。\n(void)app_host_cache_put_v2(1, frame, APP_FRAME_BYTES);"),
    .init(id:"cache-get",title:"借用应用缓存",group:"缓存",signature:"const uint8_t *app_host_cache_get_v2(uint32_t version, uint32_t *bytes);",contract:"未命中返回 NULL。格式版本必须匹配。返回指针仅在当前事件有效，不 free、不跨事件保存；同一事件借用后不能再 put 替换缓存。",example:"uint32_t bytes = 0;\nconst uint8_t *cached = app_host_cache_get_v2(1, &bytes);\nif (cached && bytes == APP_FRAME_BYTES)\n    status = app_host_display_v2(cached, bytes);"),
    .init(id:"timer",title:"注册或取消定时器",group:"定时",signature:"int32_t app_host_timer_v2(uint32_t timer_id, uint32_t interval_ms);",contract:"每应用一个定时器；最小间隔 1000 ms。0 表示取消，切出自动取消。超时 tick 会合并，不保证补发每一次；用单调时钟计算经过时间。STOP 只能取消，不能新建定时器。",example:"// START 中注册；TIMER 事件检查 c->timer_id。\nstatus = app_host_timer_v2(1, 60000);\n// STOP 中取消：app_host_timer_v2(1, 0);"),
    .init(id:"monotonic",title:"读取单调时间",group:"时间",signature:"uint64_t app_host_monotonic_ms_v2(void);",contract:"返回本次开机后的毫秒数，适合超时和倒计时；重启归零。只能在有效应用事件中使用，不能当成日历时间。",example:"uint64_t now = app_host_monotonic_ms_v2();\nif (now - last_update >= 60000) {\n    // 计算状态变化；决定本事件是否需要整屏刷新。\n}"),
    .init(id:"wall",title:"读取日历时间",group:"时间",signature:"uint64_t app_host_wall_time_v2(void);",contract:"返回 Unix 秒。系统时间未达到合理日期时返回 0。当前 SDK 没有授时接口，也不承诺离线断电后仍有准确时间；日历类应用必须处理未知时间。",example:"uint64_t unix_seconds = app_host_wall_time_v2();\nif (!unix_seconds) {\n    // 显示等待校时，或由电脑预渲染带日期的图片。\n}"),
    .init(id:"ble-register",title:"注册受管 BLE 服务",group:"BLE 2.1",signature:"int32_t app_host_ble_register_v2(const app_ble_service_v2_t *service);",contract:"需 APP_MANIFEST_BLE。每个活动应用一个服务、最多 4 个特征、每值最多 128 byte。Host 复制描述，不保存 ELF 回调。发布异步，失败通过 APP_BLE_ERROR 事件通知。",example:"// service 为完整的静态 app_ble_service_v2_t 描述。\nstatus = app_host_ble_register_v2(&service);\n// 参见开发实践中的完整 ble-echo 示例。"),
    .init(id:"ble-set",title:"更新 BLE 特征值",group:"BLE 2.1",signature:"int32_t app_host_ble_set_v2(uint32_t characteristic,\n    const uint8_t *data, uint32_t bytes);",contract:"characteristic 是从 0 开始的特征索引，Host 复制数据。值长度受描述及 128 byte 上限约束。READ/WRITE/NOTIFY 按声明生效；STOP 不可发布新值。",example:"const uint8_t value[] = \"ready\";\nstatus = app_host_ble_set_v2(0, value, sizeof(value) - 1);"),
    .init(id:"ble-stop",title:"注销 BLE 服务",group:"BLE 2.1",signature:"int32_t app_host_ble_unregister_v2(void);",contract:"STOP 中清理自己的接收缓冲并注销服务。Host 在卸载 ELF 前取消服务与旧代次事件；旧 generation / service revision 的请求失效，常驻管理通道仍可用。",example:"if (c->event == APP_EVENT_STOP) {\n    free(receive_buffer); receive_buffer = NULL;\n    status = app_host_ble_unregister_v2();\n}"),
]

struct GuideEvent:Identifiable {
    let id:Int;let name:String;let title:String;let input:String;let work:String;let completion:String
}
let guideEvents:[GuideEvent]=[
    .init(id:1,name:"START",title:"装载后初始化",input:"新的 generation；准备建立本次应用生命周期。",work:"分配资源、注册受管服务；显示应用恢复自己的缓存，或绘制等待页。没有初始显示要求的应用可以不刷新。",completion:"APP_OK + APP_READY；若 manifest 声明需要首屏，还要完成一次有效显示。"),
    .init(id:2,name:"INPUT",title:"处理外部输入",input:"input_type、data、data_size；例如 APP_INPUT_PNG。",work:"先校验长度、格式、尺寸和颜色，再请求显示。坏输入返回对应错误；不要在验证之前碰显示硬件。",completion:"用本次 event_id / generation 报告结果，不再次设置 APP_READY。"),
    .init(id:3,name:"TIMER",title:"处理到期事件",input:"timer_id；过期 tick 会合并。",work:"检查单调时间和状态变化。定时事件不等于必须刷新；避免用每秒 tick 每秒整屏刷新。",completion:"快速返回；普通事件的逻辑看门狗为 5 秒。"),
    .init(id:4,name:"NETWORK",title:"接收联网变化",input:"network_connected；由 Host 合并交付连接状态。",work:"更新应用状态。它不是 Wi-Fi 凭据或 HTTP 客户端接口，业务取数仍可留在电脑端。",completion:"正常报告事件完成，不创建自己的联网线程。"),
    .init(id:6,name:"BLE",title:"处理受管蓝牙事件",input:"data 指向 app_ble_event_v2_t；先验证 data_size 和非空。",work:"按 CONNECTED / DISCONNECTED / WRITTEN / ERROR 分支处理。CONNECTED 代表已鉴权的 Host 会话。写入业务错误应通过应用协议表达。",completion:"业务状态、资源清理和结果报告都在本次事件完成。"),
    .init(id:5,name:"STOP",title:"释放后再卸载",input:"原应用退出；尚未卸载 ELF。",work:"释放应用缓冲，取消定时器，注销服务。不能显示，也不能注册新的定时器或 BLE 服务。",completion:"报告完成后 Host 清理旧代次资源，再卸载 ELF。")
]
