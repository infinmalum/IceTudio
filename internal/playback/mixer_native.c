//go:build linux && cgo

#include "mixer_native.h"
#include <math.h>
#include <stdlib.h>
#include <string.h>

// Filled counts make transport resets O(number of nodes + edges). Old ring
// contents cannot leak into a new transport segment and need not be erased.
typedef struct { float (*data)[2]; int size, pos, filled; } history;
typedef struct {
  float value;
  ice_mix_point *points;
  int count, cursor;
} param;
typedef struct { uint32_t id; param value; float sent; int initialized; } plugin_param;
typedef struct {
  int kind, latency, num_params;
  ice_plugin_link *plugin;
  plugin_param *plugin_params;
  history memory, detector;
  float low[2], envelope, reduction, phase;
  param amount, wet;
} effect;
typedef struct {
  int track, effects, has_side, input_latency, output_latency;
  history align, source;
  uint32_t (*held)[128];
  effect fx[ICE_MIX_FX], instrument;
  param gain, pan;
  float input[2][ICE_MIX_BLOCK], side[2][ICE_MIX_BLOCK];
  ice_meter meter;
} node;
typedef struct { int from, to, side, next; float gain; history align; } edge;
struct ice_mix {
  node *nodes;
  edge *edges;
  int nn, ne, master;
  int tracks[ICE_MIX_TRACKS], outgoing[ICE_MIX_NODES];
  int64_t next_frame;
  param *override;
  float override_value;
};

static int allocate(history *h, int size) {
  h->size = size;
  if (!size) return 1;
  h->data = calloc(size, sizeof(*h->data));
  return h->data != NULL;
}

static void tap(const history *h, int n, float *l, float *r) {
  if (n > h->filled) {
    *l = *r = 0;
    return;
  }
  int i = h->pos - n;
  if (i < 0) i += h->size;
  *l = h->data[i][0];
  *r = h->data[i][1];
}

static void push(history *h, float l, float r) {
  h->data[h->pos][0] = l;
  h->data[h->pos][1] = r;
  if (++h->pos == h->size) h->pos = 0;
  if (h->filled < h->size) ++h->filled;
}

static void align(history *h, float *l, float *r) {
  if (!h->size) return;
  float a, b;
  tap(h, h->size, &a, &b);
  push(h, *l, *r);
  *l = a;
  *r = b;
}

ice_mix *ice_mix_new(int nodes, int edges, int master) {
  ice_mix *m = calloc(1, sizeof(*m));
  if (!m) return NULL;
  m->nodes = calloc(nodes, sizeof(*m->nodes));
  m->edges = edges ? calloc(edges, sizeof(*m->edges)) : NULL;
  if (!m->nodes || (edges && !m->edges)) {
    ice_mix_free(m);
    return NULL;
  }
  m->nn = nodes;
  m->ne = edges;
  m->master = master;
  m->next_frame = -1;
  for (int i = 0; i < ICE_MIX_TRACKS; i++) m->tracks[i] = -1;
  for (int i = 0; i < ICE_MIX_NODES; i++) m->outgoing[i] = -1;
  return m;
}

static void free_param(param *p) { free(p->points); }

static void free_effect(effect *f) {
  free(f->memory.data);
  free(f->detector.data);
  free_param(&f->amount);
  free_param(&f->wet);
  for (int i = 0; i < f->num_params; ++i) free_param(&f->plugin_params[i].value);
  free(f->plugin_params);
  free(f->plugin);
}

void ice_mix_free(ice_mix *m) {
  if (!m) return;
  for (int i = 0; i < m->nn; ++i) {
    node *n = &m->nodes[i];
    free(n->align.data);
    free(n->source.data);
    free(n->held);
    free_param(&n->gain);
    free_param(&n->pan);
    free_effect(&n->instrument);
    for (int j = 0; j < n->effects; ++j) free_effect(&n->fx[j]);
  }
  for (int i = 0; i < m->ne; ++i) free(m->edges[i].align.data);
  free(m->nodes);
  free(m->edges);
  free(m);
}

int ice_mix_node(ice_mix *m, int i, int track, int delay) {
  node *n = &m->nodes[i];
  n->track = track;
  n->input_latency = n->output_latency = delay;
  if (track >= 0) m->tracks[track] = i;
  return allocate(&n->align, track >= 0 ? delay : 0);
}

