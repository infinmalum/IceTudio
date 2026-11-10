#include "runtime.h"
#include "editor.h"

#include <clap/clap.h>
#include <dlfcn.h>
#include <array>
#include <atomic>
#include <map>
#include <memory>
#include <poll.h>
#include <thread>

namespace {
template<class T> const T* ext(const clap_plugin_t* p, const char* name) {
    return static_cast<const T*>(p->get_extension(p, name));
}

struct Host {
    const std::thread::id mainThread = std::this_thread::get_id();
    EventLoop loop;
    bool editor = false;
    const clap_plugin_t* plugin = nullptr;
    std::atomic_bool flush{false};
    bool audio = false;
    std::atomic_bool callback{false}, restart{false}, latency{false};
    static Host& get(const clap_host_t* h) { return *static_cast<Host*>(h->host_data); }
    clap_host_t api{
        CLAP_VERSION, this, "IceTudio", "IceTudio", "", "1",
        [](const clap_host_t* h, const char* id) -> const void* {
            static const clap_host_thread_check_t threads{
                [](const clap_host_t* h) { return get(h).mainThread == std::this_thread::get_id() && !get(h).audio; },
                [](const clap_host_t* h) { return get(h).mainThread == std::this_thread::get_id() && get(h).audio; }
            };
            static const clap_host_latency_t latency{[](const clap_host_t* h) { get(h).latency = true; }};
            static const clap_host_params_t parameters{
                [](const clap_host_t*, clap_param_rescan_flags) {},
                [](const clap_host_t*, clap_id, clap_param_clear_flags) {},
                [](const clap_host_t* h) { get(h).flush = true; }
            };
            if (!std::strcmp(id, CLAP_EXT_PARAMS)) return &parameters;
            if (get(h).editor && !std::strcmp(id, CLAP_EXT_POSIX_FD_SUPPORT)) return &fileDescriptors;
            if (get(h).editor && !std::strcmp(id, CLAP_EXT_TIMER_SUPPORT)) return &timers;
            if (!std::strcmp(id, CLAP_EXT_THREAD_CHECK)) return &threads;
            if (!std::strcmp(id, CLAP_EXT_LATENCY)) return &latency;
            return nullptr;
        },
        [](const clap_host_t* h) { get(h).restart = true; },
        [](const clap_host_t*) {},
        [](const clap_host_t* h) { get(h).callback = true; }
    };
    static short pollMask(clap_posix_fd_flags_t mask) {
        return (mask & CLAP_POSIX_FD_READ ? POLLIN : 0) | (mask & CLAP_POSIX_FD_WRITE ? POLLOUT : 0);
    }
    static inline const clap_host_posix_fd_support_t fileDescriptors{
        [](const clap_host_t* h, int fd, clap_posix_fd_flags_t mask) {
            auto& host = get(h);
            return host.loop.watch(fd, pollMask(mask), [&host, fd](short events) {
                const auto* plugin = host.plugin;
                if (const auto* ext = static_cast<const clap_plugin_posix_fd_support_t*>(
                        plugin->get_extension(plugin, CLAP_EXT_POSIX_FD_SUPPORT))) {
                    clap_posix_fd_flags_t flags = (events & POLLIN ? CLAP_POSIX_FD_READ : 0) |
                        (events & POLLOUT ? CLAP_POSIX_FD_WRITE : 0) |
                        (events & (POLLERR | POLLHUP | POLLNVAL) ? CLAP_POSIX_FD_ERROR : 0);
                    ext->on_fd(plugin, fd, flags);
                }
            });
        },
        [](const clap_host_t* h, int fd, clap_posix_fd_flags_t mask) { return get(h).loop.modify(fd, pollMask(mask)); },
        [](const clap_host_t* h, int fd) { return get(h).loop.unwatch(fd); },
    };
    static inline const clap_host_timer_support_t timers{
        [](const clap_host_t* h, uint32_t ms, clap_id* out) {
            auto& host = get(h);
            auto id = std::make_shared<unsigned>(0);
            *id = host.loop.timer(ms, [&host, id](short) {
                const auto* plugin = host.plugin;
                if (const auto* ext = static_cast<const clap_plugin_timer_support_t*>(
                        plugin->get_extension(plugin, CLAP_EXT_TIMER_SUPPORT)))
                    ext->on_timer(plugin, *id);
            });
            *out = *id;
            return *id != 0;
        },
        [](const clap_host_t* h, clap_id id) { return get(h).loop.untimer(id); },
    };
};

struct Buses {
    std::array<std::array<std::array<float, ICE_PLUGIN_FRAMES>, 2>, 8> samples{};
    std::array<std::array<float*, 2>, 8> channels{};
    std::array<clap_audio_buffer_t, 8> buffers{};
    uint32_t count, main = 0;

