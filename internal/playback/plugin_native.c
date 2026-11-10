//go:build linux && cgo

#include "plugin_native.h"
#include <math.h>
#include <string.h>
#include <time.h>

static int ready(ice_plugin_link *p, uint32_t sequence) {
  uint32_t completed = __atomic_load_n(&p->wire->completed, __ATOMIC_ACQUIRE);
  if (completed > p->sequence) return 0;
  if (completed <= sequence) return 0;
  const ice_plugin_output *source = &p->wire->out[sequence % ICE_PLUGIN_SLOTS];
  if (source->sequence != sequence) return 0;
  memcpy(&p->output, source, sizeof(p->output));
  int frames = ICE_PLUGIN_FRAMES;
  for (int i = 0; i < frames; ++i)
    if (!isfinite(p->output.audio[0][i]) || !isfinite(p->output.audio[1][i]) ||
        fabsf(p->output.audio[0][i]) > 1024 || fabsf(p->output.audio[1][i]) > 1024) {
      p->failed = 1;
      return 0;
    }
  return 1;
}

int ice_plugin_submit(ice_plugin_link *p) {
  if (p->failed || __atomic_load_n(&p->wire->status, __ATOMIC_ACQUIRE) != 1)
    return 0;
  uint32_t completed = __atomic_load_n(&p->wire->completed, __ATOMIC_ACQUIRE);
  if (completed > p->sequence || p->sequence - completed >= ICE_PLUGIN_SLOTS)
    return 0;
  p->input.sequence = p->sequence;
  p->input.reset = p->reset;
  memcpy(&p->wire->in[p->sequence % ICE_PLUGIN_SLOTS], &p->input, sizeof(p->input));
  __atomic_store_n(&p->wire->submitted, ++p->sequence, __ATOMIC_RELEASE);
  p->reset = 0;
  p->input.events = 0;
  return 1;
}

static void parameters(ice_plugin_input *in, unsigned count, const uint32_t *ids, const float *values) {
  in->params = count;
  memcpy(in->ids, ids, count * sizeof(*ids));
  memcpy(in->values, values, count * sizeof(*values));
}

static int wait_for_block(ice_plugin_link *p) {
  struct timespec nap = {0, 100000}, start, now;
  clock_gettime(CLOCK_MONOTONIC, &start);
  while (!ready(p, p->sequence-1)) {
    if (p->failed || __atomic_load_n(&p->wire->status, __ATOMIC_ACQUIRE) != 1)
      return 0;
    nanosleep(&nap, NULL);
    clock_gettime(CLOCK_MONOTONIC, &now);
    if ((now.tv_sec-start.tv_sec)*1000000000LL + now.tv_nsec-start.tv_nsec >= 1000000000LL)
      return 0;
  }
  return 1;
}

void ice_plugin_audio(ice_plugin_link *p, int len, float *l, float *r,
                      unsigned count, const uint32_t *ids, const float *values,
                      float *wet, float *out_l, float *out_r) {
  if (p->failed || __atomic_load_n(&p->wire->status, __ATOMIC_ACQUIRE) != 1) {
    p->failed = 1;
    memset(wet, 0, len*sizeof(*wet));
    return;
  }
  int lead = p->offline ? ICE_PLUGIN_FRAMES : ICE_PLUGIN_LEAD;
  for (int i = 0; i < len; ++i) {
    int k = p->filled;
    if (!k) {
      parameters(&p->input, count, ids, values);
      p->received = p->rendered >= lead &&
                    ready(p, (uint32_t)(p->rendered/ICE_PLUGIN_FRAMES - lead/ICE_PLUGIN_FRAMES));
    }
    p->input.audio[0][k] = l[i];
    p->input.audio[1][k] = r[i];
    wet[i] = p->received ? wet[i] : 0;
    out_l[i] = p->received ? p->output.audio[0][k] : 0;
    out_r[i] = p->received ? p->output.audio[1][k] : 0;
    ++p->rendered;
    if (++p->filled == ICE_PLUGIN_FRAMES) {
      p->input.frames = ICE_PLUGIN_FRAMES;
      if (!ice_plugin_submit(p) || (p->offline && !wait_for_block(p))) {
        p->failed = 1;
        memset(wet+i+1, 0, (len-i-1)*sizeof(*wet));
        return;
      }
      p->filled = 0;
    }
    // Dry alignment is the caller's responsibility, including during a miss.
  }
}