int ice_mix_effect(ice_mix *m, int i, int slot, int kind) {
  node *n = &m->nodes[i];
  effect *f = &n->fx[slot];
  n->effects++;
  f->kind = kind;
  f->latency = n->output_latency;
  switch (kind) {
    case 2: return allocate(&f->detector, f->latency - n->input_latency);
    case 3: n->output_latency += 64; return allocate(&f->memory, 64);
    case 4: return allocate(&f->memory, 24000);
    case 5: return allocate(&f->memory, 3937);
    case 6: return allocate(&f->memory, 721);
    default: return 1;
  }
}

static effect *plugin_slot(node *n, int slot) {
  return slot < 0 ? &n->instrument : &n->fx[slot];
}

int ice_mix_plugin(ice_mix *m, int i, int slot, ice_plugin_wire *wire, int latency, int offline, int count) {
  node *n = &m->nodes[i];
  effect *f = plugin_slot(n, slot);
  f->kind = slot < 0 ? 11 : 10;
  if (slot >= 0) n->effects++;
  f->latency = slot < 0 ? 0 : n->output_latency;
  n->output_latency += latency;
  if (slot < 0) {
    // The instrument feeds the track's input, before compensation for sends.
    // n->align already accounts for the source latency in the Go plan.
    n->input_latency += latency;
    if (!allocate(&n->source, latency)) return 0;
    if (wire && !(n->held = calloc(16, sizeof(*n->held)))) return 0;
  }
  if (!wire) return 1;
  f->plugin = calloc(1, sizeof(*f->plugin));
  if (!f->plugin) return 0;
  f->plugin->wire = wire;
  f->plugin->offline = offline;
  f->plugin->received = 0;
  if (count) {
    f->plugin_params = calloc(count, sizeof(*f->plugin_params));
    if (!f->plugin_params) return 0;
    f->num_params = count;
  }
  return allocate(&f->memory, slot >= 0 ? latency : 0);
}

int ice_mix_midi(ice_mix *m, int track, uint32_t kind, uint32_t channel,
                 uint32_t a, uint32_t b, uint32_t token) {
  if (!m || track >= ICE_MIX_TRACKS || m->tracks[track] < 0) return 0;
  node *n = &m->nodes[m->tracks[track]];
  effect *f = &n->instrument;
  if (!f->kind) return 0;
  ice_plugin_link *p = f->plugin;
  if (p && !p->failed && kind == 8) {
    uint32_t select_hi = a < 256 ? 101 : 99, select_lo = a < 256 ? 100 : 98;
    if (a == 3) {
      ice_mix_midi(m, track, 3, channel, 101, 0, 0);
      ice_mix_midi(m, track, 3, channel, 100, 4, 0);
      ice_mix_midi(m, track, 3, channel, 6, token, 0);
    }
    ice_mix_midi(m, track, 3, channel, select_hi, a < 256 ? 0 : a < 512 ? 120 : 127, 0);
    ice_mix_midi(m, track, 3, channel, select_lo, a % 256, 0);
    ice_mix_midi(m, track, 3, channel, 38, b & 127, 0);
    ice_mix_midi(m, track, 3, channel, 6, b >> 7, 0);
  }
  if (p && !p->failed && kind <= 7) {
    if (kind == 2) n->held[channel][a] = token;
    if (kind == 1 && n->held[channel][a] == token) n->held[channel][a] = 0;
    if (kind == 3 && (a == 120 || a == 123)) {
      // An explicit note-off also works for note-event plugins that don't
      // offer a MIDI CC mapping. Do not clear on sustain alone.
      for (uint32_t key = 0; key < 128; ++key) {
        uint32_t active = n->held[channel][key];
        if (active) ice_mix_midi(m, track, 1, channel, key, 0, active);
      }
    }
    uint32_t i = p->input.events;
    if (i == ICE_PLUGIN_EVENTS) p->failed = 1;
    else {
      p->input.event[i] = (ice_plugin_event){p->filled, kind, channel, a, b, token};
      p->input.events++;
    }
  }
  return 1;
}

int ice_mix_plugin_failed(ice_mix *m, int i, int slot) {
  ice_plugin_link *p = plugin_slot(&m->nodes[i], slot)->plugin;
  return p && (p->failed || __atomic_load_n(&p->wire->status, __ATOMIC_ACQUIRE) != 1);
}

