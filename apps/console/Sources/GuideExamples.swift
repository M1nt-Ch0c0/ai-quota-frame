import Foundation

struct GuideStarter: Identifiable {
    let id:Int; let title:String; let app:String; let artifact:String; let intro:String; let change:String; let code:String
}
let guideStarters:[GuideStarter] = [
    .init(id:0,title:"离线画面",app:"color-test",artifact:"color_test",intro:"在 START 自主生成逻辑帧；最适合从图案、名牌和色板开始。",change:"只改像素生成函数。保持一帧一调用，返回后释放缓冲。",code:#"""
/* SPDX-License-Identifier: MIT */
#include <stdlib.h>
#include "photopainter_app.h"
#ifndef COLOR_TEST_REVERSE
#define COLOR_TEST_REVERSE 0
#endif
APP_MANIFEST("color-test","Six color test",0,APP_REQUIRES_DISPLAY);
int main(int argc,char **argv) {
    (void)argc;(void)argv;
    const app_context_v2_t *c=app_host_context_v2();if(!c)return APP_ERR_RUNTIME;
    int r=APP_OK;
    if(c->event==APP_EVENT_START) {
        uint8_t *frame=malloc(APP_FRAME_BYTES);
        if(!frame)r=APP_ERR_MEMORY;
        else {
            for(unsigned y=0;y<APP_FRAME_HEIGHT;y++)for(unsigned x=0;x<APP_FRAME_WIDTH;x++) {
                unsigned color=x*6/APP_FRAME_WIDTH;
                frame[y*APP_FRAME_WIDTH+x]=COLOR_TEST_REVERSE?5-color:color;
            }
            r=app_host_display_v2(frame,APP_FRAME_BYTES);free(frame);
        }
    } else if(c->event==APP_EVENT_INPUT)r=APP_ERR_UNSUPPORTED;
    app_complete(c,r,c->event==APP_EVENT_START&&r==APP_OK?APP_READY:0);return r;
}
"""#),
    .init(id:1,title:"定时状态",app:"lifecycle",artifact:"lifecycle",intro:"不显示的完整生命周期例子，使用宿主定时器、单调时间和可选缓存。",change:"先把业务写成状态机，再按状态变化决定是否加入显示；新增显示需声明首屏能力。",code:#"""
/* SPDX-License-Identifier: MIT */
#include "photopainter_app.h"
APP_MANIFEST("lifecycle","Lifecycle timer test",0,0);
static uint32_t ticks;
int main(int argc,char **argv) {
    (void)argc;(void)argv;
    const app_context_v2_t *c=app_host_context_v2();if(!c)return APP_ERR_RUNTIME;
    int r=APP_OK;
    if(c->event==APP_EVENT_START){ticks=0;r=app_host_timer_v2(1,1000);}
    else if(c->event==APP_EVENT_TIMER) {
        ++ticks;(void)app_host_monotonic_ms_v2();(void)app_host_wall_time_v2();
        r=app_host_cache_put_v2(1,(const uint8_t *)&ticks,sizeof(ticks));
    } else if(c->event==APP_EVENT_STOP)r=app_host_timer_v2(1,0);
    else if(c->event==APP_EVENT_INPUT)r=APP_ERR_UNSUPPORTED;
    app_complete(c,r,c->event==APP_EVENT_START&&r==APP_OK?APP_READY:0);return r;
}
"""#),
    .init(id:2,title:"BLE 输入",app:"ble-echo",artifact:"ble_echo",intro:"完整 ABI 2.1 回显服务：注册、写入、读值、通知与 STOP 注销。",change:"将回显改成自己的命令协议，验证长度、类型、序号；在 STOP 与断线时释放业务缓冲。",code:#"""
/* SPDX-License-Identifier: MIT */
#include "photopainter_app.h"
APP_MANIFEST_BLE("ble-echo","Authenticated BLE echo",0,0);

/* Service a4fe1000-1536-4c88-a2ce-32ecdaac0471; characteristic ...1001...
 * The host copies this descriptor. It never calls a pointer into this ELF. */
static const app_ble_service_v2_t service={
    .size=sizeof(app_ble_service_v2_t),.count=1,
    .uuid={0xa4,0xfe,0x10,0x00,0x15,0x36,0x4c,0x88,0xa2,0xce,0x32,0xec,0xda,0xac,0x04,0x71},
    .characteristics={{
        .uuid={0xa4,0xfe,0x10,0x01,0x15,0x36,0x4c,0x88,0xa2,0xce,0x32,0xec,0xda,0xac,0x04,0x71},
        .properties=APP_BLE_READ|APP_BLE_WRITE|APP_BLE_NOTIFY,.max_bytes=APP_BLE_MAX_VALUE}}
};
int main(int argc,char **argv) {
    (void)argc;(void)argv;
    const app_context_v2_t *c=app_host_context_v2();if(!c)return APP_ERR_RUNTIME;
    int result=APP_OK;
    if(c->event==APP_EVENT_START) {
        result=app_host_ble_register_v2(&service);
        if(result==APP_OK)result=app_host_ble_set_v2(0,(const uint8_t *)"ready",5);
    } else if(c->event==APP_EVENT_BLE) {
        if(c->data_size!=sizeof(app_ble_event_v2_t) || !c->data)result=APP_ERR_ARGUMENT;
        else {
            const app_ble_event_v2_t *event=(const app_ble_event_v2_t *)c->data;
            if(event->type==APP_BLE_WRITTEN)result=app_host_ble_set_v2(event->characteristic,event->data,event->data_size);
            else if(event->type==APP_BLE_ERROR)result=app_host_ble_unregister_v2();
        }
    } else if(c->event==APP_EVENT_STOP)result=app_host_ble_unregister_v2();
    else if(c->event==APP_EVENT_INPUT)result=APP_ERR_UNSUPPORTED;
    app_complete(c,result,c->event==APP_EVENT_START && result==APP_OK?APP_READY:0);
    return result;
}
"""#),
]
