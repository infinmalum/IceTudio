#ifndef ICE_MIXER_NATIVE_H
#define ICE_MIXER_NATIVE_H
#include <stdint.h>
#include "plugin_native.h"

enum { ICE_MIX_BLOCK = 256, ICE_MIX_TRACKS = 64, ICE_MIX_NODES = 81, ICE_MIX_FX = 8 };
typedef struct ice_mix ice_mix;
typedef struct { int64_t frame; float value; int step; } ice_mix_point;
typedef struct { float peak, hold; uint64_t clips; } ice_meter;

ice_mix *ice_mix_new(int nodes, int edges, int master);
void ice_mix_free(ice_mix *m);
int ice_mix_node(ice_mix *m, int i, int track, int delay);
int ice_mix_effect(ice_mix *m, int i, int slot, int kind);
int ice_mix_edge(ice_mix *m, int i, int from, int to, int delay, float gain, int side);
int ice_mix_parameter(ice_mix *m, int i, int slot, int parameter, float value,
                      const ice_mix_point *points, int count);
int ice_mix_plugin_override(ice_mix *m, int i, int slot, uint32_t id, float value);
void ice_mix_override(ice_mix *m, int i, int slot, int parameter, float value);
void ice_mix_reset(ice_mix *m);
int ice_mix_plugin(ice_mix *m, int i, int slot, ice_plugin_wire *wire, int latency, int offline, int count);
int ice_mix_plugin_param(ice_mix *m, int i, int slot, int parameter, uint32_t id, float val, float initial,
                         const ice_mix_point *points, int count);
int ice_mix_midi(ice_mix *m, int track, uint32_t kind, uint32_t channel,
                 uint32_t a, uint32_t b, uint32_t token);
int ice_mix_plugin_failed(ice_mix *m, int i, int slot);
void ice_mix_loop(ice_mix *m);
void ice_mix_reset_meters(ice_mix *m);
ice_meter ice_mix_meter(ice_mix *m, int i);
float *ice_mix_input(ice_mix *m, int track, int channel);
void ice_mix_render(ice_mix *m, int64_t frame, int n, float *l, float *r,
                    int64_t loopa, int64_t loopb);
#endif