void ice_mix_loop(ice_mix *m) {
  if (!m) return;
  for (int i = 0; i < m->nn; ++i) {
    effect *f = &m->nodes[i].instrument;
    if (f->plugin) {
      // Keep the transport queue monotonic across a wrap. If there is
      // already a partial block, release the active channels at this offset.
      for (int ch = 0; ch < 16; ++ch) ice_mix_midi(m, m->nodes[i].track, 3, ch, 120, 0, 0);
      if (!f->plugin->filled) f->plugin->reset = 1;
    }
  }
}

int ice_mix_edge(ice_mix *m, int i, int from, int to, int delay, float gain, int side) {
  edge *e = &m->edges[i];
  e->from = from;
  e->to = to;
  e->gain = gain;
  e->side = side;
  e->next = m->outgoing[from];
  m->outgoing[from] = i;
  if (side) m->nodes[to].has_side = 1;
  return allocate(&e->align, delay);
}

static param *parameter_ptr(ice_mix *m, int i, int slot, int p) {
  node *n = &m->nodes[i];
  if (slot < 0) return p ? &n->pan : &n->gain;
  return p ? &n->fx[slot].wet : &n->fx[slot].amount;
}

int ice_mix_parameter(ice_mix *m, int i, int slot, int which, float value,
                      const ice_mix_point *points, int count) {
  param *p = parameter_ptr(m, i, slot, which);
  p->value = value;
  p->cursor = -1;
  if (!count) return 1;
  p->points = malloc(count * sizeof(*points));
  if (!p->points) return 0;
  memcpy(p->points, points, count * sizeof(*points));
  p->count = count;
  return 1;
}

int ice_mix_plugin_param(ice_mix *m, int i, int slot, int parameter, uint32_t id, float val, float initial,
                         const ice_mix_point *points, int count) {
  effect *f = plugin_slot(&m->nodes[i], slot);
  plugin_param *p = &f->plugin_params[parameter];
  p->id = id;
  p->value.value = val;
  p->initialized = 1;
  p->sent = initial;
  p->value.cursor = -1;
  if (count) {
    p->value.points = malloc(count * sizeof(*points));
    if (!p->value.points) return 0;
    memcpy(p->value.points, points, count * sizeof(*points));
    p->value.count = count;
  }
  return 1;
}

int ice_mix_plugin_override(ice_mix *m, int i, int slot, uint32_t id, float value) {
  if (!m || i < 0 || i >= m->nn || slot >= m->nodes[i].effects) return 0;
  effect *f = plugin_slot(&m->nodes[i], slot);
  if (ice_mix_plugin_failed(m, i, slot)) return 0;
  for (int j = 0; j < f->num_params; ++j) {
    if (f->plugin_params[j].id == id) {
      m->override = &f->plugin_params[j].value;
      m->override_value = value;
      return 1;
    }
  }
  return 0;
}

void ice_mix_override(ice_mix *m, int i, int slot, int which, float value) {
  if (!m) return;
  m->override = i < 0 ? NULL : parameter_ptr(m, i, slot, which);
  m->override_value = value;
}

static void locate(param *p, int64_t frame) {
  int lo = 0, hi = p->count;
  while (lo < hi) {
    int mid = lo + (hi-lo)/2;
    if (p->points[mid].frame <= frame) lo = mid + 1;
    else hi = mid;
  }
  p->cursor = lo-1;
}

static float value(ice_mix *m, param *p, int64_t frame) {
  if (m->override == p) return m->override_value;
  if (p->cursor >= 0 && p->points[p->cursor].frame > frame) locate(p, frame);
  while (p->cursor + 1 < p->count && p->points[p->cursor+1].frame <= frame)
    ++p->cursor;
  if (p->cursor < 0) return p->value;
  const ice_mix_point *a = &p->points[p->cursor];
  if (a->step || p->cursor + 1 == p->count) return a->value;
  const ice_mix_point *b = a + 1;
  return a->value + (b->value - a->value) * (float)(frame - a->frame) / (float)(b->frame - a->frame);
}

