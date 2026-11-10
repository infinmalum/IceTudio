#include "probe.h"
#include "editor.h"
#include "vst3_editor.h"
#include "transport.h"

#include "pluginterfaces/gui/iplugview.h"
#include "pluginterfaces/vst/ivstaudioprocessor.h"
#include "pluginterfaces/vst/ivsteditcontroller.h"
#include "pluginterfaces/vst/ivstprocesscontext.h"
#include "public.sdk/source/common/memorystream.h"
#include "public.sdk/source/vst/hosting/eventlist.h"
#include "public.sdk/source/vst/hosting/hostclasses.h"
#include "public.sdk/source/vst/hosting/parameterchanges.h"
#include "public.sdk/source/vst/hosting/plugprovider.h"
#include "public.sdk/source/vst/hosting/processdata.h"
#include "public.sdk/source/vst/utility/stringconvert.h"

#include <iomanip>
#include <optional>
#include <poll.h>

namespace {
using namespace Steinberg;
using namespace Steinberg::Vst;

void verifyEditor(IEditController* controller) {
    require(controller, "missing VST3 editor controller");
    EditorWindow window;
    for (int attempt = 0; attempt < 2; ++attempt) {
        auto frame = owned(new X11PlugFrame(window));
        auto view = owned(controller->createView(ViewType::kEditor));
        require(view && view->isPlatformTypeSupported(kPlatformTypeX11EmbedWindowID) == kResultOk,
                "no embedded VST3 X11 editor");
        require(view->setFrame(frame) == kResultOk, "VST3 editor frame rejected");
        ViewRect size{};
        require(view->getSize(&size) == kResultOk && window.resize(size.getWidth(), size.getHeight()),
                "invalid VST3 editor dimensions");
        require(view->attached(reinterpret_cast<void*>(window.id()), kPlatformTypeX11EmbedWindowID) == kResultOk,
                "VST3 editor attach failed");
        window.verify(frame->loop, true);
        require(view->removed() == kResultOk, "VST3 editor detach failed");
        view->setFrame(nullptr);
        view.reset();
        window.verify(frame->loop, false);
    }
}

void stateIO(const Request& request, IComponent* component, IEditController* controller) {
    if (request.mode == Mode::Save) {
        MemoryStream stream;
        require(component->getState(&stream) == kResultOk, "VST3 state save failed");
        const auto size = stream.getSize();
        require(size > 0 && size <= TSize(MaxState), "invalid VST3 state size");
        writeState(request.statePath, State(stream.getData(), stream.getData() + size));
    } else {
        auto bytes = readState(request.statePath);
        MemoryStream stream(bytes.data(), bytes.size());
        require(component->setState(&stream) == kResultOk, "VST3 state restore failed");
        if (controller) {
            stream.seek(0, IBStream::kIBSeekSet, nullptr);
            auto result = controller->setComponentState(&stream);
            require(result == kResultOk || result == kNotImplemented, "VST3 controller state sync failed");
        }
        std::cout << "restored bytes=" << bytes.size() << '\n';
    }
}

void activateAudioBuses(IComponent* component, BusDirection direction) {
    const auto count = component->getBusCount(kAudio, direction);
    require(count >= 0 && count <= 8 && (direction == kInput || count > 0), "unsupported VST3 bus count");
    for (int bus = 0; bus < count; ++bus) {
        BusInfo info{};
        require(component->getBusInfo(kAudio, direction, bus, info) == kResultOk, "VST3 bus metadata failed");
        require(info.channelCount == 1 || info.channelCount == 2, "probe requires mono or stereo");
        require(component->activateBus(kAudio, direction, bus, true) == kResultOk, "VST3 bus activation failed");
    }
}

class LatencyHandler final : public U::Implements<U::Directly<IComponentHandler>> {
public:
    bool changed = false;
    tresult PLUGIN_API beginEdit(ParamID) override { return kNotImplemented; }
    tresult PLUGIN_API performEdit(ParamID, ParamValue) override { return kNotImplemented; }
    tresult PLUGIN_API endEdit(ParamID) override { return kNotImplemented; }
    tresult PLUGIN_API restartComponent(int32 flags) override {
        changed |= (flags & kLatencyChanged) != 0;
        return kResultOk;
    }
};

void verifyLatency(IComponent* component, IEditController* controller) {
    auto handler = owned(new LatencyHandler);
    require(controller && controller->setComponentHandler(handler) == kResultOk, "VST3 latency handler rejected");
    FUnknownPtr<IAudioProcessor> processor(component);
    require(processor, "VST3 fixture processor missing");
    activateAudioBuses(component, kInput);
    activateAudioBuses(component, kOutput);
    ProcessSetup setup{kRealtime, kSample32, Frames, Rate};
    require(processor->setupProcessing(setup) == kResultOk, "VST3 fixture setup failed");
    for (const unsigned expected : {64, 192}) {
        require(component->setActive(true) == kResultOk, "VST3 fixture activation failed");
        const auto reported = processor->getLatencySamples();
        require(reported == expected, "VST3 fixture did not report the new latency");
        Samples input{}, output{};
        input[0] = 1.f;
        float* inData = input.data();
        float* outData = output.data();
        AudioBusBuffers in, out;
        in.numChannels = out.numChannels = 1;
        in.channelBuffers32 = &inData;
        out.channelBuffers32 = &outData;
        ProcessData data;
        data.numSamples = Frames;
        data.numInputs = data.numOutputs = 1;
        data.inputs = &in;
        data.outputs = &out;
        require(processor->setProcessing(true) == kResultOk, "VST3 fixture start failed");
        require(processor->process(data) == kResultOk, "VST3 fixture process failed");
        require(processor->setProcessing(false) == kResultOk, "VST3 fixture stop failed");
        for (unsigned i = 0; i < output.size(); ++i)
            require(output[i] == (i == expected ? 1.f : 0.f), "VST3 fixture impulse disagrees with reported latency");
        if (expected == 64) {
            ParameterInfo parameter{};
            require(controller->getParameterInfo(0, parameter) == kResultOk &&
                    controller->setParamNormalized(parameter.id, 1.0) == kResultOk && handler->changed,
                    "VST3 fixture did not request latency change");
        }
        require(component->setActive(false) == kResultOk, "VST3 fixture deactivation failed");
        std::cout << "latency reported=" << reported << " impulse_frame=" << expected
                  << " restart=" << (expected == 64 && handler->changed) << '\n';
    }
    controller->setComponentHandler(nullptr);
}

ParamID findParameter(const Request& request, IEditController* controller) {
    require(controller, "missing VST3 parameter controller");
    for (int32 i = 0; i < controller->getParameterCount(); ++i) {
        ParameterInfo info{};
        if (controller->getParameterInfo(i, info) != kResultOk ||
            request.parameter != StringConvert::convert(info.title, 128)) continue;
        require(info.flags & ParameterInfo::kCanAutomate, "VST3 parameter is not automatable");
        return info.id;
    }
    throw std::runtime_error("VST3 parameter not found: " + request.parameter);
}

void render(IComponent* component, const Request& request, bool instrument, ParamID parameter) {
    FUnknownPtr<IAudioProcessor> processor(component);
    require(processor && processor->canProcessSampleSize(kSample32) == kResultOk, "no VST3 float processor");
    activateAudioBuses(component, kInput);
    activateAudioBuses(component, kOutput);
    if (instrument) {
        require(component->getBusCount(kEvent, kInput) > 0, "no VST3 note input");
        require(component->activateBus(kEvent, kInput, 0, true) == kResultOk, "VST3 note bus activation failed");
    }
    ProcessSetup setup{kRealtime, kSample32, Frames, Rate};
    require(processor->setupProcessing(setup) == kResultOk, "VST3 processing setup failed");
    require(component->setActive(true) == kResultOk, "VST3 activation failed");

    HostProcessData data;
    require(data.prepare(*component, Frames, kSample32), "VST3 audio buffer setup failed");
    std::cout << "buses in=" << data.numInputs << " out=" << data.numOutputs
              << " latency=" << processor->getLatencySamples() << '\n';
    data.processMode = setup.processMode;
    data.numSamples = Frames;
    EventList events;
    data.inputEvents = &events;
    ParameterChanges changes(1);
    data.inputParameterChanges = &changes;
    ProcessContext context{};
    context.state = ProcessContext::kPlaying | ProcessContext::kTempoValid | ProcessContext::kTimeSigValid;
    context.sampleRate = Rate;
    context.tempo = 120;
    context.timeSigNumerator = context.timeSigDenominator = 4;
    data.processContext = &context;
    require(processor->setProcessing(true) == kResultOk, "VST3 start processing failed");

    std::optional<Transport> transport;
    if (transportMode(request.mode)) transport.emplace(TransportWorkerFD, false);
    Measurement measurement;
    for (int block = 0; block < (transport ? TransportBlocks : Blocks); ++block) {
        AudioBlock packet;
        if (transport) packet = transport->next(request.mode == Mode::TransportCrash);
        else if (block == 1) packet.note = AudioBlock::NoteOn;
        else if (block == 33) packet.note = AudioBlock::NoteOff;
        context.projectTimeSamples = block * Frames;
        events.clear();
        changes.clearQueue();
        if (block == 0 && !request.parameter.empty() && request.mode == Mode::Save) {
            int32 index = 0;
            auto* queue = changes.addParameterData(parameter, index);
            require(queue && queue->addPoint(0, request.value, index) == kResultOk, "VST3 parameter queue rejected event");
        }
        for (int bus = 0; bus < data.numInputs; ++bus) {
            auto& input = data.inputs[bus];
            const bool active = !instrument && bus == 0;
            input.silenceFlags = active ? 0 : (1u << input.numChannels) - 1;
            for (int channel = 0; channel < input.numChannels; ++channel) {
                if (active && transport) {
                    const auto& samples = channel == 0 ? packet.left : packet.right;
                    std::copy(samples.begin(), samples.end(), input.channelBuffers32[channel]);
                    continue;
                }
                for (int i = 0; i < Frames; ++i)
                    input.channelBuffers32[channel][i] = active ? 0.2f * std::sin(float(block * Frames + i) * 0.0576f) : 0.f;
            }
        }
        if (instrument && packet.note != AudioBlock::Silent) {
            Event event{};
            if (packet.note == AudioBlock::NoteOn) {
                event.type = Event::kNoteOnEvent;
                event.noteOn.pitch = 60;
                event.noteOn.velocity = 0.8f;
                event.noteOn.noteId = 1;
            } else {
                event.type = Event::kNoteOffEvent;
                event.noteOff.pitch = 60;
                event.noteOff.noteId = 1;
            }
            require(events.addEvent(event) == kResultOk, "VST3 note queue rejected event");
        }
        for (int bus = 0; bus < data.numOutputs; ++bus) {
            auto& output = data.outputs[bus];
            for (int channel = 0; channel < output.numChannels; ++channel)
                std::fill_n(output.channelBuffers32[channel], Frames, 0.f);
            output.silenceFlags = 0;
        }
        require(processor->process(data) == kResultOk, "VST3 processing failed");
        if (transport) {
            const auto& output = data.outputs[0];
            std::copy_n(output.channelBuffers32[0], Frames, packet.left.begin());
            std::copy_n(output.channelBuffers32[output.numChannels - 1], Frames, packet.right.begin());
            transport->publish(packet);
        }
        for (int bus = 0; bus < data.numOutputs; ++bus)
            for (int channel = 0; channel < data.outputs[bus].numChannels; ++channel) {
                Samples samples;
                std::copy_n(data.outputs[bus].channelBuffers32[channel], Frames, samples.begin());
                measurement.observe(samples);
            }
        if (block == 0) {
            std::cout << "processed first block" << std::endl;
            injectFault(request.mode);
        }
    }
    processor->setProcessing(false);
    component->setActive(false);
    measurement.report();
}
}

