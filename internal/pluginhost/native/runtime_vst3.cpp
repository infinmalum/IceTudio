#include "runtime.h"
#include "vst3_editor.h"

#include "pluginterfaces/vst/ivstaudioprocessor.h"
#include "pluginterfaces/vst/ivstmidicontrollers.h"
#include "pluginterfaces/vst/ivsteditcontroller.h"
#include "pluginterfaces/vst/ivstprocesscontext.h"
#include "public.sdk/source/common/memorystream.h"
#include "public.sdk/source/vst/hosting/eventlist.h"
#include "public.sdk/source/vst/hosting/hostclasses.h"
#include "public.sdk/source/vst/hosting/parameterchanges.h"
#include "public.sdk/source/vst/hosting/plugprovider.h"
#include "public.sdk/source/vst/hosting/processdata.h"
#include "public.sdk/source/vst/utility/stringconvert.h"
#include <atomic>
#include <set>
#include <map>
#include <mutex>

namespace {
using namespace Steinberg;
using namespace Steinberg::Vst;

struct Handler final : U::Implements<U::Directly<IComponentHandler>> {
    std::atomic_bool changed{false};
    bool editor = false;
    std::mutex mutex;
    std::map<ParamID, ParamValue> pending;

    tresult PLUGIN_API beginEdit(ParamID) override { return editor ? kResultOk : kNotImplemented; }
    tresult PLUGIN_API performEdit(ParamID id, ParamValue value) override {
        if (!editor || !std::isfinite(value) || value < 0 || value > 1) return kResultFalse;
        std::lock_guard lock(mutex);
        if (pending.size() == ICE_PLUGIN_PARAMS && !pending.contains(id)) return kResultFalse;
        pending[id] = value;
        return kResultOk;
    }
    tresult PLUGIN_API endEdit(ParamID) override { return editor ? kResultOk : kNotImplemented; }
    tresult PLUGIN_API restartComponent(int32 flags) override {
        if (flags & (kLatencyChanged | kIoChanged | kReloadComponent)) changed = true;
        return kResultOk;
    }
};

int mainBus(IComponent* c, BusDirection direction) {
    int main = -1;
    const int count = c->getBusCount(kAudio, direction);
    check(count >= 0 && count <= 8, "unsupported VST3 audio bus count");
    for (int i = 0; i < count; ++i) {
        BusInfo info{};
        check(c->getBusInfo(kAudio, direction, i, info) == kResultOk &&
              (info.channelCount == 1 || info.channelCount == 2), "VST3 host requires mono or stereo buses");
        if (info.busType == kMain && main == -1) main = i;
        check(c->activateBus(kAudio, direction, i, true) == kResultOk, "VST3 bus activation failed");
    }
    return main;
}

void state(IComponent* c, IEditController* controller, ice_plugin_wire& wire, bool restore) {
    if (restore && wire.state_size) {
        MemoryStream input(reinterpret_cast<char*>(wire.state), wire.state_size);
        check(c->setState(&input) == kResultOk, "VST3 state restore failed");
        if (controller) {
            input.seek(0, IBStream::kIBSeekSet, nullptr);
            auto result = controller->setComponentState(&input);
            check(result == kResultOk || result == kNotImplemented, "VST3 controller state restore failed");
        }
    }
    MemoryStream output;
    auto status = c->getState(&output);
    check((status == kResultOk || (status == kNotImplemented && output.getSize() == 0)) && output.getSize() >= 0 &&
          output.getSize() <= ICE_PLUGIN_STATE, "VST3 state capture failed");
    wire.state_size = output.getSize();
    if (wire.state_size) std::memcpy(wire.state, output.getData(), wire.state_size);
}
std::set<ParamID> describe(IEditController* controller, ice_plugin_wire& wire) {
    std::set<ParamID> ids;
    wire.param_count = 0;
    std::memset(wire.params, 0, sizeof(wire.params));
    for (int32 i = 0, count = controller ? std::min(controller->getParameterCount(), 4096) : 0; i < count; ++i) {
        ParameterInfo info{};
        if (controller->getParameterInfo(i, info) == kResultOk && !(info.flags & ParameterInfo::kIsReadOnly)) {
            ids.insert(info.id);
            if (info.flags & ParameterInfo::kCanAutomate)
                parameter(wire, info.id, controller->getParamNormalized(info.id), StringConvert::convert(info.title, 128));
        }
    }
    return ids;
}

template<typename Process>
void edit(IComponent* c, IEditController* controller, Handler& handler, const std::set<ParamID>& ids,
          ice_plugin_wire& wire, Process process) {
    check(controller, "VST3 editor controller unavailable");
    ice_plugin_input in{};
    ice_plugin_output out{};
    in.frames = 64;
    auto apply = [&](const std::map<ParamID, ParamValue>& values) {
        in.params = 0;
        for (const auto& [id, value] : values) {
            if (!ids.contains(id)) continue;
            controller->setParamNormalized(id, value);
            in.ids[in.params] = id;
            in.values[in.params++] = value;
        }
        if (in.params) process(in, out);
    };
    std::map<ParamID, ParamValue> initial;
    for (uint32_t i = 0; i < wire.in[0].params && i < ICE_PLUGIN_PARAMS; ++i)
        initial[wire.in[0].ids[i]] = wire.in[0].values[i];
    apply(initial);
    EditorWindow window;
    auto frame = owned(new X11PlugFrame(window));
    auto view = owned(controller->createView(ViewType::kEditor));
    check(view && view->isPlatformTypeSupported(kPlatformTypeX11EmbedWindowID) == kResultOk &&
          view->setFrame(frame) == kResultOk, "no embedded VST3 X11 editor");
    ViewRect size{};
    check(view->getSize(&size) == kResultOk && window.resize(size.getWidth(), size.getHeight()), "invalid VST3 editor dimensions");
    check(view->attached(reinterpret_cast<void*>(window.id()), kPlatformTypeX11EmbedWindowID) == kResultOk, "VST3 editor attach failed");
    const auto pump = [&] {
        frame->loop.pump();
        std::map<ParamID, ParamValue> pending;
        {
            std::lock_guard lock(handler.mutex);
            pending.swap(handler.pending);
        }
        apply(pending);
        return !window.closed();
    };
    editUntilSave(wire, pump, [&] {
        pump();
        state(c, controller, wire, false);
        describe(controller, wire);
        view->removed();
        view->setFrame(nullptr);
        view.reset();
    });
}

}

