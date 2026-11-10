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
#include <time.h>
#include <math.h>

struct ice_pcm {
  int16_t *samples;
  ice_audio_region region;
  int64_t prefix_end;
  int owner, ordinal;
};
struct ice_plan {
  ice_mix *mix;
  struct ice_pcm *pcm;
  int npcm, pcm_capacity;
  int64_t audio_limit;
  ice_event *events, *initial, *loop;
  int n, ni, nl;
  int32_t *channels;
  int nc;
  int64_t start, end, loopa, loopb;
  int looping, wrapped, groups[4];
};
struct ice_engine {
  fluid_settings_t *settings, *synth_settings;
  fluid_synth_t *synth[4], *audition;
  fluid_audio_driver_t *driver;
  pthread_mutex_t mutex;
  ice_plan *plan;
  char *font;
  int index, playing;
  int click_remaining, click_period, click_phase;
  int count_beats;
  int64_t count_frame, count_interval;
  int64_t frame, loops;
  atomic_llong callbacks, misses, overruns;
  atomic_llong timing[101];
  atomic_int callback_frames;
  uint32_t active[1024][128];
  // Instant audition (docs/p2-daw-workspace.md §7): one note group at a time,
  // off the timeline, on a channel the current plan does not use.
  int64_t rframe;       // render sample counter, advances even when stopped
  int64_t aud_off;      // rframe deadline for note-off
  int aud_chan, aud_program, aud_bank, aud_velocity;
  int aud_keys[8], aud_nkeys, aud_on;
};
enum { track_outputs = 16, synth_channels = 256 };
static int physical_channel(int logical) {
  return (logical % 16) * track_outputs + (logical % 256) / 16;
}

static void clear(ice_engine *e) {
  e->click_remaining = 0;
  e->count_beats = 0;
  if (!e->plan)
    return;
  for (int group = 0; group < 4; group++)
    if (e->plan->groups[group]) fluid_synth_set_portamento_time_mode(e->synth[group], FLUID_PORTAMENTO_TIME_MODE_AUTO);
  for (int i = 0; i < e->plan->nc; i++) {
    int global = e->plan->channels[i];
    int ch = physical_channel(global);
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
    fluid_synth_bank_select(synth, ch, global % 16 == 9 ? 128 : 0);
    fluid_synth_pitch_bend(synth, ch, 8192);
    // Releasing hold pedals queues voice releases. Kill voices afterwards so
    // those releases cannot supersede the queued voice-off during a swap.
    fluid_synth_all_sounds_off(synth, ch);
    memset(e->active[global], 0, sizeof(e->active[global]));
  }
}
static void begin_click(ice_engine *e, int accent) {
  e->click_remaining = 960;
  e->click_period = accent ? 32 : 48;
  e->click_phase = 0;
}
static void mix_click(ice_engine *e, int count, float *left, float *right) {
  for (int i = 0; i < count && e->click_remaining > 0; ++i) {
    int phase = e->click_phase;
    float triangle = phase < e->click_period / 2 ? phase : e->click_period - phase;
    float sample = (triangle * 4.0f / e->click_period - 1.0f) *
                   (e->click_remaining / 960.0f) * 0.13f;
    left[i] += sample;
    right[i] += sample;
    e->click_phase = (phase + 1) % e->click_period;
    --e->click_remaining;
  }
}
static void dispatch(ice_engine *e, ice_event v) {
  if (v.kind == 9) {
    begin_click(e, v.a);
    return;
  }
  int global = v.channel;
  if (global < 0 || global >= 1024)
    return;
  if (e->plan && ice_mix_midi(e->plan->mix, global / 16, v.kind, global % 16, v.a, v.b, v.token))
    return;
  int ch = physical_channel(global);
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
  int ch = e->aud_chan % 16;
  fluid_synth_t *synth = e->audition;
  for (int i = 0; i < e->aud_nkeys; i++)
    fluid_synth_noteoff(synth, ch, e->aud_keys[i]);
  fluid_synth_all_sounds_off(synth, ch);
  e->aud_on = 0;
  e->aud_nkeys = 0;
}
int ice_audition(ice_engine *e, int channel, int program, int bank,
                  const int *keys, int nkeys, int velocity, int64_t frames) {
  if (!e || channel < 0 || channel >= 1024 || !keys || nkeys < 1 || nkeys > 8 ||
      program < 0 || program > 127 || velocity < 1 || velocity > 127 || frames < 1)
    return 0;
  for (int i = 0; i < nkeys; i++)
    if (keys[i] < 0 || keys[i] > 127)
      return 0;
  if (!e->audition) {
    fluid_synth_t *synth = new_fluid_synth(e->settings);
    if (!synth) return 0;
    if (fluid_synth_sfload(synth, e->font, 1) < 0) {
      delete_fluid_synth(synth);
      return 0;
    }
    pthread_mutex_lock(&e->mutex);
    e->audition = synth;
  } else {
    pthread_mutex_lock(&e->mutex);
  }
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
  int ch = channel % 16;
  fluid_synth_t *synth = e->audition;
  fluid_synth_cc(synth, ch, 7, 100);
  fluid_synth_cc(synth, ch, 10, 64);
  fluid_synth_cc(synth, ch, 11, 127);
  fluid_synth_pitch_bend(synth, ch, 8192);
  fluid_synth_set_channel_type(synth, ch, e->aud_bank == 128 ? CHANNEL_TYPE_DRUM : CHANNEL_TYPE_MELODIC);
  fluid_synth_bank_select(synth, ch, e->aud_bank);
  fluid_synth_program_change(synth, ch, program);
  for (int i = 0; i < nkeys; i++)
    fluid_synth_noteon(synth, ch, keys[i], velocity);
  pthread_mutex_unlock(&e->mutex);
  return 1;
}
void ice_audition_stop(ice_engine *e) {
  if (!e)
    return;
  pthread_mutex_lock(&e->mutex);
  audition_silence(e);
  pthread_mutex_unlock(&e->mutex);
}
static void render_synth(ice_engine *e, ice_mix *mix, int group, int len,
                         float *left, float *right) {
  float tmp[2 * track_outputs][ICE_MIX_BLOCK] = {{0}};
  float *outputs[2 * track_outputs];
  for (int i = 0; i < 2 * track_outputs; ++i) outputs[i] = tmp[i];
  // Missing tracks use separate scratch buffers.
  if (mix) for (int i = 0; i < 2 * track_outputs; ++i) {
    float *input = ice_mix_input(mix, group * track_outputs + i/2, i%2);
    if (input) outputs[i] = input;
  }
  fluid_synth_process(e->synth[group], len, 0, NULL, 2 * track_outputs, outputs);
  if (mix) return;
  for (int i = 0; i < 2 * track_outputs; i += 2)
    for (int k = 0; k < len; ++k) {
      left[k] += outputs[i][k];
      right[k] += outputs[i+1][k];
    }
}