void ice_mix_reset(ice_mix *m) {
  if (!m) return;
  m->next_frame = -1;
  for (int i = 0; i < m->nn; ++i) {
    node *n = &m->nodes[i];
    n->align.filled = n->source.filled = 0;
    n->meter.peak = 0;
    for (int j = 0; j < n->effects; ++j) {
      effect *f = &n->fx[j];
      f->memory.filled = f->detector.filled = 0;
      f->low[0] = f->low[1] = f->envelope = f->reduction = f->phase = 0;
    }
  }
  for (int i = 0; i < m->ne; ++i) m->edges[i].align.filled = 0;
}

void ice_mix_reset_meters(ice_mix *m) {
  if (m)
    for (int i = 0; i < m->nn; ++i) m->nodes[i].meter = (ice_meter){0};
}

ice_meter ice_mix_meter(ice_mix *m, int i) {
  return m && i >= 0 && i < m->nn ? m->nodes[i].meter : (ice_meter){0};
}

float *ice_mix_input(ice_mix *m, int track, int channel) {
  if (!m || track < 0 || track >= ICE_MIX_TRACKS || m->tracks[track] < 0) return NULL;
  return m->nodes[m->tracks[track]].input[channel];
}

static void run_effect(ice_mix *m, effect *f, int64_t frame, float detect,
                       float *l, float *r) {
  float amount = value(m, &f->amount, frame);
  float wet = value(m, &f->wet, frame);
  float a = *l, b = *r;
  switch (f->kind) {
    case 1: {
      // Fixed 1 kHz crossover. Amount at 50% is flat; tilt boosts one band
      // while cutting the other. Two separate state variables preserve stereo.
      f->low[0] += 0.1227f * (a - f->low[0]);
      f->low[1] += 0.1227f * (b - f->low[1]);
      if (fabsf(f->low[0]) < 1e-20f) f->low[0] = 0;
      if (fabsf(f->low[1]) < 1e-20f) f->low[1] = 0;
      float tilt = (amount - 0.5f) * 1.6f;
      a += tilt * (a - 2*f->low[0]);
      b += tilt * (b - 2*f->low[1]);
      break;
    }
    case 2: {
      if (detect < 0) detect = fmaxf(fabsf(a), fabsf(b));
      else {
        float unused = 0;
        align(&f->detector, &detect, &unused);
      }
      float alpha = detect > f->envelope ? 0.08f : 0.0005f;
      f->envelope += alpha * (detect - f->envelope);
      if (f->envelope < 1e-20f) f->envelope = 0;
      float threshold = 0.65f - 0.55f * amount;
      if (f->envelope > threshold) {
        float gain = (threshold + (f->envelope-threshold) * (1-0.85f*amount))/f->envelope;
        a *= gain;
        b *= gain;
      }
      break;
    }
    case 3: {
      // 64-frame stereo linked lookahead. Dry signal has the same delay.
      float ceiling = 1 - 0.75f*amount;
      float peak = fmaxf(fabsf(a), fabsf(b));
      float reduction = peak > ceiling ? 1 - ceiling/peak : 0;
      f->reduction = fmaxf(reduction, f->reduction * 0.9997f);
      if (f->reduction < 1e-20f) f->reduction = 0;
      align(&f->memory, l, r);
      peak = fmaxf(fabsf(*l), fabsf(*r));
      float gain = 1 - f->reduction;
      if (peak > ceiling) gain = fminf(gain, ceiling/peak);
      a = *l * gain;
      b = *r * gain;
      break;
    }
    case 4: {
      int delay = 2400 + (int)(21600 * amount);
      tap(&f->memory, delay, &a, &b);
      push(&f->memory, *l + a * 0.38f, *r + b * 0.38f);
      break;
    }
    case 5: {
      float c,d,e,g;
      tap(&f->memory, 1429, &a, &b);
      tap(&f->memory, 2131, &c, &d);
      tap(&f->memory, 3937, &e, &g);
      a = 0.5f*a + 0.3f*d + 0.2f*e;
      b = 0.5f*b + 0.3f*c + 0.2f*g;
      push(&f->memory, *l + b*(0.2f+0.55f*amount), *r + a*(0.2f+0.55f*amount));
      break;
    }
    case 6: {
      // Opposite triangular LFO phases move two fractional taps between
      // 1 and 15 ms. The dry path stays aligned with the rest of the graph.
      float wave = 1 - 4 * fabsf(f->phase - 0.5f);
      float left = 384 + 336 * amount * wave;
      float right = 384 - 336 * amount * wave;
      int left_frame = (int)left, right_frame = (int)right;
      float next_a, next_b, unused;
      tap(&f->memory, left_frame, &a, &unused);
      tap(&f->memory, left_frame + 1, &next_a, &unused);
      tap(&f->memory, right_frame, &unused, &b);
      tap(&f->memory, right_frame + 1, &unused, &next_b);
      a += (left - left_frame) * (next_a - a);
      b += (right - right_frame) * (next_b - b);
      push(&f->memory, *l, *r);
      f->phase += 0.35f / 48000;
      if (f->phase >= 1) f->phase -= 1;
      break;
    }
    case 7: {
      float alpha = 0.013f + 0.779f * amount * amount;
      f->low[0] += alpha * (a - f->low[0]);
      f->low[1] += alpha * (b - f->low[1]);
      if (fabsf(f->low[0]) < 1e-20f) f->low[0] = 0;
      if (fabsf(f->low[1]) < 1e-20f) f->low[1] = 0;
      a = f->low[0];
      b = f->low[1];
      break;
    }
    case 8: {
      float drive = 8 * amount;
      a = a * (1 + drive) / (1 + drive * fabsf(a));
      b = b * (1 + drive) / (1 + drive * fabsf(b));
      break;
    }
    case 9: {
      // Stereo linked gate, with separate open/close thresholds and
      // smoothed gain so a low-level tail does not chatter at the threshold.
      float peak = fmaxf(fabsf(a), fabsf(b));
      float threshold = 0.15f * amount;
      f->envelope += (peak > f->envelope ? 0.08f : 0.004f) * (peak - f->envelope);
      if (f->envelope < 1e-20f) f->envelope = 0;
      f->reduction = f->envelope >= threshold * (f->reduction ? 0.75f : 1);
      float target = f->reduction;
      f->low[0] += (target > f->low[0] ? 0.02f : 0.001f) * (target - f->low[0]);
      if (f->low[0] < 1e-20f) f->low[0] = 0;
      a *= f->low[0];
      b *= f->low[0];
      break;
    }
  }
  *l += wet * (a - *l);
  *r += wet * (b - *r);
}

