#include <clap/clap.h>

#include <array>
#include <cstring>
#include <new>

namespace {
const char* features[]{CLAP_PLUGIN_FEATURE_AUDIO_EFFECT, nullptr};
const clap_plugin_descriptor_t descriptor{
    CLAP_VERSION, "icetudio.probe.latency", "IceTudio Latency Fixture", "IceTudio",
    "", "", "", "1", "Tests host restart and latency change", features,
};

struct Fixture {
    clap_plugin_t api{};
    const clap_host_t* host;
    uint32_t activation = 0, latency = 0, index = 0;
    std::array<float, 192> ring{};
};

Fixture& get(const clap_plugin_t* plugin) {
    return *static_cast<Fixture*>(plugin->plugin_data);
}

const clap_plugin_latency_t latencyExtension{
    [](const clap_plugin_t* plugin) { return get(plugin).latency; },
};

const clap_plugin_t* create(const clap_host_t* host) {
    auto* instance = new (std::nothrow) Fixture{};
    if (!instance) return nullptr;
    instance->host = host;
    instance->api = {
        &descriptor, instance,
        [](const clap_plugin_t*) { return true; },
        [](const clap_plugin_t* plugin) { delete &get(plugin); },
        [](const clap_plugin_t* plugin, double, uint32_t, uint32_t) {
            auto& self = get(plugin);
            self.latency = ++self.activation == 1 ? 64 : 192;
            self.ring.fill(0);
            self.index = 0;
            const auto* ext = static_cast<const clap_host_latency_t*>(self.host->get_extension(self.host, CLAP_EXT_LATENCY));
            if (ext) ext->changed(self.host);
            return true;
        },
        [](const clap_plugin_t*) {},
        [](const clap_plugin_t*) { return true; },
        [](const clap_plugin_t*) {},
        [](const clap_plugin_t* plugin) { get(plugin).ring.fill(0); get(plugin).index = 0; },
        [](const clap_plugin_t* plugin, const clap_process_t* process) -> clap_process_status {
            auto& self = get(plugin);
            const auto* input = process->audio_inputs[0].data32[0];
            auto* output = process->audio_outputs[0].data32[0];
            for (uint32_t i = 0; i < process->frames_count; ++i) {
                output[i] = self.ring[self.index];
                self.ring[self.index] = input[i];
                self.index = (self.index + 1) % self.latency;
            }
            if (self.activation == 1) self.host->request_restart(self.host);
            return CLAP_PROCESS_CONTINUE;
        },
        [](const clap_plugin_t*, const char* id) -> const void* {
            return std::strcmp(id, CLAP_EXT_LATENCY) == 0 ? &latencyExtension : nullptr;
        },
        [](const clap_plugin_t*) {},
    };
    return &instance->api;
}

const clap_plugin_factory_t factory{
    [](const clap_plugin_factory_t*) -> uint32_t { return 1; },
    [](const clap_plugin_factory_t*, uint32_t i) -> const clap_plugin_descriptor_t* {
        return i == 0 ? &descriptor : nullptr;
    },
    [](const clap_plugin_factory_t*, const clap_host_t* host, const char* id) -> const clap_plugin_t* {
        return std::strcmp(id, descriptor.id) == 0 ? create(host) : nullptr;
    },
};
}

extern "C" CLAP_EXPORT const clap_plugin_entry_t clap_entry{
    CLAP_VERSION,
    [](const char*) { return true; },
    [] {},
    [](const char* id) -> const void* {
        return std::strcmp(id, CLAP_PLUGIN_FACTORY_ID) == 0 ? &factory : nullptr;
    },
};