static int64_t monotonic_ns(void) {
  struct timespec time;
  clock_gettime(CLOCK_MONOTONIC, &time);
  return time.tv_sec * INT64_C(1000000000) + time.tv_nsec;
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
    if (e->count_beats > 0) {
      int64_t in_beat = e->count_frame % e->count_interval;
      if (in_beat == 0) begin_click(e, e->count_frame == 0);
      int64_t until = e->count_interval - in_beat;
      int len = until < n - offset ? (int)until : n - offset;
      mix_click(e, len, left + offset, right + offset);
      e->count_frame += len;
      offset += len;
      if (e->count_frame >= e->count_interval * e->count_beats) {
        e->count_beats = 0;
        e->click_remaining = 0;
        chase(e, p->initial, p->ni);
      }
      continue;
    }
    if (p->looping && e->frame >= p->loopb) {
      clear(e);
      ice_mix_loop(p->mix);
      p->wrapped = 1;
      e->frame = p->loopa;
      e->index = lower(p, e->frame);
      chase(e, p->loop, p->nl);
      e->loops++;
    }
    if (!p->looping && e->frame >= p->end) {
      clear(e);
      ice_mix_reset(p->mix);
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
    if (p->mix) for (int t = 0; t < ICE_MIX_TRACKS; ++t)
      for (int c = 0; c < 2; ++c) {
        float *input = ice_mix_input(p->mix, t, c);
        if (input) memset(input, 0, len * sizeof(*input));
      }
    for (int group = 0; group < 4; group++)
      if (p->groups[group])
        render_synth(e, p->mix, group, len, left + offset, right + offset);
    int lo = 0, hi = p->npcm;
    while (lo < hi) {
      int mid = lo + (hi-lo)/2;
      if (p->pcm[mid].prefix_end <= e->frame) lo = mid+1;
      else hi = mid;
    }
    for (int a = lo; a < p->npcm; ++a) {
      const struct ice_pcm *pcm = &p->pcm[a];
      const ice_audio_region *r = &pcm->region;
      if (r->start >= e->frame + len) break;
      int64_t first = e->frame > r->start ? e->frame : r->start;
      int64_t last = e->frame + len < r->start + r->length ? e->frame + len : r->start + r->length;
      if (p->audio_limit > 0 && last > p->audio_limit) last = p->audio_limit;
      if (last <= first) continue;
      float *l = p->mix ? ice_mix_input(p->mix, r->track, 0) : left + offset;
      float *rr = p->mix ? ice_mix_input(p->mix, r->track, 1) : right + offset;
      if (!l || !rr) continue;
      for (int64_t pos = first; pos < last; ++pos) {
        int64_t i = pos-r->start;
        float gain = r->gain;
        int out = (int)(pos-e->frame);
        if (!r->rate) {
          int64_t src = r->source + (r->reverse ? r->length-i-1 : i);
          if (i < r->fadein) gain *= (float)i/r->fadein;
          if (r->length-1-i < r->fadeout) gain *= (float)(r->length-1-i)/r->fadeout;
          l[out] += pcm->samples[src*2]*gain;
          rr[out] += pcm->samples[src*2+1]*gain;
          continue;
        }
        int64_t env = i < r->gate ? i : r->gate;
        if (env < r->attack) gain *= (float)env/r->attack;
        else if (env-r->attack < r->decay) gain *= 1-(1-r->sustain)*(float)(env-r->attack)/r->decay;
        else gain *= r->sustain;
        if (i >= r->gate && r->release) gain *= 1-(float)(i-r->gate)/r->release;
        double sample = i*r->rate;
        if (r->loop_source) sample = fmod(sample, r->source_frames);
        int64_t index = (int64_t)sample;
        if (index >= r->source_frames) continue;
        float fraction = (float)(sample-index);
        const int16_t *src = pcm->samples+(r->source+index)*2;
        const int16_t *next = index+1<r->source_frames ? src+2 : r->loop_source ? pcm->samples+r->source*2 : src;
        l[out] += (src[0]+fraction*(next[0]-src[0]))*gain;
        rr[out] += (src[1]+fraction*(next[1]-src[1]))*gain;
      }
    }
    if (p->mix) ice_mix_render(p->mix, e->frame, len, left + offset, right + offset,
                               p->loopa, p->wrapped ? p->loopb : 0);
    mix_click(e, len, left + offset, right + offset);
    offset += len;
    e->frame += len;
  }
  // Count-in pauses the project, but audition can run independently.
  for (int off = 0; off < n && e->aud_on;) {
    int64_t until = e->aud_off - e->rframe - off;
    if (until <= 0) {
      audition_silence(e);
      break;
    }
    int len = n - off > ICE_MIX_BLOCK ? ICE_MIX_BLOCK : n - off;
    if (until < len) len = (int)until;
    float l[ICE_MIX_BLOCK], r[ICE_MIX_BLOCK];
    fluid_synth_write_float(e->audition, len, l, 0, 1, r, 0, 1);
    for (int i = 0; i < len; ++i) {
      left[off+i] += l[i];
      right[off+i] += r[i];
    }
    off += len;
  }
  e->rframe += n;
  pthread_mutex_unlock(&e->mutex);
}
static int audio(void *data, int n, int nfx, float *fx[], int nout,
                 float *out[]) {
  (void)nfx;
  (void)fx;
  if (nout < 2 || n <= 0)
    return FLUID_FAILED;
  ice_engine *e = data;
  int64_t start = monotonic_ns();
  ice_render(e, n, out[0], out[1]);
  int64_t elapsed = monotonic_ns() - start;
  int64_t percent = elapsed * 48000 / ((int64_t)n * 10000000);
  atomic_store(&e->callback_frames, n);
  atomic_fetch_add(&e->timing[percent > 100 ? 100 : percent], 1);
  if (percent >= 100) atomic_fetch_add(&e->overruns, 1);
  return FLUID_OK;
}
static pthread_once_t init_once = PTHREAD_ONCE_INIT;
static void register_drivers(void) {
  static const char *drivers[] = {"pulseaudio", "pipewire", "alsa", NULL};
  fluid_audio_driver_register(drivers);
}
ice_engine *ice_new(const char *font, const char *driver, int offline, int period) {
  pthread_once(&init_once, register_drivers);
  ice_engine *e = calloc(1, sizeof(*e));
  if (!e)
    return NULL;
  pthread_mutex_init(&e->mutex, NULL);
  e->font = strdup(font);
  if (!e->font) {
    ice_delete(e);
    return NULL;
  }
  e->settings = new_fluid_settings();
  e->synth_settings = new_fluid_settings();
  if (!e->settings || !e->synth_settings) {
    ice_delete(e);
    return NULL;
  }
  fluid_settings_setnum(e->synth_settings, "synth.sample-rate", 48000);
  fluid_settings_setnum(e->synth_settings, "synth.gain", 0.18);
  fluid_settings_setnum(e->settings, "synth.sample-rate", 48000);
  fluid_settings_setnum(e->settings, "synth.gain", 0.18);
  fluid_settings_setint(e->settings, "synth.reverb.active", 0);
  fluid_settings_setint(e->settings, "synth.chorus.active", 0);
  fluid_settings_setint(e->synth_settings, "synth.midi-channels", synth_channels);
  fluid_settings_setint(e->synth_settings, "synth.audio-channels", track_outputs);
  fluid_settings_setint(e->synth_settings, "synth.audio-groups", track_outputs);
  fluid_settings_setint(e->synth_settings, "synth.polyphony", 256);
  fluid_settings_setint(e->synth_settings, "synth.reverb.active", 0);
  fluid_settings_setint(e->synth_settings, "synth.chorus.active", 0);
  fluid_settings_setint(e->settings, "audio.period-size", period);
  fluid_settings_setint(e->settings, "audio.periods", 3);
  if (driver && *driver)
    fluid_settings_setstr(e->settings, "audio.driver", driver);
  for (int group = 0; group < 4; group++) {
    e->synth[group] = new_fluid_synth(e->synth_settings);
    if (!e->synth[group]) {
      ice_delete(e);
      return NULL;
    }
    if (fluid_synth_sfload(e->synth[group], font, 1) < 0) {
      ice_delete(e);
      return NULL;
    }
    fluid_synth_reset_basic_channel(e->synth[group], -1);
    for (int ch = 0; ch < synth_channels; ch++) {
      fluid_synth_set_basic_channel(e->synth[group], ch, FLUID_CHANNEL_MODE_OMNIOFF_POLY, 1);
      fluid_synth_set_channel_type(e->synth[group], ch,
                                   ch / track_outputs == 9 ? CHANNEL_TYPE_DRUM
                                                : CHANNEL_TYPE_MELODIC);
      fluid_synth_bank_select(e->synth[group], ch, ch / track_outputs == 9 ? 128 : 0);
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
void ice_plan_mixer(ice_plan *p, ice_mix *mix) { p->mix = mix; }
void ice_plan_audio_limit(ice_plan *p, int64_t frame) { p->audio_limit = frame; }
void ice_plan_delete(ice_plan *p) {
  if (p) {
    ice_mix_free(p->mix);
    free(p->events);
    free(p->initial);
    free(p->loop);
    free(p->channels);
    for (int i = 0; i < p->npcm; ++i) if (p->pcm[i].owner) free(p->pcm[i].samples);
    free(p->pcm);
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
int ice_plan_audio_reserve(ice_plan *p, int n) {
  if (!n) return 1;
  if (n < 0 || n > 16640) return 0;
  p->pcm = calloc(n, sizeof(*p->pcm));
  p->pcm_capacity = n;
  return p->pcm != NULL;
}

int ice_plan_audio(ice_plan *p, const int16_t *samples, int64_t total,
                   int reuse, ice_audio_region r) {
  if (p->npcm >= p->pcm_capacity || r.length<1 || r.source_frames<1 ||
      r.source<0 || r.source>total || r.source_frames>total-r.source)
    return 0;
  int16_t *data = reuse >= 0 && reuse < p->npcm ?
      p->pcm[reuse].samples : copy(samples, total * 2 * sizeof(int16_t));
  if (!data) return 0;
  p->pcm[p->npcm] = (struct ice_pcm){data, r, 0, reuse < 0, p->npcm};
  p->npcm++;
  return 1;
}

static int compare_pcm(const void *a, const void *b) {
  const struct ice_pcm *first = a, *second = b;
  if (first->region.start != second->region.start)
    return first->region.start < second->region.start ? -1 : 1;
  return first->ordinal - second->ordinal;
}

void ice_plan_audio_sort(ice_plan *p) {
  if (p->npcm>1) qsort(p->pcm,p->npcm,sizeof(*p->pcm),compare_pcm);
  int64_t end=0;
  for (int i=0;i<p->npcm;++i) {
    int64_t next=p->pcm[i].region.start+p->pcm[i].region.length;
    if (next>end) end=next;
    p->pcm[i].prefix_end=end;
  }
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
void ice_install(ice_engine *e, ice_plan *p, int playing, int beats, int64_t beat_frames) {
  pthread_mutex_lock(&e->mutex);
  clear(e);
  ice_plan *old = e->plan;
  e->plan = p;
  e->frame = p->start;
  e->index = lower(p, p->start);
  e->playing = playing;
  e->count_frame = 0;
  e->count_interval = beat_frames;
  e->count_beats = playing && beat_frames > 0 ? beats : 0;
  if (playing && e->count_beats == 0)
    chase(e, p->initial, p->ni);
  pthread_mutex_unlock(&e->mutex);
  ice_plan_delete(old);
}
void ice_stop(ice_engine *e) {
  pthread_mutex_lock(&e->mutex);
  clear(e);
  if (e->plan) ice_mix_reset(e->plan->mix);
  e->playing = 0;
  pthread_mutex_unlock(&e->mutex);
}
ice_status ice_get_status(ice_engine *e) {
  pthread_mutex_lock(&e->mutex);
  ice_status s = {e->frame, atomic_load(&e->callbacks), atomic_load(&e->misses),
                  e->loops, e->playing, e->count_beats > 0};
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
  if (e->audition) delete_fluid_synth(e->audition);
  free(e->font);
  if (e->settings)
    delete_fluid_settings(e->settings);
  if (e->synth_settings)
    delete_fluid_settings(e->synth_settings);
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
  int ch = physical_channel(channel), value = -1;
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

void ice_read_meters(ice_engine *e, ice_meter *out, int count) {
  pthread_mutex_lock(&e->mutex);
  for (int i = 0; i < count; ++i) out[i] = ice_mix_meter(e->plan ? e->plan->mix : NULL, i);
  pthread_mutex_unlock(&e->mutex);
}

void ice_reset_meters(ice_engine *e) {
  pthread_mutex_lock(&e->mutex);
  if (e->plan) ice_mix_reset_meters(e->plan->mix);
  pthread_mutex_unlock(&e->mutex);
}

int ice_plugin_override(ice_engine *e, int i, int slot, uint32_t id, float value) {
  pthread_mutex_lock(&e->mutex);
  int ok = e->plan && ice_mix_plugin_override(e->plan->mix, i, slot, id, value);
  pthread_mutex_unlock(&e->mutex);
  return ok;
}

void ice_override(ice_engine *e, int index, int slot, int parameter, float value) {
  pthread_mutex_lock(&e->mutex);
  if (e->plan) ice_mix_override(e->plan->mix, index, slot, parameter, value);
  pthread_mutex_unlock(&e->mutex);
}

int ice_callback_p99(ice_engine *e) {
  int64_t counts[101], total = 0;
  for (int i = 0; i < 101; ++i) {
    counts[i] = atomic_load(&e->timing[i]);
    total += counts[i];
  }
  int64_t rank = (total*99 + 99)/100;
  for (int i = 0; i < 101; ++i) {
    rank -= counts[i];
    if (rank <= 0) return i;
  }
  return 100;
}

int64_t ice_overruns(ice_engine *e) { return atomic_load(&e->overruns); }

int ice_callback_frames(ice_engine *e) { return atomic_load(&e->callback_frames); }

void ice_release_plan(ice_engine *e) {
  pthread_mutex_lock(&e->mutex);
  ice_plan *old = e->plan;
  if (!e->playing) e->plan = NULL;
  else old = NULL;
  pthread_mutex_unlock(&e->mutex);
  ice_plan_delete(old);
}

int ice_failed_plugin(ice_engine *e, int node, int slot) {
  pthread_mutex_lock(&e->mutex);
  int result = e->plan && e->plan->mix && ice_mix_plugin_failed(e->plan->mix, node, slot);
  pthread_mutex_unlock(&e->mutex);
  return result;
}
