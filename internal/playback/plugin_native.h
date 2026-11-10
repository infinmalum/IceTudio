#ifndef ICE_PLUGIN_NATIVE_H
#define ICE_PLUGIN_NATIVE_H
#include "../pluginhost/native/runtime_wire.h"

typedef struct {
  ice_plugin_wire *wire;
  ice_plugin_input input;
  ice_plugin_output output;
  uint32_t sequence;
  int filled, received, offline, failed, reset;
  int64_t rendered;
} ice_plugin_link;

int ice_plugin_submit(ice_plugin_link *p);
void ice_plugin_audio(ice_plugin_link *p, int len, float *l, float *r,
                      unsigned count, const uint32_t *ids, const float *values,
                      float *wet, float *out_l, float *out_r);
#endif
