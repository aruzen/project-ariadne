#ifndef ARIADNE_PLUGIN_H
#define ARIADNE_PLUGIN_H
#include <stdint.h>
#include <stddef.h>
#ifdef __cplusplus
extern "C" {
#endif
#define ARIADNE_PLUGIN_ABI_V1 1u
#if defined(_WIN32)
#define ARIADNE_PLUGIN_EXPORT __declspec(dllexport)
#else
#define ARIADNE_PLUGIN_EXPORT __attribute__((visibility("default")))
#endif
/* JSON buffers exclude Content-Length framing and are valid only for the call.
 * Receivers must copy retained buffers. Allocators never cross the ABI.
 * send/log may be called from plugin worker threads; message/shutdown are
 * serialized by the helper. message must return promptly; send later responses
 * asynchronously. Never write protocol messages to stdout. */
typedef struct ariadne_host_v1 {
 uint32_t abi_version;
 uint32_t struct_size;
 int (*send)(const uint8_t *json, size_t length);
 void (*log)(const uint8_t *text, size_t length);
} ariadne_host_v1;
typedef struct ariadne_plugin_v1 {
 uint32_t abi_version;
 uint32_t struct_size;
 void *instance;
 int (*message)(void *instance, const uint8_t *json, size_t length);
 void (*shutdown)(void *instance);
} ariadne_plugin_v1;
/* Implement this entrypoint. Return zero on success. The host table lives until
 * shutdown returns; all plugin threads must stop before shutdown returns. */
ARIADNE_PLUGIN_EXPORT int ariadne_plugin_init(const ariadne_host_v1 *host, ariadne_plugin_v1 *plugin);
#ifdef __cplusplus
}
#endif
#endif