void runVST3(const char* path, const char* id, bool instrument, bool offline, bool editor, ice_plugin_wire& wire) {
    using VST3::Hosting::Module;
    auto host = owned(new HostApplication);
    std::string error;
    auto module = Module::create(path, error);
    check(bool(module), "VST3 module load failed");
    const auto& factory = module->getFactory();
    factory.setHostContext(host);
    auto classes = factory.classInfos();
    auto found = std::find_if(classes.begin(), classes.end(), [&](const auto& info) {
        return info.category() == kVstAudioEffectClass && info.ID().toString() == id;
    });
    check(found != classes.end(), "VST3 class ID no longer exists");
    PluginContextFactory::instance().setPluginContext(host);
    PlugProvider provider(factory, *found);
    check(provider.initialize(), "VST3 instance initialization failed");
    auto c = provider.getComponentPtr();
    auto controller = provider.getControllerPtr();
    FUnknownPtr<IAudioProcessor> processor(c);
    check(processor && processor->canProcessSampleSize(kSample32) == kResultOk, "VST3 float processor unavailable");
    auto handler = owned(new Handler);
    handler->editor = editor;
    if (controller) controller->setComponentHandler(handler);
    state(c, controller, wire, true);
    const auto ids = describe(controller, wire);
    const int mainIn = mainBus(c, kInput), mainOut = mainBus(c, kOutput);
    check(mainOut >= 0 && (instrument || mainIn >= 0), "VST3 main audio bus unavailable");
    if (instrument) {
        check(c->getBusCount(kEvent, kInput) > 0 && c->activateBus(kEvent, kInput, 0, true) == kResultOk,
              "VST3 note input unavailable");
    }
    ProcessSetup setup{offline ? kOffline : kRealtime, kSample32, ICE_PLUGIN_FRAMES, 48000};
    check(processor->setupProcessing(setup) == kResultOk && c->setActive(true) == kResultOk, "VST3 activation failed");
    wire.latency = processor->getLatencySamples();
    handler->changed = false;
    HostProcessData data;
    check(data.prepare(*c, ICE_PLUGIN_FRAMES, kSample32), "VST3 buffer setup failed");
    data.processMode = setup.processMode;
    EventList events(ICE_PLUGIN_EVENTS);
    ParameterChanges changes(ICE_PLUGIN_PARAMS + ICE_PLUGIN_EVENTS);
    FUnknownPtr<IMidiMapping> mapping(controller);
    data.inputEvents = &events;
    data.inputParameterChanges = &changes;
    ProcessContext context{};
    context.state = ProcessContext::kPlaying;
    context.sampleRate = 48000;
    data.processContext = &context;
    check(processor->setProcessing(true) == kResultOk, "VST3 start failed");

    const auto process = [&](const ice_plugin_input& in, ice_plugin_output& out) {
        if (in.reset) {
            processor->setProcessing(false);
            check(c->setActive(false) == kResultOk && c->setActive(true) == kResultOk &&
                  processor->setProcessing(true) == kResultOk, "VST3 reset failed");
        }
        data.numSamples = in.frames;
        events.clear();
        changes.clearQueue();
        const auto point = [&](ParamID id, int32 offset, double value) {
            int32 index = 0;
            auto* queue = changes.addParameterData(id, index);
            check(queue && queue->addPoint(offset, value, index) == kResultOk, "VST3 parameter queue rejected event");
        };
        for (uint32_t i = 0; i < in.params; ++i) {
            check(ids.contains(in.ids[i]), "VST3 parameter ID no longer exists");
            point(in.ids[i], 0, in.values[i]);
        }
        for (uint32_t i = 0; i < in.events; ++i) {
            const auto& v = in.event[i];
            Event e{};
            e.sampleOffset = v.offset;
            if (v.kind == 1) {
                e.type = Event::kNoteOffEvent;
                e.noteOff = {int16(v.channel), int16(v.a), 0.f, int32(v.token), 0.f};
            } else if (v.kind == 2) {
                e.type = Event::kNoteOnEvent;
                e.noteOn = {int16(v.channel), int16(v.a), 0.f, v.b/127.f, 0, int32(v.token)};
            } else if (v.kind == 7) {
                e.type = Event::kPolyPressureEvent;
                e.polyPressure = {int16(v.channel), int16(v.a), v.b/127.f, -1};
            } else {
                ParamID id;
                int controller = v.kind == 3 ? v.a : v.kind == 5 ? kPitchBend : v.kind == 6 ? kAfterTouch : -1;
                if (controller >= 0 && mapping &&
                    mapping->getMidiControllerAssignment(0, v.channel, controller, id) == kResultOk)
                    point(id, v.offset, v.kind == 3 ? v.b/127.0 : v.kind == 5 ? v.a/16383.0 : v.a/127.0);
                continue;
            }
            check(events.addEvent(e) == kResultOk, "VST3 event queue rejected event");
        }
        for (int i = 0; i < data.numInputs; ++i) {
            auto& bus = data.inputs[i];
            const bool used = !instrument && i == mainIn;
            bus.silenceFlags = used ? 0 : (1u << bus.numChannels)-1;
            for (int j = 0; j < bus.numChannels; ++j) {
                if (used) std::copy_n(in.audio[j], in.frames, bus.channelBuffers32[j]);
                else std::fill_n(bus.channelBuffers32[j], in.frames, 0.f);
            }
        }
        for (int i = 0; i < data.numOutputs; ++i) {
            auto& bus = data.outputs[i];
            bus.silenceFlags = 0;
            for (int j = 0; j < bus.numChannels; ++j) std::fill_n(bus.channelBuffers32[j], in.frames, 0.f);
        }
        check(processor->process(data) == kResultOk, "VST3 process failed");
        context.projectTimeSamples += in.frames;
        auto& bus = data.outputs[mainOut];
        for (int ch = 0; ch < 2; ++ch)
            std::copy_n(bus.channelBuffers32[std::min(ch, bus.numChannels-1)], in.frames, out.audio[ch]);
    };
    if (editor) {
        edit(c, controller, *handler, ids, wire, process);
        return;
    }
    if (wire.in[0].params) {
        ice_plugin_input initial = wire.in[0];
        ice_plugin_output discard{};
        initial.frames = ICE_PLUGIN_FRAMES;
        for (uint32_t i = 0; i < initial.params && i < ICE_PLUGIN_PARAMS; ++i)
            if (controller && ids.contains(initial.ids[i])) controller->setParamNormalized(initial.ids[i], initial.values[i]);
        process(initial, discard);
    }
    if (handler->changed || processor->getLatencySamples() != wire.latency) {
        processor->setProcessing(false);
        check(c->setActive(false) == kResultOk && c->setActive(true) == kResultOk &&
              processor->setProcessing(true) == kResultOk, "VST3 startup reactivation failed");
        wire.latency = processor->getLatencySamples();
        handler->changed = false;
    }
    context.projectTimeSamples = 0;
    processBlocks(wire, [&](const ice_plugin_input& in, ice_plugin_output& out) {
        process(in, out);
        if (handler->changed || processor->getLatencySamples() != wire.latency) requestGraphChange(wire);
    });
}
