#pragma once
#include <stdint.h>
#include <stdlib.h>

typedef struct { void* ptr; size_t len; } cliproxy_buffer;
typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);
typedef struct {
    uint32_t abi_version;
    void* host_ctx;
    cliproxy_host_call_fn call;
    cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

void photopainter_set_host(const cliproxy_host_api* api);
int photopainter_host_call(const char* method, const uint8_t* request, size_t size, cliproxy_buffer* response);
void photopainter_host_free(void* pointer, size_t size);
