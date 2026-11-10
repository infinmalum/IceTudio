#pragma once

#include "runtime_wire.h"
#include <algorithm>
#include <cmath>
#include <cstring>
#include <stdexcept>
#include <string>
#include <time.h>
#include <vector>

inline void check(bool ok, const char* message) {
    if (!ok) throw std::runtime_error(message);
}

inline void parameter(ice_plugin_wire& wire, uint32_t id, float value, const std::string& name) {
    if (wire.param_count == ICE_PLUGIN_PARAMS || !std::isfinite(value)) return;
    auto& out = wire.params[wire.param_count++];
    out.id = id;
    out.value = std::clamp(value, 0.f, 1.f);
    std::memcpy(out.name, name.data(), std::min(name.size(), sizeof(out.name) - 1));
}

void runClap(const char* path, const char* id, bool instrument, bool offline, bool editor, ice_plugin_wire& wire);
void runVST3(const char* path, const char* id, bool instrument, bool offline, bool editor, ice_plugin_wire& wire);

[[noreturn]] inline void requestGraphChange(ice_plugin_wire& wire) {
    __atomic_store_n(&wire.status, 4, __ATOMIC_RELEASE);
    while (true) {
        timespec nap{0, 10000000};
        nanosleep(&nap, nullptr);
    }
}

template<typename Pump, typename Save>
void editUntilSave(ice_plugin_wire& wire, Pump pump, Save save) {
    __atomic_store_n(&wire.status, 1, __ATOMIC_RELEASE);
    while (__atomic_load_n(&wire.submitted, __ATOMIC_ACQUIRE) == 0) {
        check(pump(), "plugin editor closed before capturing changes");
    }
    save();
    __atomic_store_n(&wire.status, 3, __ATOMIC_RELEASE);
    while (true) {
        timespec nap{0, 10000000};
        nanosleep(&nap, nullptr);
    }
}

template<typename Process>
void processBlocks(ice_plugin_wire& wire, Process process) {
    __atomic_store_n(&wire.status, 1, __ATOMIC_RELEASE);
    uint32_t cursor = 0;
    while (true) {
        uint32_t submitted = __atomic_load_n(&wire.submitted, __ATOMIC_ACQUIRE);
        if (cursor == submitted) {
            // Wait outside any desktop or device audio callback.
            timespec nap{0, 250000};
            nanosleep(&nap, nullptr);
            continue;
        }
        check(submitted - cursor <= ICE_PLUGIN_SLOTS, "invalid queue distance");
        const auto& input = wire.in[cursor % ICE_PLUGIN_SLOTS];
        auto& output = wire.out[cursor % ICE_PLUGIN_SLOTS];
        check(input.sequence == cursor && input.frames > 0 && input.frames <= ICE_PLUGIN_FRAMES &&
              input.events <= ICE_PLUGIN_EVENTS && input.params <= ICE_PLUGIN_PARAMS, "invalid audio block");
        output.sequence = cursor;
        process(input, output);
        for (const auto& channel : output.audio)
            for (uint32_t i = 0; i < input.frames; ++i)
                check(std::isfinite(channel[i]) && std::abs(channel[i]) <= 1024, "invalid plugin audio");
        __atomic_store_n(&wire.completed, ++cursor, __ATOMIC_RELEASE);
    }
}