void probeVST3(const Request& request) {
    using VST3::Hosting::Module;
    auto host = owned(new HostApplication);
    std::string error;
    auto module = Module::create(request.path, error);
    require(bool(module), "VST3 module load failed: " + error);
    const auto& factory = module->getFactory();
    factory.setHostContext(host);
    for (const auto& info : factory.classInfos()) {
        if (info.category() != kVstAudioEffectClass) continue;
        const auto id = info.ID().toString();
        if (request.mode == Mode::Scan) {
            std::cout << "plugin " << std::quoted(id) << ' ' << std::quoted(info.name()) << '\n';
            continue;
        }
        if (id != request.id) continue;
        PluginContextFactory::instance().setPluginContext(host);
        PlugProvider provider(factory, info);
        require(provider.initialize(), "VST3 instance initialization failed");
        auto component = provider.getComponentPtr();
        auto controller = provider.getControllerPtr();
        require(bool(component), "VST3 component missing");
        if (request.mode == Mode::Editor) {
            verifyEditor(controller);
            return;
        }
        if (request.mode == Mode::Latency) {
            verifyLatency(component, controller);
            return;
        }
        auto view = controller ? owned(controller->createView(ViewType::kEditor)) : IPtr<IPlugView>{};
        std::cout << "params=" << (controller ? controller->getParameterCount() : 0)
                  << " gui_x11_embed=" << bool(view && view->isPlatformTypeSupported(kPlatformTypeX11EmbedWindowID) == kResultOk)
                  << " gui_wayland_embed=" << bool(view && view->isPlatformTypeSupported(kPlatformTypeWaylandSurfaceID) == kResultOk)
                  << '\n';
        view.reset();
        if (request.mode != Mode::Save && !transportMode(request.mode)) stateIO(request, component, controller);
        ParamID parameter = 0;
        if (!request.parameter.empty()) {
            parameter = findParameter(request, controller);
            const auto actual = controller->getParamNormalized(parameter);
            if (request.mode == Mode::Save) {
                require(std::abs(actual - request.value) > 1e-4, "VST3 test requires a non-default value");
                require(controller->setParamNormalized(parameter, request.value) == kResultOk, "VST3 controller rejected value");
            } else {
                require(std::abs(actual - request.value) < 1e-4, "VST3 restored parameter did not match the test value");
            }
            std::cout << "parameter " << request.parameter << " id=" << parameter
                      << " value=" << controller->getParamNormalized(parameter) << '\n';
        }
        const auto& categories = info.subCategories();
        render(component, request, std::find(categories.begin(), categories.end(), PlugType::kInstrument) != categories.end(), parameter);
        if (request.mode == Mode::Save) stateIO(request, component, controller);
        return;
    }
    require(request.mode == Mode::Scan, "VST3 class ID not found");
}
