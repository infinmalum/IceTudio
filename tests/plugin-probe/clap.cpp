#include "probe.h"
#include "editor.h"
#include "transport.h"

#include <clap/clap.h>
#include <dlfcn.h>

#include <algorithm>
#include <atomic>
#include <cstring>
#include <iomanip>
#include <memory>
#include <optional>
#include <thread>
#include <poll.h>

namespace {
struct Host {
    const std::thread::id mainThread = std::this_thread::get_id();
    bool audio = false;
    std::atomic_bool callback{false};
    std::atomic_bool restart{false};
    std::atomic_bool latencyChanged{false};
    bool editor = false;
    EventLoop loop;
    const clap_plugin_t* plugin = nullptr;

    static Host& get(const clap_host_t* host) {
        return *static_cast<Host*>(host->host_data);
    }

    clap_host_t api{
        CLAP_VERSION, this, "IceTudio E6 probe", "IceTudio", "", "1",
        [](const clap_host_t* host, const char* id) -> const void* {
            static const clap_host_thread_check_t threadCheck{
                [](const clap_host_t* h) { return std::this_thread::get_id() == get(h).mainThread && !get(h).audio; },
                [](const clap_host_t* h) { return std::this_thread::get_id() == get(h).mainThread && get(h).audio; },
            };
            static const clap_host_latency_t latency{
                [](const clap_host_t* h) { get(h).latencyChanged = true; },
            };
            if (std::strcmp(id, CLAP_EXT_THREAD_CHECK) == 0) return &threadCheck;
            if (std::strcmp(id, CLAP_EXT_LATENCY) == 0) return &latency;
            if (get(host).editor && std::strcmp(id, CLAP_EXT_POSIX_FD_SUPPORT) == 0) return &fileDescriptors;
            if (get(host).editor && std::strcmp(id, CLAP_EXT_TIMER_SUPPORT) == 0) return &timers;
            return nullptr;
        },
        [](const clap_host_t* h) { get(h).restart = true; },
        [](const clap_host_t*) {},
        [](const clap_host_t* h) { get(h).callback = true; },
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

template<class T>
const T* extension(const clap_plugin_t* plugin, const char* name) {
    return static_cast<const T*>(plugin->get_extension(plugin, name));
}

void verifyEditor(const clap_plugin_t* plugin, const clap_plugin_gui_t* gui, Host& host) {
    require(gui && gui->is_api_supported(plugin, CLAP_WINDOW_API_X11, false), "no embedded CLAP X11 editor");
    EditorWindow window;
    const auto dispatch = [&] {
        if (host.callback.exchange(false)) plugin->on_main_thread(plugin);
    };
    for (int attempt = 0; attempt < 2; ++attempt) {
        require(gui->create(plugin, CLAP_WINDOW_API_X11, false), "CLAP editor creation failed");
        uint32_t width = 0, height = 0;
        require(gui->get_size(plugin, &width, &height) && window.resize(width, height), "invalid CLAP editor dimensions");
        clap_window_t parent{};
        parent.api = CLAP_WINDOW_API_X11;
        parent.x11 = window.id();
        require(gui->set_parent(plugin, &parent) && gui->show(plugin), "CLAP editor attach failed");
        window.verify(host.loop, true, dispatch);
        const bool hidden = gui->hide(plugin);
        gui->destroy(plugin);
        std::cout << "editor hide_result=" << hidden << '\n';
        window.verify(host.loop, false, dispatch);
    }
}

void verifyLatency(const clap_plugin_t* plugin, Host& host) {
    const auto* latency = extension<clap_plugin_latency_t>(plugin, CLAP_EXT_LATENCY);
    require(latency, "fixture did not expose latency");
    for (const unsigned expected : {64, 192}) {
        host.latencyChanged = false;
        host.restart = false;
        require(plugin->activate(plugin, Rate, Frames, Frames), "fixture activation failed");
        const auto reported = latency->get(plugin);
        require(host.latencyChanged && reported == expected, "fixture did not report the new latency");
        Samples input{}, output{};
        input[0] = 1.f;
        float* inData = input.data();
        float* outData = output.data();
        const clap_audio_buffer_t in{&inData, nullptr, 1, 0, 0};
        clap_audio_buffer_t out{&outData, nullptr, 1, 0, 0};
        const clap_process_t process{0, Frames, nullptr, &in, &out, 1, 1, nullptr, nullptr};
        host.audio = true;
        require(plugin->start_processing(plugin), "fixture start failed");
        require(plugin->process(plugin, &process) == CLAP_PROCESS_CONTINUE, "fixture process failed");
        plugin->stop_processing(plugin);
        host.audio = false;
        plugin->deactivate(plugin);
        for (unsigned i = 0; i < output.size(); ++i) {
            require(output[i] == (i == expected ? 1.f : 0.f), "fixture impulse disagrees with reported latency");
        }
        require(expected != 64 || host.restart, "fixture failed to request host restart");
        std::cout << "latency reported=" << reported << " impulse_frame=" << expected
                  << " restart=" << host.restart.load() << '\n';
    }
}

void stateIO(const Request& request, const clap_plugin_t* plugin) {
    const auto* state = extension<clap_plugin_state_t>(plugin, CLAP_EXT_STATE);
    require(state, "CLAP plugin has no state extension");
    if (request.mode == Mode::Save) {
        State data;
        const clap_ostream_t stream{
            &data,
            [](const clap_ostream_t* s, const void* buffer, uint64_t size) -> int64_t {
                auto& bytes = *static_cast<State*>(s->ctx);
                if (size > MaxState - bytes.size()) return -1;
                const auto* begin = static_cast<const char*>(buffer);
                bytes.insert(bytes.end(), begin, begin + size);
                return size;
            },
        };
        require(state->save(plugin, &stream), "CLAP state save failed");
        writeState(request.statePath, data);
    } else {
        struct Reader {
            State data;
            std::size_t offset = 0;
        } reader{readState(request.statePath)};
        const clap_istream_t stream{
            &reader,
            [](const clap_istream_t* s, void* buffer, uint64_t size) -> int64_t {
                auto& r = *static_cast<Reader*>(s->ctx);
                size = std::min<uint64_t>(size, r.data.size() - r.offset);
                std::memcpy(buffer, r.data.data() + r.offset, size);
                r.offset += size;
                return size;
            },
        };
        require(state->load(plugin, &stream), "CLAP state restore failed");
        std::cout << "restored bytes=" << reader.data.size() << '\n';
    }
}

struct Buses {
    std::array<std::array<Samples, 2>, 8> samples{};
    std::array<std::array<float*, 2>, 8> channels{};
    std::array<clap_audio_buffer_t, 8> buffers{};
    uint32_t count;

    Buses(const clap_plugin_t* plugin, const clap_plugin_audio_ports_t* ports, bool input)
        : count(ports->count(plugin, input)) {
        require(count <= buffers.size() && (input || count > 0), "unsupported CLAP bus count");
        for (uint32_t i = 0; i < count; ++i) {
            clap_audio_port_info_t port{};
            require(ports->get(plugin, i, input, &port), "CLAP port metadata failed");
            require(port.channel_count == 1 || port.channel_count == 2, "probe requires mono or stereo");
            channels[i] = {samples[i][0].data(), samples[i][1].data()};
            buffers[i] = {channels[i].data(), nullptr, port.channel_count, 0, 0};
        }
    }
};

bool isInstrument(const clap_plugin_t* plugin) {
    for (auto feature = plugin->desc->features; feature && *feature; ++feature)
        if (std::strcmp(*feature, CLAP_PLUGIN_FEATURE_INSTRUMENT) == 0) return true;
    return false;
}

clap_event_param_value_t findParameter(const Request& request, const clap_plugin_t* plugin,
                                       const clap_plugin_params_t* params) {
    require(params, "missing CLAP parameter extension");
    for (uint32_t i = 0; i < params->count(plugin); ++i) {
        clap_param_info_t info{};
        if (!params->get_info(plugin, i, &info) || request.parameter != info.name) continue;
        require(info.flags & CLAP_PARAM_IS_AUTOMATABLE, "CLAP parameter is not automatable");
        clap_event_param_value_t event{};
        event.header = {sizeof(event), 0, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_PARAM_VALUE, 0};
        event.param_id = info.id;
        event.cookie = info.cookie;
        event.note_id = event.port_index = event.channel = event.key = -1;
        event.value = info.min_value + request.value * (info.max_value - info.min_value);
        return event;
    }
    throw std::runtime_error("CLAP parameter not found: " + request.parameter);
}

void render(const clap_plugin_t* plugin, Host& host, const Request& request, clap_event_param_value_t* parameter) {
    const auto* ports = extension<clap_plugin_audio_ports_t>(plugin, CLAP_EXT_AUDIO_PORTS);
    require(ports, "missing CLAP audio ports");
    Buses inputs(plugin, ports, true), outputs(plugin, ports, false);
    const bool instrument = isInstrument(plugin);
    const auto* notePorts = extension<clap_plugin_note_ports_t>(plugin, CLAP_EXT_NOTE_PORTS);
    clap_note_port_info_t port{};
    bool useClapNotes = false;
    if (instrument) {
        require(notePorts && notePorts->count(plugin, true) && notePorts->get(plugin, 0, true, &port), "no CLAP note input");
        useClapNotes = port.supported_dialects & CLAP_NOTE_DIALECT_CLAP;
        require(useClapNotes || (port.supported_dialects & CLAP_NOTE_DIALECT_MIDI), "no supported note dialect");
    }
    require(plugin->activate(plugin, Rate, Frames, Frames), "CLAP activation failed");
    const auto* latency = extension<clap_plugin_latency_t>(plugin, CLAP_EXT_LATENCY);
    std::cout << "buses in=" << inputs.count << " out=" << outputs.count
              << " latency=" << (latency ? latency->get(plugin) : 0) << '\n';
    host.audio = true;
    require(plugin->start_processing(plugin), "CLAP start processing failed");

    clap_event_note_t note{};
    note.header = {sizeof(note), 0, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_NOTE_ON, 0};
    note.note_id = -1;
    note.key = 60;
    note.velocity = 0.8;
    clap_event_midi_t midi{{sizeof(midi), 0, CLAP_CORE_EVENT_SPACE_ID, CLAP_EVENT_MIDI, 0}, 0, {0x90, 60, 102}};
    clap_input_events_t events{
        nullptr,
        [](const clap_input_events_t* list) -> uint32_t { return list->ctx ? 1 : 0; },
        [](const clap_input_events_t* list, uint32_t i) -> const clap_event_header_t* {
            return i == 0 ? static_cast<const clap_event_header_t*>(list->ctx) : nullptr;
        },
    };
    const clap_output_events_t discard{nullptr, [](const clap_output_events_t*, const clap_event_header_t*) { return false; }};
    clap_process_t process{0, Frames, nullptr, inputs.buffers.data(), outputs.buffers.data(),
                           inputs.count, outputs.count, &events, &discard};

    std::optional<Transport> transport;
    if (transportMode(request.mode)) transport.emplace(TransportWorkerFD, false);
    Measurement measurement;
    for (int block = 0; block < (transport ? TransportBlocks : Blocks); ++block) {
        AudioBlock packet;
        if (transport) packet = transport->next(request.mode == Mode::TransportCrash);
        else if (block == 1) packet.note = AudioBlock::NoteOn;
        else if (block == 33) packet.note = AudioBlock::NoteOff;
        process.steady_time = block * Frames;
        events.ctx = nullptr;
        if (!instrument && inputs.count > 0) {
            if (transport) inputs.samples[0] = {packet.left, packet.right};
            else for (int i = 0; i < Frames; ++i)
                inputs.samples[0][0][i] = inputs.samples[0][1][i] = 0.2f * std::sin(float(block * Frames + i) * 0.0576f);
        } else if (instrument && packet.note != AudioBlock::Silent) {
            const bool on = packet.note == AudioBlock::NoteOn;
            note.header.type = on ? CLAP_EVENT_NOTE_ON : CLAP_EVENT_NOTE_OFF;
            midi.data[0] = on ? 0x90 : 0x80;
            events.ctx = useClapNotes ? &note.header : &midi.header;
        }
        if (block == 0 && parameter) events.ctx = &parameter->header;
        for (uint32_t bus = 0; bus < outputs.count; ++bus) {
            outputs.samples[bus][0].fill(0);
            outputs.samples[bus][1].fill(0);
        }
        require(plugin->process(plugin, &process) != CLAP_PROCESS_ERROR, "CLAP processing failed");
        if (transport) {
            packet.left = outputs.samples[0][0];
            packet.right = outputs.samples[0][outputs.buffers[0].channel_count - 1];
            transport->publish(packet);
        }
        for (uint32_t bus = 0; bus < outputs.count; ++bus)
            for (uint32_t channel = 0; channel < outputs.buffers[bus].channel_count; ++channel)
                measurement.observe(outputs.samples[bus][channel]);
        host.audio = false;
        if (host.callback.exchange(false)) {
            plugin->on_main_thread(plugin);
        }
        if (block == 0) {
            std::cout << "processed first block" << std::endl;
            injectFault(request.mode);
        }
        host.audio = true;
    }
    plugin->stop_processing(plugin);
    host.audio = false;
    plugin->deactivate(plugin);
    measurement.report();
    std::cout << "restart=" << host.restart.load() << " latency_changed=" << host.latencyChanged.load() << '\n';
}
}

void probeClap(const Request& request) {
    auto* library = dlopen(request.path.c_str(), RTLD_NOW | RTLD_LOCAL);
    if (!library) throw std::runtime_error(dlerror());
    const auto* entry = static_cast<const clap_plugin_entry_t*>(dlsym(library, "clap_entry"));
    require(entry && clap_version_is_compatible(entry->clap_version), "incompatible CLAP entry");
    require(entry->init(request.path.c_str()), "CLAP module initialization failed");
    const auto* factory = static_cast<const clap_plugin_factory_t*>(entry->get_factory(CLAP_PLUGIN_FACTORY_ID));
    require(factory, "missing CLAP factory");
    if (request.mode == Mode::Scan) {
        const auto count = factory->get_plugin_count(factory);
        require(count > 0 && count <= 1024, "invalid CLAP descriptor count");
        for (uint32_t i = 0; i < count; ++i) {
            const auto* desc = factory->get_plugin_descriptor(factory, i);
            require(desc && desc->id && desc->name, "invalid CLAP descriptor");
            std::cout << "plugin " << std::quoted(desc->id) << ' ' << std::quoted(desc->name) << '\n';
        }
    } else {
        Host host;
        host.editor = request.mode == Mode::Editor;
        const auto* plugin = factory->create_plugin(factory, &host.api, request.id.c_str());
        host.plugin = plugin;
        require(plugin && plugin->init(plugin), "CLAP instance initialization failed");
        const auto* params = extension<clap_plugin_params_t>(plugin, CLAP_EXT_PARAMS);
        const auto* gui = extension<clap_plugin_gui_t>(plugin, CLAP_EXT_GUI);
        std::cout << "params=" << (params ? params->count(plugin) : 0)
                  << " gui_x11_embed=" << bool(gui && gui->is_api_supported(plugin, CLAP_WINDOW_API_X11, false))
                  << " gui_x11_float=" << bool(gui && gui->is_api_supported(plugin, CLAP_WINDOW_API_X11, true))
                  << " gui_wayland_embed=" << bool(gui && gui->is_api_supported(plugin, CLAP_WINDOW_API_WAYLAND, false))
                  << " gui_wayland_float=" << bool(gui && gui->is_api_supported(plugin, CLAP_WINDOW_API_WAYLAND, true)) << '\n';
        if (request.mode == Mode::Editor || request.mode == Mode::Latency) {
            if (request.mode == Mode::Editor) verifyEditor(plugin, gui, host);
            else verifyLatency(plugin, host);
            plugin->destroy(plugin);
            entry->deinit();
            dlclose(library);
            return;
        }
        if (request.mode != Mode::Save && !transportMode(request.mode)) stateIO(request, plugin);
        clap_event_param_value_t event{};
        const bool checkParameter = !request.parameter.empty();
        if (checkParameter) {
            event = findParameter(request, plugin, params);
            double actual = 0;
            require(params->get_value(plugin, event.param_id, &actual), "CLAP parameter read failed");
            if (request.mode == Mode::Save)
                require(std::abs(actual - event.value) > 1e-4, "CLAP test requires a non-default value");
        }
        render(plugin, host, request, checkParameter && request.mode == Mode::Save ? &event : nullptr);
        if (checkParameter) {
            double actual = 0;
            require(params->get_value(plugin, event.param_id, &actual) && std::abs(actual - event.value) < 1e-4,
                    "CLAP parameter did not match the test value");
            std::cout << "parameter " << request.parameter << " id=" << event.param_id << " value=" << actual << '\n';
        }
        if (request.mode == Mode::Save) stateIO(request, plugin);
        plugin->destroy(plugin);
    }
    entry->deinit();
    dlclose(library);
}
