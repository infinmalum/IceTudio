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
typedef struct {
  int kind, latency;
  history memory, detector;
  float low[2], envelope, reduction, phase;
  param amount, wet;
} effect;
typedef struct {
  int track, effects, has_side, input_latency, output_latency;
  history align;
  effect fx[ICE_MIX_FX];
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

void ice_mix_free(ice_mix *m) {
  if (!m) return;
  for (int i = 0; i < m->nn; ++i) {
    node *n = &m->nodes[i];
    free(n->align.data);
    free_param(&n->gain);
    free_param(&n->pan);
    for (int j = 0; j < n->effects; ++j) {
      effect *f = &n->fx[j];
      free(f->memory.data);
      free(f->detector.data);
      free_param(&f->amount);
      free_param(&f->wet);
    }
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
    n->align.filled = 0;
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
    for (int k = 0; k < len; ++k) {
      int64_t pos = frame+k;
      float a = n->input[0][k], b = n->input[1][k];
      align(&n->align, &a, &b);
      float detect = n->has_side ? fmaxf(fabsf(n->side[0][k]), fabsf(n->side[1][k])) : -1;
      for (int j = 0; j < n->effects; ++j)
        run_effect(m, &n->fx[j], musical_frame(pos - n->fx[j].latency, loopa, loopb), detect, &a, &b);
      pos = musical_frame(pos - n->output_latency, loopa, loopb);
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
