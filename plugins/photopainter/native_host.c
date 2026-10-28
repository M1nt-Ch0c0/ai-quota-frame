#include "native_host.h"

/* Installed once before registration. The host drains requests before unload. */
static cliproxy_host_api host_api;

void photopainter_set_host(const cliproxy_host_api* api) {
    host_api = *api;
}

int photopainter_host_call(const char* method, const uint8_t* request, size_t size, cliproxy_buffer* response) {
    if (!host_api.call || !host_api.free_buffer) return 1;
    return host_api.call(host_api.host_ctx, method, request, size, response);
}

void photopainter_host_free(void* pointer, size_t size) {
    if (pointer && host_api.free_buffer) host_api.free_buffer(pointer, size);
}
