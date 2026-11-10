#ifndef ICE_PLUGIN_RUNTIME_WIRE_H
#define ICE_PLUGIN_RUNTIME_WIRE_H
#include <stdint.h>
#include <stddef.h>

// One mapping per process generation. Control and cursors are host-private
// except for the four 32-bit acquire/release words identified below.
enum {
    ICE_PLUGIN_FRAMES = 256,
    ICE_PLUGIN_SLOTS = 8,
    ICE_PLUGIN_LEAD = 512,
    ICE_PLUGIN_EVENTS = 512,
    ICE_PLUGIN_PARAMS = 512,
    ICE_PLUGIN_STATE = 1 << 20,
    ICE_PLUGIN_MAGIC = 0x49434531
};
typedef struct {
    uint32_t offset, kind, channel, a, b, token;
} ice_plugin_event;
typedef struct {
    uint32_t id;
    float value;
    char name[56];
} ice_plugin_param;
typedef struct {
    uint32_t sequence, frames, events, params, reset;
    uint32_t ids[ICE_PLUGIN_PARAMS];
    float values[ICE_PLUGIN_PARAMS];
    ice_plugin_event event[ICE_PLUGIN_EVENTS];
    float audio[2][ICE_PLUGIN_FRAMES];
} ice_plugin_input;
typedef struct {
    uint32_t sequence;
    float audio[2][ICE_PLUGIN_FRAMES];
} ice_plugin_output;
typedef struct {
    uint32_t magic, status, submitted, completed; // atomic words after magic
    uint32_t latency, param_count, state_size;
    char error[228];
    ice_plugin_param params[ICE_PLUGIN_PARAMS];
    unsigned char state[ICE_PLUGIN_STATE];
    ice_plugin_input in[ICE_PLUGIN_SLOTS];
    ice_plugin_output out[ICE_PLUGIN_SLOTS];
} ice_plugin_wire;

#ifdef __cplusplus
static_assert(sizeof(ice_plugin_wire) == 1245632);
static_assert(offsetof(ice_plugin_wire, in) == 256 + 512*64 + 1048576);
static_assert(offsetof(ice_plugin_input, params) == 12);
static_assert(offsetof(ice_plugin_input, ids) == 20);
#endif
#endif