    Buses(const clap_plugin_t* p, const clap_plugin_audio_ports_t* ports, bool input)
        : count(ports->count(p, input)) {
        check(count <= buffers.size() && (input || count > 0), "unsupported CLAP port count");
        for (uint32_t i = 0; i < count; ++i) {
            clap_audio_port_info_t info{};
            check(ports->get(p, i, input, &info) && (info.channel_count == 1 || info.channel_count == 2),
                  "CLAP host requires mono or stereo ports");
            if (info.flags & CLAP_AUDIO_PORT_IS_MAIN) main = i;
            channels[i] = {samples[i][0].data(), samples[i][1].data()};
            buffers[i] = {channels[i].data(), nullptr, info.channel_count, 0, 0};
        }
    }
};

void state(const clap_plugin_t* p, ice_plugin_wire& wire, bool restore) {
    const auto* st = ext<clap_plugin_state_t>(p, CLAP_EXT_STATE);
    check(st || !wire.state_size, "CLAP state extension unavailable");
    if (!st) return;
    struct Reader { const ice_plugin_wire& wire; uint32_t offset = 0; } reader{wire};
    if (restore && wire.state_size) {
        const clap_istream_t input{&reader, [](const clap_istream_t* stream, void* dest, uint64_t count) -> int64_t {
            auto& r = *static_cast<Reader*>(stream->ctx);
            count = std::min<uint64_t>(count, r.wire.state_size - r.offset);
            std::memcpy(dest, r.wire.state + r.offset, count);
            r.offset += count;
            return count;
        }};
        check(st->load(p, &input), "CLAP state restore failed");
    }
    wire.state_size = 0;
    const clap_ostream_t output{&wire, [](const clap_ostream_t* stream, const void* src, uint64_t count) -> int64_t {
        auto& w = *static_cast<ice_plugin_wire*>(stream->ctx);
        if (count > ICE_PLUGIN_STATE - w.state_size) return -1;
        std::memcpy(w.state + w.state_size, src, count);
        w.state_size += count;
        return count;
    }};
    check(st->save(p, &output), "CLAP state capture failed");
}
using Parameters = std::map<uint32_t, clap_param_info_t>;

Parameters describe(const clap_plugin_t* p, ice_plugin_wire& wire) {
    const auto* params = ext<clap_plugin_params_t>(p, CLAP_EXT_PARAMS);
    Parameters parameters;
    wire.param_count = 0;
    std::memset(wire.params, 0, sizeof(wire.params));
    for (uint32_t i = 0, count = params ? std::min(params->count(p), 4096u) : 0; i < count; ++i) {
        clap_param_info_t info{};
        double value = 0;
        if (params->get_info(p, i, &info) && !(info.flags & CLAP_PARAM_IS_READONLY) &&
            (info.flags & CLAP_PARAM_IS_AUTOMATABLE) && info.max_value > info.min_value &&
            params->get_value(p, info.id, &value)) {
            parameter(wire, info.id, (value - info.min_value)/(info.max_value - info.min_value),
                      std::string(info.name, strnlen(info.name, sizeof(info.name))));
            parameters.emplace(info.id, info);
        }
    }
    return parameters;
}

clap_event_param_value_t paramEvent(const clap_param_info_t& info, float value) {
    clap_event_param_value_t event{};
    event.header = {sizeof(event), 0, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_PARAM_VALUE, 0};
    event.param_id = info.id;
    event.cookie = info.cookie;
    event.note_id = event.port_index = event.channel = event.key = -1;
    event.value = info.min_value + value*(info.max_value-info.min_value);
    if (info.flags & CLAP_PARAM_IS_STEPPED) event.value = std::round(event.value);
    return event;
}

void flushParams(const clap_plugin_t* p, const Parameters& parameters, const ice_plugin_input* overrides) {
    const auto* params = ext<clap_plugin_params_t>(p, CLAP_EXT_PARAMS);
    if (!params && !overrides) return;
    check(params, "CLAP parameters unavailable");
    std::vector<clap_event_param_value_t> queue;
    for (uint32_t i = 0; overrides && i < overrides->params && i < ICE_PLUGIN_PARAMS; ++i) {
        auto found = parameters.find(overrides->ids[i]);
        check(found != parameters.end(), "CLAP parameter no longer exists");
        queue.push_back(paramEvent(found->second, overrides->values[i]));
    }
    const clap_input_events_t events{&queue,
        [](const clap_input_events_t* e) { return uint32_t(static_cast<decltype(queue)*>(e->ctx)->size()); },
        [](const clap_input_events_t* e, uint32_t i) -> const clap_event_header_t* {
            return &(*static_cast<decltype(queue)*>(e->ctx))[i].header;
        }};
    const clap_output_events_t discard{nullptr, [](const clap_output_events_t*, const clap_event_header_t*) { return true; }};
    params->flush(p, &events, &discard);
}

void edit(const clap_plugin_t* p, Host& host, const Parameters& parameters, ice_plugin_wire& wire) {
    const auto* gui = ext<clap_plugin_gui_t>(p, CLAP_EXT_GUI);
    check(gui && gui->is_api_supported(p, CLAP_WINDOW_API_X11, false), "no embedded CLAP X11 editor");
    if (wire.in[0].params) flushParams(p, parameters, &wire.in[0]);
    EditorWindow window;
    check(gui->create(p, CLAP_WINDOW_API_X11, false), "CLAP editor creation failed");
    uint32_t w = 0, h = 0;
    check(gui->get_size(p, &w, &h) && window.resize(w, h), "invalid CLAP editor dimensions");
    clap_window_t parent{};
    parent.api = CLAP_WINDOW_API_X11;
    parent.x11 = window.id();
    check(gui->set_parent(p, &parent) && gui->show(p), "CLAP editor attach failed");
    const auto pump = [&] {
        host.loop.pump();
        if (host.callback.exchange(false)) p->on_main_thread(p);
        if (host.flush.exchange(false)) flushParams(p, parameters, nullptr);
        return !window.closed();
    };
    editUntilSave(wire, pump, [&] {
        pump();
        state(p, wire, false);
        describe(p, wire);
        gui->hide(p);
        gui->destroy(p);
    });
}

}

