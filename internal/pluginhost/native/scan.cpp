#include <clap/clap.h>
#include <dlfcn.h>
#include <fcntl.h>
#include <unistd.h>

#include "pluginterfaces/vst/ivstaudioprocessor.h"
#include "public.sdk/source/vst/hosting/module.h"

#include <algorithm>
#include <cstring>
#include <sstream>
#include <stdexcept>
#include <string_view>

namespace {
constexpr std::size_t MaxText = 256;
constexpr std::size_t MaxClasses = 64;
constexpr std::string_view Protocol = "ICE_PLUGIN_SCAN_1";

void require(bool ok, const char* message) {
    if (!ok) throw std::runtime_error(message);
}

std::string_view bounded(const char* s) {
    if (!s) return {};
    const auto length = strnlen(s, MaxText + 1);
    require(length <= MaxText, "descriptor text exceeds limit");
    return {s, length};
}

void field(std::ostream& output, std::string_view text) {
    static constexpr char hex[] = "0123456789abcdef";
    require(text.size() <= MaxText, "descriptor text exceeds limit");
    output << '\t';
    for (unsigned char c : text)
        output << hex[c >> 4] << hex[c & 15];
}

void record(std::ostream& output, std::string_view id, std::string_view name,
            std::string_view vendor, std::string_view version, std::string_view kind) {
    require(!id.empty() && !name.empty(), "descriptor missing ID or name");
    output << "plugin";
    for (auto value : {id, name, vendor, version, kind}) field(output, value);
    output << '\n';
}

void scanClap(const char* path, std::ostream& output) {
    auto* module = dlopen(path, RTLD_NOW | RTLD_LOCAL);
    if (!module) throw std::runtime_error(dlerror());
    const auto* entry = static_cast<const clap_plugin_entry_t*>(dlsym(module, "clap_entry"));
    require(entry && clap_version_is_compatible(entry->clap_version), "incompatible CLAP entry");
    require(entry->init(path), "CLAP module initialization failed");
    const auto* factory = static_cast<const clap_plugin_factory_t*>(entry->get_factory(CLAP_PLUGIN_FACTORY_ID));
    require(factory, "missing CLAP factory");
    const auto count = factory->get_plugin_count(factory);
    require(count > 0 && count <= MaxClasses, "unsupported CLAP class count");
    for (uint32_t i = 0; i < count; ++i) {
        const auto* desc = factory->get_plugin_descriptor(factory, i);
        require(desc && clap_version_is_compatible(desc->clap_version), "incompatible CLAP descriptor");
        std::string_view kind = "other";
        for (unsigned n = 0; desc->features && n < 64 && desc->features[n]; ++n) {
            const auto feature = bounded(desc->features[n]);
            if (feature == CLAP_PLUGIN_FEATURE_INSTRUMENT) { kind = "instrument"; break; }
            if (feature == CLAP_PLUGIN_FEATURE_AUDIO_EFFECT) kind = "effect";
        }
        record(output, bounded(desc->id), bounded(desc->name), bounded(desc->vendor), bounded(desc->version), kind);
    }
    entry->deinit();
    dlclose(module);
}

void scanVST3(const char* path, std::ostream& output) {
    std::string error;
    auto module = VST3::Hosting::Module::create(path, error);
    if (!module) throw std::runtime_error(error);
    const auto classes = module->getFactory().classInfos();
    require(classes.size() <= MaxClasses, "unsupported VST3 class count");
    unsigned count = 0;
    for (const auto& info : classes) {
        using namespace Steinberg::Vst;
        if (info.category() != kVstAudioEffectClass) continue;
        const auto& categories = info.subCategories();
        const bool instrument = std::find(categories.begin(), categories.end(), PlugType::kInstrument) != categories.end();
        record(output, info.ID().toString(), info.name(), info.vendor(), info.version(), instrument ? "instrument" : "effect");
        ++count;
    }
    require(count > 0, "no VST3 audio classes");
}

void send(int fd, std::string_view text) {
    while (!text.empty()) {
        const auto size = write(fd, text.data(), text.size());
        if (size <= 0) throw std::runtime_error("scan response failed");
        text.remove_prefix(static_cast<std::size_t>(size));
    }
}
}

int main(int argc, char** argv) {
    if (argc == 2 && std::string_view(argv[1]) == "--protocol") {
        return write(STDOUT_FILENO, Protocol.data(), Protocol.size()) == static_cast<ssize_t>(Protocol.size()) ? 0 : 1;
    }
    if (argc != 3) return 1;
    const int report = fcntl(STDOUT_FILENO, F_DUPFD_CLOEXEC, 3);
    const int discard = open("/dev/null", O_WRONLY | O_CLOEXEC);
    if (report < 0 || discard < 0 || dup2(discard, STDOUT_FILENO) < 0 || dup2(discard, STDERR_FILENO) < 0)
        return 1;
    close(discard);
    try {
        std::ostringstream output;
        output << Protocol << '\n';
        try {
            const std::string_view format = argv[1];
            if (format == "clap") scanClap(argv[2], output);
            else if (format == "vst3") scanVST3(argv[2], output);
            else throw std::runtime_error("unsupported plugin format");
        } catch (const std::exception& e) {
            output.str({});
            output << Protocol << "\nerror";
            field(output, std::string_view(e.what()).substr(0, MaxText));
            output << '\n';
        }
        send(report, output.str());
        close(report);
        return 0;
    } catch (...) {
        return 1;
    }
}
