//go:build darwin && cgo

/* SPDX-License-Identifier: MIT
 * CoreBluetooth CoC bridge. A separate central retains the already connected
 * peripheral; the existing GATT client keeps its authenticated physical link.
 * No private cbgo fields, dependency patch, or application callback is used.
 */
#import <Foundation/Foundation.h>
#import <CoreBluetooth/CoreBluetooth.h>
#import "coc_darwin.h"

@interface PPCoC : NSObject <CBCentralManagerDelegate, CBPeripheralDelegate> {
@public
    CBCentralManager *central;
    CBPeripheral *peripheral;
    CBL2CAPChannel *channel;
    NSUUID *identifier;
    dispatch_queue_t queue;
    dispatch_semaphore_t opened;
    uint16_t psm;
    BOOL done,closed,connecting,failed;
}
- (void)finish:(BOOL)ok;
- (void)stop;
@end
@implementation PPCoC
- (void)finish:(BOOL)ok {
    if(done)return;
    failed=!ok;done=YES;dispatch_semaphore_signal(opened);
}
- (void)centralManagerDidUpdateState:(CBCentralManager *)manager {
    if(closed || connecting)return;
    if(manager.state==CBManagerStatePoweredOn){
        NSArray *found=[manager retrievePeripheralsWithIdentifiers:@[identifier]];
        if(!found.count){[self finish:NO];return;}
        peripheral=[found[0] retain];peripheral.delegate=self;connecting=YES;
        [manager connectPeripheral:peripheral options:nil];
    }else if(manager.state!=CBManagerStateUnknown && manager.state!=CBManagerStateResetting){[self finish:NO];}
}
- (void)centralManager:(CBCentralManager *)manager didConnectPeripheral:(CBPeripheral *)device {
    if(!closed)[device openL2CAPChannel:psm];
}
- (void)centralManager:(CBCentralManager *)manager didFailToConnectPeripheral:(CBPeripheral *)device error:(NSError *)error {
    [self finish:NO];
}
- (void)centralManager:(CBCentralManager *)manager didDisconnectPeripheral:(CBPeripheral *)device error:(NSError *)error {
    failed=YES;[self finish:NO];
}
- (void)peripheral:(CBPeripheral *)device didOpenL2CAPChannel:(CBL2CAPChannel *)value error:(NSError *)error {
    if(closed)return;
    if(error || !value){[self finish:NO];return;}
    channel=[value retain];
    /* Synchronous stream access is serialized on queue. No main runloop or
     * GUI thread is required; readiness polling happens in the Go context. */
    [channel.inputStream open];[channel.outputStream open];[self finish:YES];
}
- (void)stop {
    closed=YES;[channel.inputStream close];[channel.outputStream close];
    peripheral.delegate=nil;
    if(peripheral)[central cancelPeripheralConnection:peripheral];
    central.delegate=nil;
}
- (void)dealloc {
    [channel release];[peripheral release];[central release];[identifier release];
    dispatch_release(opened);dispatch_release(queue);[super dealloc];
}
@end

void *pp_coc_open(const char *uuid,uint16_t port,uint32_t timeout_ms){
    @autoreleasepool {
        PPCoC *c=[[PPCoC alloc] init];
        c->queue=dispatch_queue_create("photopainter.coc",DISPATCH_QUEUE_SERIAL);
        c->opened=dispatch_semaphore_create(0);c->psm=port;
        c->identifier=[[NSUUID alloc] initWithUUIDString:[NSString stringWithUTF8String:uuid]];
        if(!c->identifier){[c release];return NULL;}
        dispatch_sync(c->queue,^{c->central=[[CBCentralManager alloc] initWithDelegate:c queue:c->queue options:nil];});
        long timed_out=dispatch_semaphore_wait(c->opened,dispatch_time(DISPATCH_TIME_NOW,(int64_t)timeout_ms*NSEC_PER_MSEC));
        __block BOOL ok=NO;
        dispatch_sync(c->queue,^{ok=!timed_out && !c->failed && c->channel!=nil;if(!ok)[c stop];});
        if(!ok){[c release];return NULL;}
        return c;
    }
}
int pp_coc_read(void *handle,void *data,int capacity){
    PPCoC *c=(PPCoC *)handle;__block int result=0;
    dispatch_sync(c->queue,^{@autoreleasepool {
        NSInputStream *s=c->channel.inputStream;
        if(c->closed || c->failed || s.streamStatus==NSStreamStatusError || s.streamStatus==NSStreamStatusAtEnd || s.streamStatus==NSStreamStatusClosed)result=-1;
        else if(s.hasBytesAvailable)result=(int)[s read:data maxLength:(NSUInteger)capacity];
    }});return result;
}
int pp_coc_write(void *handle,const void *data,int length){
    PPCoC *c=(PPCoC *)handle;__block int result=0;
    dispatch_sync(c->queue,^{@autoreleasepool {
        NSOutputStream *s=c->channel.outputStream;
        if(c->closed || c->failed || s.streamStatus==NSStreamStatusError || s.streamStatus==NSStreamStatusAtEnd || s.streamStatus==NSStreamStatusClosed)result=-1;
        else if(s.hasSpaceAvailable)result=(int)[s write:data maxLength:(NSUInteger)length];
    }});return result;
}
void pp_coc_close(void *handle){
    if(!handle)return;PPCoC *c=(PPCoC *)handle;
    dispatch_sync(c->queue,^{[c stop];});[c release];
}