static int64_t musical_frame(int64_t frame, int64_t loopa, int64_t loopb) {
  if (loopb > loopa && frame < loopa)
    return loopb - 1 - (loopa - 1 - frame) % (loopb-loopa);
  return frame;
}

static void run_plugin(ice_mix *m, effect *f, int len, int64_t frame, int64_t loopa, int64_t loopb,
                       float *l, float *r) {
  if (!f->plugin) return;
  uint32_t ids[ICE_PLUGIN_PARAMS];
  float values[ICE_PLUGIN_PARAMS], wet[ICE_MIX_BLOCK], out_l[ICE_MIX_BLOCK] = {0}, out_r[ICE_MIX_BLOCK] = {0};
  unsigned count = 0;
  int64_t position = musical_frame(frame-f->latency, loopa, loopb);
  // Parameter changes take effect at the next processing block boundary.
  if (!f->plugin->filled) {
    for (int i = 0; i < f->num_params; ++i) {
      plugin_param *p = &f->plugin_params[i];
      float v = value(m, &p->value, position);
      if (!p->initialized || p->sent != v || f->plugin->reset) {
        ids[count] = p->id;
        values[count++] = v;
        p->initialized = 1;
        p->sent = v;
      }
    }
  }
  for (int k = 0; k < len; ++k) wet[k] = f->kind == 11 ? 1 : value(m, &f->wet, musical_frame(frame+k-f->latency-f->memory.size, loopa, loopb));
  ice_plugin_audio(f->plugin, len, l, r, count, ids, values, wet, out_l, out_r);
  for (int k = 0; k < len; ++k) {
    align(&f->memory, &l[k], &r[k]);
    if (wet[k] > 0 && isfinite(out_l[k]) && isfinite(out_r[k]) && fabsf(out_l[k]) <= 1024 && fabsf(out_r[k]) <= 1024) {
      l[k] += wet[k] * (out_l[k] - l[k]);
      r[k] += wet[k] * (out_r[k] - r[k]);
    }
  }
}

