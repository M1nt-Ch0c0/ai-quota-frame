#ifndef PP_COC_DARWIN_H
#define PP_COC_DARWIN_H
#include <stdint.h>
void *pp_coc_open(const char *identifier,uint16_t psm,uint32_t timeout_ms);
int pp_coc_read(void *handle,void *data,int capacity);
int pp_coc_write(void *handle,const void *data,int length);
void pp_coc_close(void *handle);
#endif