void runClap(const char* path, const char* id, bool instrument, bool, bool editor, ice_plugin_wire& wire) {
    auto* library = dlopen(path, RTLD_NOW | RTLD_LOCAL);
    check(library, "CLAP binary load failed");
    const auto* entry = static_cast<const clap_plugin_entry_t*>(dlsym(library, "clap_entry"));
    check(entry && clap_version_is_compatible(entry->clap_version) && entry->init(path), "CLAP module initialization failed");
    const auto* factory = static_cast<const clap_plugin_factory_t*>(entry->get_factory(CLAP_PLUGIN_FACTORY_ID));
    check(factory, "CLAP factory unavailable");
    Host host;
    host.editor = editor;
    const auto* p = factory->create_plugin(factory, &host.api, id);
    check(p && p->init(p), "CLAP instance initialization failed");
    host.plugin = p;
    state(p, wire, true);
    auto parameters = describe(p, wire);
    if (editor) {
        edit(p, host, parameters, wire);
        return;
    }
    if (wire.in[0].params) flushParams(p, parameters, &wire.in[0]);
    const auto* ports = ext<clap_plugin_audio_ports_t>(p, CLAP_EXT_AUDIO_PORTS);
    check(ports, "CLAP audio ports unavailable");
    Buses input(p, ports, true), output(p, ports, false);
    check(instrument || input.count, "CLAP effect has no input");
    clap_note_port_info_t notePort{};
    if (instrument) {
        const auto* notes = ext<clap_plugin_note_ports_t>(p, CLAP_EXT_NOTE_PORTS);
        check(notes && notes->count(p, true) && notes->get(p, 0, true, &notePort), "CLAP note input unavailable");
        check(notePort.supported_dialects & (CLAP_NOTE_DIALECT_CLAP | CLAP_NOTE_DIALECT_MIDI),
              "CLAP note input dialect unsupported");
    }
    const auto* latency = ext<clap_plugin_latency_t>(p, CLAP_EXT_LATENCY);
    const auto activate = [&] {
        check(p->activate(p, 48000, ICE_PLUGIN_FRAMES, ICE_PLUGIN_FRAMES), "CLAP activation failed");
        wire.latency = latency ? latency->get(p) : 0;
        host.restart = host.latency = false;
        host.audio = true;
        check(p->start_processing(p), "CLAP processing initialization failed");
    };
    activate();
    std::array<clap_event_note_t, ICE_PLUGIN_EVENTS> notes{};
    std::array<clap_event_midi_t, ICE_PLUGIN_EVENTS> midi{};
    std::array<clap_event_param_value_t, ICE_PLUGIN_PARAMS> automation{};
    std::vector<const clap_event_header_t*> list;
    list.reserve(ICE_PLUGIN_EVENTS + ICE_PLUGIN_PARAMS);
    const clap_input_events_t events{&list,
        [](const clap_input_events_t* e) { return static_cast<uint32_t>(static_cast<decltype(list)*>(e->ctx)->size()); },
        [](const clap_input_events_t* e, uint32_t i) { return (*static_cast<decltype(list)*>(e->ctx))[i]; }};
    const clap_output_events_t discard{nullptr, [](const clap_output_events_t*, const clap_event_header_t*) { return false; }};
    int64_t frame = 0;
    const auto process = [&](const ice_plugin_input& in, ice_plugin_output& out) {
        if (in.reset) p->reset(p);
        list.clear();
        for (uint32_t i = 0; i < in.params; ++i) {
            auto found = parameters.find(in.ids[i]);
            check(found != parameters.end(), "CLAP parameter ID no longer exists");
            auto& e = automation[i];
            e = paramEvent(found->second, in.values[i]);
            list.push_back(&e.header);
        }
        for (uint32_t i = 0; i < in.events; ++i) {
            const auto& v = in.event[i];
            if ((v.kind == 1 || v.kind == 2) && (notePort.supported_dialects & CLAP_NOTE_DIALECT_CLAP)) {
                auto& e = notes[i];
                e.header = {sizeof(e), v.offset, CLAP_CORE_EVENT_SPACE_ID,
                            uint16_t(v.kind == 2 ? CLAP_EVENT_NOTE_ON : CLAP_EVENT_NOTE_OFF), 0};
                e.note_id = v.token;
                e.port_index = 0;
                e.channel = v.channel;
                e.key = v.a;
                e.velocity = v.b/127.0;
                list.push_back(&e.header);
            } else if (notePort.supported_dialects & CLAP_NOTE_DIALECT_MIDI) {
                static const uint8_t status[] = {0, 0x80, 0x90, 0xB0, 0xC0, 0xE0, 0xD0, 0xA0};
                if (v.kind >= sizeof(status) || !v.kind) continue;
                auto& e = midi[i];
                e.header = {sizeof(e), v.offset, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_MIDI, 0};
                e.port_index = 0;
                e.data[0] = status[v.kind] | v.channel;
                e.data[1] = v.a & 127;
                e.data[2] = v.kind == 5 ? v.a >> 7 : (v.b & 127);
                list.push_back(&e.header);
            }
        }
        if (!instrument)
            for (uint32_t ch = 0; ch < input.buffers[input.main].channel_count; ++ch)
                std::copy_n(in.audio[ch], in.frames, input.channels[input.main][ch]);
        for (auto& bus : output.samples)
            for (auto& ch : bus) std::fill_n(ch.begin(), in.frames, 0.f);
        clap_process_t process{frame, in.frames, nullptr, input.buffers.data(), output.buffers.data(),
                               input.count, output.count, &events, &discard};
        check(p->process(p, &process) != CLAP_PROCESS_ERROR, "CLAP process failed");
        frame += in.frames;
        host.audio = false;
        if (host.callback.exchange(false)) p->on_main_thread(p);
        host.audio = true;

        for (uint32_t ch = 0; ch < 2; ++ch)
            std::copy_n(output.channels[output.main][std::min(ch, output.buffers[output.main].channel_count - 1)], in.frames, out.audio[ch]);
    };
    // Give the plugin one silent block to settle latency notifications
    // before publishing the immutable graph latency.
    ice_plugin_input prime{};
    ice_plugin_output discardPrime{};
    prime.frames = ICE_PLUGIN_FRAMES;
    process(prime, discardPrime);
    if (host.latency || host.restart) {
        p->stop_processing(p);
        host.audio = false;
        p->deactivate(p);
        activate();
    }
    p->reset(p);
    frame = 0;
    processBlocks(wire, [&](const ice_plugin_input& in, ice_plugin_output& out) {
        process(in, out);
        if (host.latency || host.restart) requestGraphChange(wire);
    });
}