void ice_mix_render(ice_mix *m, int64_t frame, int len, float *l, float *r,
                    int64_t loopa, int64_t loopb) {
  for (int i = 0; i < m->nn; ++i) {
    node *n = &m->nodes[i];
    if (n->track < 0) memset(n->input, 0, sizeof(n->input));
    if (n->has_side) memset(n->side, 0, sizeof(n->side));
    if (frame != m->next_frame) {
      locate(&n->gain, musical_frame(frame - n->output_latency, loopa, loopb));
      locate(&n->pan, musical_frame(frame - n->output_latency, loopa, loopb));
      for (int j = 0; j < n->effects; ++j) {
        locate(&n->fx[j].amount, musical_frame(frame - n->fx[j].latency, loopa, loopb));
        locate(&n->fx[j].wet, musical_frame(frame - n->fx[j].latency, loopa, loopb));
      }
    }
  }
  m->next_frame = frame + len;
  for (int i = 0; i < m->nn; ++i) {
    node *n = &m->nodes[i];
    float peak = n->meter.peak * fmaxf(0, 1 - len / 4800.0f);
    uint64_t clips = 0;
    float *left = n->input[0], *right = n->input[1];
    if (n->instrument.kind) {
      effect *f = &n->instrument;
      float synth_l[ICE_MIX_BLOCK] = {0}, synth_r[ICE_MIX_BLOCK] = {0};
      for (int off = 0; off < len;) {
        int span = len-off;
        if (f->plugin && span > ICE_PLUGIN_FRAMES-f->plugin->filled)
          span = ICE_PLUGIN_FRAMES-f->plugin->filled;
        run_plugin(m, f, span, frame+off, loopa, loopb, synth_l+off, synth_r+off);
        off += span;
      }
      for (int k = 0; k < len; ++k) {
        align(&n->source, &left[k], &right[k]);
        left[k] += synth_l[k];
        right[k] += synth_r[k];
      }
    }
    for (int k = 0; k < len; ++k) align(&n->align, &left[k], &right[k]);
    for (int j = 0; j < n->effects; ++j) {
      effect *f = &n->fx[j];
      if (f->kind == 10) {
        // Don't cross a realtime plugin block in a single control span.
        for (int off = 0; off < len;) {
          int span = len - off;
          if (f->plugin && span > ICE_PLUGIN_FRAMES - f->plugin->filled)
            span = ICE_PLUGIN_FRAMES - f->plugin->filled;
          run_plugin(m, f, span, frame+off, loopa, loopb, left+off, right+off);
          off += span;
        }
        continue;
      }
      for (int k = 0; k < len; ++k) {
        float detect = n->has_side ? fmaxf(fabsf(n->side[0][k]), fabsf(n->side[1][k])) : -1;
        run_effect(m, f, musical_frame(frame+k-f->latency, loopa, loopb), detect, &left[k], &right[k]);
      }
    }
    for (int k = 0; k < len; ++k) {
      float a = left[k], b = right[k];
      int64_t pos = musical_frame(frame+k-n->output_latency, loopa, loopb);
      float gain = value(m, &n->gain, pos), pan = value(m, &n->pan, pos);
      a *= gain * (pan > 0 ? 1-pan : 1);
      b *= gain * (pan < 0 ? 1+pan : 1);
      n->input[0][k] = a;
      n->input[1][k] = b;
      float sample_peak = fmaxf(fabsf(a), fabsf(b));
      peak = fmaxf(peak, sample_peak);
      clips += sample_peak > 1;
    }
    n->meter.peak = peak;
    n->meter.hold = fmaxf(n->meter.hold, peak);
    n->meter.clips += clips;
    for (int j = m->outgoing[i]; j >= 0; j = m->edges[j].next) {
      edge *e = &m->edges[j];
      node *dst = &m->nodes[e->to];
      float (*output)[ICE_MIX_BLOCK] = e->side ? dst->side : dst->input;
      for (int k = 0; k < len; ++k) {
        float a = n->input[0][k], b = n->input[1][k];
        align(&e->align, &a, &b);
        output[0][k] += a * e->gain;
        output[1][k] += b * e->gain;
      }
    }
  }
  for (int k = 0; k < len; ++k) {
    l[k] += m->nodes[m->master].input[0][k];
    r[k] += m->nodes[m->master].input[1][k];
  }
}
