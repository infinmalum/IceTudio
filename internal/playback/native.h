#ifndef ICETUDIO_NATIVE_PLAYBACK_H
#define ICETUDIO_NATIVE_PLAYBACK_H
#include <stdint.h>
#include "mixer_native.h"
typedef struct {
  int64_t frame;
  int32_t kind, channel, a, b;
  uint32_t token;
} ice_event;
typedef struct ice_engine ice_engine;
typedef struct ice_plan ice_plan;
typedef struct {
  int64_t frame, callbacks, misses, loops;
  int playing, counting;
} ice_status;
ice_engine *ice_new(const char *font, const char *driver, int offline, int period);
void ice_delete(ice_engine *e);
ice_plan *ice_plan_new(const ice_event *events, int n, const ice_event *initial,
                       int ni, const ice_event *loop, int nl,
                       const int32_t *channels, int nc, int64_t start,
                       int64_t end, int64_t loopa, int64_t loopb, int looping);
typedef struct {
  int64_t start, source, source_frames, length, fadein, fadeout;
  int64_t attack, decay, gate, release;
  double rate;
  float gain, sustain;
  int reverse, track, loop_source;
} ice_audio_region;
int ice_plan_audio_reserve(ice_plan *p, int n);
int ice_plan_audio(ice_plan *p, const int16_t *samples, int64_t total, int reuse,
                   ice_audio_region region);
void ice_plan_audio_sort(ice_plan *p);
void ice_plan_mixer(ice_plan *p, ice_mix *mix);
void ice_plan_audio_limit(ice_plan *p, int64_t frame);
void ice_plan_delete(ice_plan *p);
void ice_install(ice_engine *e, ice_plan *p, int playing, int beats, int64_t beat_frames);
void ice_stop(ice_engine *e);
void ice_release_plan(ice_engine *e);
ice_status ice_get_status(ice_engine *e);
int ice_failed_plugin(ice_engine *e, int node, int slot);
void ice_render(ice_engine *e, int n, float *left, float *right);
const char *ice_version(void);
void ice_read_meters(ice_engine *e, ice_meter *out, int count);
void ice_reset_meters(ice_engine *e);
int ice_plugin_override(ice_engine *e, int i, int slot, uint32_t id, float value);
void ice_override(ice_engine *e, int index, int slot, int parameter, float value);
int ice_callback_p99(ice_engine *e);
int ice_callback_frames(ice_engine *e);
int64_t ice_overruns(ice_engine *e);
// Read-only internal diagnostics: 0=CC, 1=sounding preset bank, 2=preset program.
int ice_channel_value(ice_engine *e, int channel, int kind, int cc);
// Instant audition bypassing the timeline: bank/program + note-on now, note-off
// after `frames` render samples. Channel must be outside the current plan's
// channels; the audition group renders even while playback is stopped.
int ice_audition(ice_engine *e, int channel, int program, int bank,
                  const int *keys, int nkeys, int velocity, int64_t frames);
void ice_audition_stop(ice_engine *e);
#endif
