#include "public.sdk/source/vst/vstsinglecomponenteffect.h"
#include "public.sdk/source/main/pluginfactory.h"

#include <array>

namespace {
using namespace Steinberg;
using namespace Steinberg::Vst;

const FUID fixtureID(0xA7CE358F, 0x9BDA486E, 0x8F2BD07E, 0x2205E095);
constexpr ParamID latencyID = 100;

class LatencyFixture final : public SingleComponentEffect {
public:
    static FUnknown* make(void*) { return static_cast<IComponent*>(new LatencyFixture); }

    tresult PLUGIN_API initialize(FUnknown* context) override {
        const auto result = SingleComponentEffect::initialize(context);
        if (result != kResultOk) return result;
        addAudioInput(STR16("Input"), SpeakerArr::kMono);
        addAudioOutput(STR16("Output"), SpeakerArr::kMono);
        parameters.addParameter(STR16("Latency"), nullptr, 1, 0.0, ParameterInfo::kCanAutomate, latencyID);
        return kResultOk;
    }

    tresult PLUGIN_API setParamNormalized(ParamID id, ParamValue value) override {
        const auto result = SingleComponentEffect::setParamNormalized(id, value);
        if (result == kResultOk && id == latencyID) {
            nextLatency = value >= 0.5 ? 192 : 64;
            if (componentHandler) componentHandler->restartComponent(kLatencyChanged);
        }
        return result;
    }

    tresult PLUGIN_API setActive(TBool active) override {
        if (active) {
            latency = nextLatency;
            index = 0;
            ring.fill(0.f);
        }
        return kResultOk;
    }

    uint32 PLUGIN_API getLatencySamples() override { return latency; }
    tresult PLUGIN_API setProcessing(TBool) override { return kResultOk; }

    tresult PLUGIN_API process(ProcessData& data) override {
        auto* in = data.inputs[0].channelBuffers32[0];
        auto* out = data.outputs[0].channelBuffers32[0];
        for (int32 i = 0; i < data.numSamples; ++i) {
            out[i] = ring[index];
            ring[index] = in[i];
            index = (index + 1) % latency;
        }
        return kResultOk;
    }

private:
    uint32 nextLatency = 64, latency = 64, index = 0;
    std::array<float, 192> ring{};
};
}

BEGIN_FACTORY_DEF("IceTudio", "", "")
DEF_CLASS2(INLINE_UID_FROM_FUID(fixtureID), PClassInfo::kManyInstances, kVstAudioEffectClass,
           "IceTudio Latency Fixture", 0, PlugType::kFx, "1", kVstVersionString, LatencyFixture::make)
END_FACTORY
