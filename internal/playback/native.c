// go:build linux && cgo
//  IceTudio native sample-frame scheduler. No Go callbacks or file/DB/UI work
//  occurs in the rendering path. FluidSynth is dynamically linked (LGPL).
#include "native.h"
#include <fluidsynth.h>
// The scheduler depends on 2.6 semantics (portamento time modes); behavior is
// verified against 2.6.1 — see docs/p2-playback.md.
#if FLUIDSYNTH_VERSION_MAJOR < 2 || (FLUIDSYNTH_VERSION_MAJOR == 2 && FLUIDSYNTH_VERSION_MINOR < 6)
#error "IceTUDIO requires FluidSynth >= 2.6 (portamento time modes)"
#endif
#include <pthread.h>
#include <stdatomic.h>
#include <stdlib.h>
#include <string.h>

struct ice_plan {
  ice_event *events, *initial, *loop;
  int n, ni, nl;
  int32_t *channels;
  int nc;
  int64_t start, end, loopa, loopb;
  int looping, groups[4];
};
struct ice_engine {
  fluid_settings_t *settings;
  fluid_synth_t *synth[4];
  fluid_audio_driver_t *driver;
  pthread_mutex_t mutex;
  ice_plan *plan;
  int index, playing;
  int64_t frame, loops;
  atomic_llong callbacks, misses;
  uint32_t active[1024][128];
  // Instant audition (docs/p2-daw-workspace.md §7): one note group at a time,
  // off the timeline, on a channel the current plan does not use.
  int64_t rframe;       // render sample counter, advances even when stopped
  int64_t aud_off;      // rframe deadline for note-off
  int aud_chan, aud_program, aud_bank, aud_velocity;
  int aud_keys[8], aud_nkeys, aud_on;
};
static void clear(ice_engine *e) {
  if (!e->plan)
    return;
  for (int group = 0; group < 4; group++)
    if (e->plan->groups[group]) fluid_synth_set_portamento_time_mode(e->synth[group], FLUID_PORTAMENTO_TIME_MODE_AUTO);
  for (int i = 0; i < e->plan->nc; i++) {
    int global = e->plan->channels[i];
    int ch = global % 256;
    fluid_synth_t *synth = e->synth[global / 256];
    for (int key = 0; key < 128; key++) if (e->active[global][key]) fluid_synth_noteoff(synth, ch, key);
    fluid_synth_cc(synth, ch, 121, 0);
    // CC121 intentionally retains several controls and RPN ranges. Reset
    // these too so a new score/seek cannot inherit the previous score's state.
    const int retained[] = {0,32,7,39,8,40,10,42,70,71,72,73,74,75,76,77,78,79,91,92,93,94,95};
    for (unsigned j = 0; j < sizeof(retained)/sizeof(retained[0]); j++) {
      int cc = retained[j];
      int value = cc == 7 ? 100 : cc == 8 || cc == 10 || (cc >= 70 && cc <= 79) ? 64 : 0;
      fluid_synth_cc(synth, ch, cc, value);
    }
    fluid_synth_pitch_wheel_sens(synth, ch, 2);
    fluid_synth_cc(synth, ch, 101, 0);
    fluid_synth_cc(synth, ch, 100, 5);
    fluid_synth_cc(synth, ch, 38, 64);
    fluid_synth_cc(synth, ch, 6, 0);
    fluid_synth_deactivate_tuning(synth, ch, 1);
    fluid_synth_reset_basic_channel(synth, ch);
    fluid_synth_set_basic_channel(synth, ch, FLUID_CHANNEL_MODE_OMNIOFF_POLY, 1);
    fluid_synth_cc(synth, ch, 65, 0);
    fluid_synth_cc(synth, ch, 64, 0);
    fluid_synth_cc(synth, ch, 7, 100);
    fluid_synth_cc(synth, ch, 10, 64);
    fluid_synth_cc(synth, ch, 11, 127);
    fluid_synth_bank_select(synth, ch, ch % 16 == 9 ? 128 : 0);
    fluid_synth_pitch_bend(synth, ch, 8192);
    // Releasing hold pedals queues voice releases. Kill voices afterwards so
    // those releases cannot supersede the queued voice-off during a swap.
    fluid_synth_all_sounds_off(synth, ch);
    memset(e->active[global], 0, sizeof(e->active[global]));
  }
}
static void dispatch(ice_engine *e, ice_event v) {
  int global = v.channel;
  if (global < 0 || global >= 1024)
    return;
  int ch = global % 256;
  fluid_synth_t *synth = e->synth[global / 256];
  switch (v.kind) {
  case 1:
    if (e->active[global][v.a] == v.token) {
      fluid_synth_noteoff(synth, ch, v.a);
      e->active[global][v.a] = 0;
    }
    break;
  case 2:
    fluid_synth_noteon(synth, ch, v.a, v.b);
    e->active[global][v.a] = v.token;
    break;
  case 3:
    // Every internal channel owns a one-channel basic group. MIDI mode
    // messages may change its mode, never disable another track's routing.
    if (v.a == 120 || v.a == 123 || v.a >= 124)
      for (int key = 0; key < 128; key++) if (e->active[global][key]) fluid_synth_noteoff(synth, ch, key);
    if (v.a >= 124) {
      int mode = 0;
      fluid_synth_get_basic_channel(synth, ch, NULL, &mode, NULL);
      if (v.a == 124) mode |= 2;
      if (v.a == 125) mode &= ~2;
      if (v.a == 126) mode |= 1;
      if (v.a == 127) mode &= ~1;
      fluid_synth_reset_basic_channel(synth, ch);
      fluid_synth_set_basic_channel(synth, ch, mode, 1);
    } else fluid_synth_cc(synth, ch, v.a, v.b);
    if (v.a == 120 || v.a == 123 || v.a >= 124)
      memset(e->active[global], 0, sizeof(e->active[global]));
    break;
  case 4:
    fluid_synth_program_change(synth, ch, v.a);
    break;
  case 5:
    fluid_synth_pitch_bend(synth, ch, v.a);
    break;
  case 6:
    fluid_synth_channel_pressure(synth, ch, v.a);
    break;
  case 7:
    fluid_synth_key_pressure(synth, ch, v.a, v.b);
    break;
  case 8: {
    int msb = v.b >> 7, lsb = v.b & 127;
    if (v.a < 256) {
      if (v.a == 3) {
        fluid_synth_cc(synth, ch, 101, 0);
        fluid_synth_cc(synth, ch, 100, 4);
        fluid_synth_cc(synth, ch, 6, v.token);
      }
      fluid_synth_cc(synth, ch, 101, 0);
      fluid_synth_cc(synth, ch, 100, v.a);
    } else {
      fluid_synth_cc(synth, ch, 99, v.a < 512 ? 120 : 127);
      fluid_synth_cc(synth, ch, 98, v.a % 256);
    }
    fluid_synth_cc(synth, ch, 38, lsb);
    fluid_synth_cc(synth, ch, 6, msb);
    break;
  }
  }
}
static void chase(ice_engine *e, ice_event *events, int n) {
  for (int i = 0; i < n; i++)
    dispatch(e, events[i]);
}
static int lower(ice_plan *p, int64_t f) {
  int a = 0, b = p->n;
  while (a < b) {
    int m = a + (b - a) / 2;
    if (p->events[m].frame < f)
      a = m + 1;
    else
      b = m;
  }
  return a;
}
// Caller holds e->mutex. Release audition notes and kill hanging voices; safe
// to call repeatedly. Never touches plan channels, so playback keeps sounding.
static void audition_silence(ice_engine *e) {
  if (!e->aud_on)
    return;
  int ch = e->aud_chan % 256;
  fluid_synth_t *synth = e->synth[e->aud_chan / 256];
  for (int i = 0; i < e->aud_nkeys; i++)
    fluid_synth_noteoff(synth, ch, e->aud_keys[i]);
  fluid_synth_all_sounds_off(synth, ch);
  e->aud_on = 0;
  e->aud_nkeys = 0;
}
void ice_audition(ice_engine *e, int channel, int program, int bank,
                  const int *keys, int nkeys, int velocity, int64_t frames) {
  if (!e || channel < 0 || channel >= 1024 || !keys || nkeys < 1 || nkeys > 8 ||
      program < 0 || program > 127 || velocity < 1 || velocity > 127 || frames < 1)
    return;
  for (int i = 0; i < nkeys; i++)
    if (keys[i] < 0 || keys[i] > 127)
      return;
  pthread_mutex_lock(&e->mutex);
  audition_silence(e);
  e->aud_chan = channel;
  e->aud_program = program;
  e->aud_bank = bank == 128 ? 128 : 0;
  e->aud_velocity = velocity;
  e->aud_nkeys = nkeys;
  for (int i = 0; i < nkeys; i++)
    e->aud_keys[i] = keys[i];
  e->aud_off = e->rframe + frames;
  e->aud_on = 1;
  int ch = channel % 256;
  fluid_synth_t *synth = e->synth[channel / 256];
  fluid_synth_cc(synth, ch, 7, 100);
  fluid_synth_cc(synth, ch, 10, 64);
  fluid_synth_cc(synth, ch, 11, 127);
  fluid_synth_pitch_bend(synth, ch, 8192);
  fluid_synth_bank_select(synth, ch, e->aud_bank);
  fluid_synth_program_change(synth, ch, program);
  for (int i = 0; i < nkeys; i++)
    fluid_synth_noteon(synth, ch, keys[i], velocity);
  pthread_mutex_unlock(&e->mutex);
}
void ice_audition_stop(ice_engine *e) {
  if (!e)
    return;
  pthread_mutex_lock(&e->mutex);
  audition_silence(e);
  pthread_mutex_unlock(&e->mutex);
}
void ice_render(ice_engine *e, int n, float *left, float *right) {
  memset(left, 0, n * sizeof(float));
  memset(right, 0, n * sizeof(float));
  atomic_fetch_add(&e->callbacks, 1);
  if (pthread_mutex_trylock(&e->mutex)) {
    atomic_fetch_add(&e->misses, 1);
    return;
  }
  int offset = 0;
  while (offset < n && e->playing && e->plan) {
    ice_plan *p = e->plan;
    if (p->looping && e->frame >= p->loopb) {
      clear(e);
      e->frame = p->loopa;
      e->index = lower(p, e->frame);
      chase(e, p->loop, p->nl);
      e->loops++;
    }
    if (!p->looping && e->frame >= p->end) {
      clear(e);
      e->playing = 0;
      break;
    }
    while (e->index < p->n && p->events[e->index].frame <= e->frame)
      dispatch(e, p->events[e->index++]);
    int64_t boundary = p->looping ? p->loopb : p->end;
    if (e->index < p->n && p->events[e->index].frame < boundary)
      boundary = p->events[e->index].frame;
    int len = (int)((boundary - e->frame) < (n - offset) ? (boundary - e->frame)
                                                         : (n - offset));
    if (len <= 0)
      continue;
    // FluidSynth internally renders in blocks; requesting short spans bounds
    // its event quantization to its internal buffer (typically 64 frames).
    if (len > 256)
      len = 256;
    float l[256], r[256];
    for (int group = 0; group < 4; group++)
      if (p->groups[group]) {
        fluid_synth_write_float(e->synth[group], len, l, 0, 1, r, 0, 1);
        for (int i = 0; i < len; i++) {
          left[offset + i] += l[i];
          right[offset + i] += r[i];
        }
      }
    offset += len;
    e->frame += len;
  }
  // Audition path: release expired notes, then render the audition group for
  // the whole callback even while playback is stopped. While playback runs,
  // the plan loop above already rendered that group; skip to avoid a double
  // render of the same synth.
  if (e->aud_on && e->rframe >= e->aud_off)
    audition_silence(e);
  if (e->aud_on) {
    int group = e->aud_chan / 256;
    int rendered = e->playing && e->plan && e->plan->groups[group];
    if (!rendered)
      for (int off = 0; off < n;) {
        int len = n - off > 256 ? 256 : n - off;
        float l[256], r[256];
        fluid_synth_write_float(e->synth[group], len, l, 0, 1, r, 0, 1);
        for (int i = 0; i < len; i++) {
          left[off + i] += l[i];
          right[off + i] += r[i];
        }
        off += len;
      }
  }
  e->rframe += n;
  pthread_mutex_unlock(&e->mutex);
}
static int audio(void *data, int n, int nfx, float *fx[], int nout,
                 float *out[]) {
  (void)nfx;
  (void)fx;
  if (nout < 2)
    return FLUID_FAILED;
  ice_render(data, n, out[0], out[1]);
  return FLUID_OK;
}
static pthread_once_t init_once = PTHREAD_ONCE_INIT;
static void register_drivers(void) {
  static const char *drivers[] = {"pulseaudio", "pipewire", "alsa", NULL};
  fluid_audio_driver_register(drivers);
}
ice_engine *ice_new(const char *font, const char *driver, int offline) {
  pthread_once(&init_once, register_drivers);
  ice_engine *e = calloc(1, sizeof(*e));
  if (!e)
    return NULL;
  pthread_mutex_init(&e->mutex, NULL);
  e->settings = new_fluid_settings();
  if (!e->settings) {
    ice_delete(e);
    return NULL;
  }
  fluid_settings_setnum(e->settings, "synth.sample-rate", 48000);
  fluid_settings_setnum(e->settings, "synth.gain", 0.18);
  fluid_settings_setint(e->settings, "synth.midi-channels", 256);
  fluid_settings_setint(e->settings, "synth.polyphony", 256);
  fluid_settings_setint(e->settings, "synth.reverb.active", 0);
  fluid_settings_setint(e->settings, "synth.chorus.active", 0);
  fluid_settings_setint(e->settings, "audio.period-size", 128);
  fluid_settings_setint(e->settings, "audio.periods", 3);
  if (driver && *driver)
    fluid_settings_setstr(e->settings, "audio.driver", driver);
  for (int group = 0; group < 4; group++) {
    e->synth[group] = new_fluid_synth(e->settings);
    if (!e->synth[group]) {
      ice_delete(e);
      return NULL;
    }
    if (fluid_synth_sfload(e->synth[group], font, 1) < 0) {
      ice_delete(e);
      return NULL;
    }
    fluid_synth_reset_basic_channel(e->synth[group], -1);
    for (int ch = 0; ch < 256; ch++) {
      fluid_synth_set_basic_channel(e->synth[group], ch, FLUID_CHANNEL_MODE_OMNIOFF_POLY, 1);
      fluid_synth_set_channel_type(e->synth[group], ch,
                                   ch % 16 == 9 ? CHANNEL_TYPE_DRUM
                                                : CHANNEL_TYPE_MELODIC);
      fluid_synth_bank_select(e->synth[group], ch, ch % 16 == 9 ? 128 : 0);
    }
  }
  if (!offline) {
    e->driver = new_fluid_audio_driver2(e->settings, audio, e);
    if (!e->driver) {
      ice_delete(e);
      return NULL;
    }
  }
  return e;
}
void ice_plan_delete(ice_plan *p) {
  if (p) {
    free(p->events);
    free(p->initial);
    free(p->loop);
    free(p->channels);
    free(p);
  }
}
static void *copy(const void *p, size_t n) {
  if (!n)
    return NULL;
  void *out = malloc(n);
  if (out)
    memcpy(out, p, n);
  return out;
}
ice_plan *ice_plan_new(const ice_event *events, int n, const ice_event *initial,
                       int ni, const ice_event *loop, int nl,
                       const int32_t *channels, int nc, int64_t start,
                       int64_t end, int64_t loopa, int64_t loopb, int looping) {
  ice_plan *p = calloc(1, sizeof(*p));
  if (!p)
    return NULL;
  p->events = copy(events, n * sizeof(*events));
  p->initial = copy(initial, ni * sizeof(*initial));
  p->loop = copy(loop, nl * sizeof(*loop));
  p->channels = copy(channels, nc * sizeof(*channels));
  if ((n && !p->events) || (ni && !p->initial) || (nl && !p->loop) ||
      (nc && !p->channels)) {
    ice_plan_delete(p);
    return NULL;
  }
  p->n = n;
  p->ni = ni;
  p->nl = nl;
  p->nc = nc;
  p->start = start;
  p->end = end;
  p->loopa = loopa;
  p->loopb = loopb;
  p->looping = looping;
  for (int i = 0; i < nc; i++)
    p->groups[channels[i] / 256] = 1;
  return p;
}
void ice_install(ice_engine *e, ice_plan *p, int playing) {
  pthread_mutex_lock(&e->mutex);
  clear(e);
  ice_plan *old = e->plan;
  e->plan = p;
  e->frame = p->start;
  e->index = lower(p, p->start);
  e->playing = playing;
  if (playing)
    chase(e, p->initial, p->ni);
  pthread_mutex_unlock(&e->mutex);
  ice_plan_delete(old);
}
void ice_stop(ice_engine *e) {
  pthread_mutex_lock(&e->mutex);
  clear(e);
  e->playing = 0;
  pthread_mutex_unlock(&e->mutex);
}
ice_status ice_get_status(ice_engine *e) {
  pthread_mutex_lock(&e->mutex);
  ice_status s = {e->frame, atomic_load(&e->callbacks), atomic_load(&e->misses),
                  e->loops, e->playing};
  pthread_mutex_unlock(&e->mutex);
  return s;
}
void ice_delete(ice_engine *e) {
  if (!e)
    return;
  if (e->driver)
    delete_fluid_audio_driver(e->driver);
  for (int i = 0; i < 4; i++)
    if (e->synth[i])
      delete_fluid_synth(e->synth[i]);
  if (e->settings)
    delete_fluid_settings(e->settings);
  ice_plan_delete(e->plan);
  pthread_mutex_destroy(&e->mutex);
  free(e);
}
const char *ice_version(void) { return fluid_version_str(); }
int ice_channel_value(ice_engine *e, int channel, int kind, int cc) {
  if (channel < 0 || channel >= 1024)
    return -1;
  pthread_mutex_lock(&e->mutex);
  fluid_synth_t *synth = e->synth[channel / 256];
  int ch = channel % 256, value = -1;
  if (kind == 0 && cc >= 0 && cc < 128)
    fluid_synth_get_cc(synth, ch, cc, &value);
  else if (kind == 3)
    fluid_synth_get_pitch_wheel_sens(synth, ch, &value);
  else if (kind == 4)
    value = (int)(fluid_synth_get_gen(synth, ch, cc) * 1000);
  else {
    fluid_preset_t *preset = fluid_synth_get_channel_preset(synth, ch);
    if (preset && kind == 1)
      value = fluid_preset_get_banknum(preset);
    if (preset && kind == 2)
      value = fluid_preset_get_num(preset);
  }
  pthread_mutex_unlock(&e->mutex);
  return value;
}
