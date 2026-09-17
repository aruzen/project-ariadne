#include "../../../api/plugin/ariadne_plugin.h"
int ariadne_native_open(const char *path);
int ariadne_native_message(const unsigned char *data, size_t length);
void ariadne_native_close(void);
uintptr_t ariadne_redirect_stdout(void);
