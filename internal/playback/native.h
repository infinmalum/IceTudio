#ifndef ICETUDIO_NATIVE_PLAYBACK_H
#define ICETUDIO_NATIVE_PLAYBACK_H
#include <stdint.h>
typedef struct {
  int64_t frame;
  int32_t kind, channel, a, b;
  uint32_t token;
} ice_event;
typedef struct ice_engine ice_engine;
typedef struct ice_plan ice_plan;
typedef struct {
  int64_t frame, callbacks, misses, loops;
  int playing;
} ice_status;
ice_engine *ice_new(const char *font, const char *driver, int offline);
void ice_delete(ice_engine *e);
ice_plan *ice_plan_new(const ice_event *events, int n, const ice_event *initial,
                       int ni, const ice_event *loop, int nl,
                       const int32_t *channels, int nc, int64_t start,
                       int64_t end, int64_t loopa, int64_t loopb, int looping);
void ice_plan_delete(ice_plan *p);
void ice_install(ice_engine *e, ice_plan *p, int playing);
void ice_stop(ice_engine *e);
ice_status ice_get_status(ice_engine *e);
void ice_render(ice_engine *e, int n, float *left, float *right);
const char *ice_version(void);
// Read-only internal diagnostics: 0=CC, 1=sounding preset bank, 2=preset program.
int ice_channel_value(ice_engine *e, int channel, int kind, int cc);
#endif
