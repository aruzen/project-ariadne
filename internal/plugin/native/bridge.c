//go:build cgo
#include "bridge.h"
#include <stdio.h>
#include <stdlib.h>
#if defined(_WIN32)
#include <windows.h>
#include <io.h>
#define DUP _dup
#define DUP2 _dup2
#else
#include <dlfcn.h>
#include <unistd.h>
#include <fcntl.h>
#define DUP dup
#define DUP2 dup2
#endif
extern int goNativeSend(void *, size_t);
extern void goNativeLog(void *, size_t);
static ariadne_plugin_v1 plugin;
static int send_json(const uint8_t *p, size_t n) { return goNativeSend((void *)p,n); }
static void log_text(const uint8_t *p, size_t n) { goNativeLog((void *)p,n); }
static const ariadne_host_v1 host = {1,sizeof(ariadne_host_v1),send_json,log_text};
uintptr_t ariadne_redirect_stdout(void) {
 fflush(stdout);
#if defined(_WIN32)
 HANDLE protocol;
 if (!DuplicateHandle(GetCurrentProcess(),GetStdHandle(STD_OUTPUT_HANDLE),GetCurrentProcess(),&protocol,0,FALSE,DUPLICATE_SAME_ACCESS)) return (uintptr_t)-1;
 if (DUP2(2,1) < 0 || !SetStdHandle(STD_OUTPUT_HANDLE,GetStdHandle(STD_ERROR_HANDLE))) {CloseHandle(protocol);return (uintptr_t)-1;}
 return (uintptr_t)protocol;
#else
 int protocol=DUP(1);
 if (protocol < 0 || DUP2(2,1) < 0) return (uintptr_t)-1;
 fcntl(protocol,F_SETFD,FD_CLOEXEC);
 return (uintptr_t)protocol;
#endif
}
int ariadne_native_open(const char *path) {
 int (*entry)(const ariadne_host_v1 *,ariadne_plugin_v1 *);
#if defined(_WIN32)
 int length=MultiByteToWideChar(CP_UTF8,MB_ERR_INVALID_CHARS,path,-1,NULL,0);
 if(length<=0) return -1;
 wchar_t *wide=(wchar_t *)malloc((size_t)length*sizeof(wchar_t));
 if(!wide) return -1;
 MultiByteToWideChar(CP_UTF8,MB_ERR_INVALID_CHARS,path,-1,wide,length);
 HMODULE library=LoadLibraryExW(wide,NULL,LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|LOAD_LIBRARY_SEARCH_DEFAULT_DIRS);
 free(wide);
 if (!library) return -1;
 entry = (int (*)(const ariadne_host_v1 *,ariadne_plugin_v1 *))GetProcAddress(library,"ariadne_plugin_init");
#else
 void *library = dlopen(path,RTLD_NOW|RTLD_LOCAL);
 if (!library) {fprintf(stderr,"dlopen: %s\n",dlerror());return -1;}
 entry = (int (*)(const ariadne_host_v1 *,ariadne_plugin_v1 *))dlsym(library,"ariadne_plugin_init");
#endif
 if (!entry) return -2;
 plugin.abi_version = 1; plugin.struct_size = sizeof(plugin);
 int code = entry(&host,&plugin);
 if (code || plugin.abi_version != 1 || plugin.struct_size < sizeof(plugin) || !plugin.message || !plugin.shutdown) return -3;
 return 0;
}
int ariadne_native_message(const unsigned char *data,size_t length) {return plugin.message(plugin.instance,data,length);}
void ariadne_native_close(void) {if(plugin.shutdown) plugin.shutdown(plugin.instance);}
