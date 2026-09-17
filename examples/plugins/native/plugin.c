/* Works as C or C++. See README.md for compiler commands. The example supports
 * compact JSON emitted by Ariadne; production plugins should use a JSON parser. */
#include "../../../api/plugin/ariadne_plugin.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <inttypes.h>
static const ariadne_host_v1 *host;
static uint64_t inputs;
static uint64_t number(const char *json,const char *key) {
 const char *p = strstr(json,key);
 return p ? (uint64_t)strtoull(p + strlen(key),NULL,10) : 0;
}
static int message(void *instance,const uint8_t *data,size_t length) {
 (void)instance;
 char *json = (char *)malloc(length+1);
 if(!json) return -1;
 memcpy(json,data,length);json[length]=0;
 uint64_t id = number(json,"\"id\":");
 if(!id || !strstr(json,"\"method\":")) {free(json);return 0;}
 char prefix[128];snprintf(prefix,sizeof(prefix),"{\"jsonrpc\":\"2.0\",\"id\":%" PRIu64 ",\"result\":",id);
 size_t capacity = 256;
 char *result = NULL;
 if(strstr(json,"\"method\":\"initialize\"")) result = (char *)"{\"api_version\":1}";
 else if(strstr(json,"\"method\":\"command\"")) result = (char *)"{\"text\":\"Hello from native C/C++\"}";
 else if(strstr(json,"\"method\":\"widget\"")) result = (char *)"{\"text\":\"native\"}";
 else if(strstr(json,"\"method\":\"view.input\"")) {++inputs;result=(char *)"null";}
 else if(strstr(json,"\"method\":\"view.render\"")) {
  uint64_t width=number(json,"\"width\":"),height=number(json,"\"height\":"),generation=number(json,"\"generation\":");
  const char *view=strstr(json,"\"id\":\"");char view_id[129]={0};
  if(!view||width==0||height==0||width*height>65536) {free(json);return -1;}
  view += 6;const char *end=strchr(view,'"');if(!end||(size_t)(end-view)>128){free(json);return -1;}memcpy(view_id,view,(size_t)(end-view));
  capacity = (size_t)(width*height)*128+1024;result=(char *)malloc(capacity);if(!result){free(json);return -1;}
  int used=snprintf(result,capacity,"{\"view_id\":\"%s\",\"generation\":%" PRIu64 ",\"width\":%" PRIu64 ",\"height\":%" PRIu64 ",\"cells\":[",view_id,generation,width,height);
  const char *title="Native C/C++ plugin";
  for(uint64_t i=0;i<width*height;++i) {
   char c=i<strlen(title)&&i<width?title[i]:' ';
   used+=snprintf(result+used,capacity-(size_t)used,"%s{\"text\":\"%c\",\"width\":1,\"style\":{\"foreground\":{\"r\":230,\"g\":230,\"b\":230},\"background\":{\"r\":20,\"g\":25,\"b\":30}}}",i?",":"",c);
  }
  snprintf(result+used,capacity-(size_t)used,"],\"cursor\":{\"visible\":false}}");
 } else result=(char *)"null";
 size_t size=strlen(prefix)+strlen(result)+2;char *response=(char *)malloc(size);if(!response){if(capacity>256)free(result);free(json);return -1;}
 snprintf(response,size,"%s%s}",prefix,result);int code=host->send((const uint8_t *)response,strlen(response));
 free(response);if(capacity>256)free(result);free(json);return code;
}
static void shutdown(void *instance) {(void)instance;}
ARIADNE_PLUGIN_EXPORT int ariadne_plugin_init(const ariadne_host_v1 *table,ariadne_plugin_v1 *plugin) {
 if(table->abi_version!=1||table->struct_size<sizeof(*table)) return -1;
 host=table;plugin->abi_version=1;plugin->struct_size=sizeof(*plugin);plugin->instance=NULL;plugin->message=message;plugin->shutdown=shutdown;
 printf("native stdout is a log, never protocol\n");fflush(stdout);
 return 0;
}
