#include "runtime.h"

#include <sys/mman.h>

int main(int argc, char** argv) {
    if (argc != 6) return 2;
    void* mapping = mmap(nullptr, sizeof(ice_plugin_wire), PROT_READ | PROT_WRITE, MAP_SHARED, 0, 0);
    if (mapping == MAP_FAILED) return 2;
    auto& wire = *static_cast<ice_plugin_wire*>(mapping);
    try {
        check(wire.magic == ICE_PLUGIN_MAGIC && wire.state_size <= ICE_PLUGIN_STATE, "invalid host protocol");
        const std::string format = argv[1];
        const bool instrument = std::string(argv[4]) == "instrument";
        const bool offline = std::string(argv[5]) == "offline";
        const bool editor = std::string(argv[5]) == "editor";
        if (format == "clap") runClap(argv[2], argv[3], instrument, offline, editor, wire);
        else if (format == "vst3") runVST3(argv[2], argv[3], instrument, offline, editor, wire);
        else throw std::runtime_error("unsupported plugin format");
    } catch (const std::exception& error) {
        std::strncpy(wire.error, error.what(), sizeof(wire.error)-1);
        __atomic_store_n(&wire.status, 2, __ATOMIC_RELEASE);
    }
    munmap(mapping, sizeof(ice_plugin_wire));
    return 1;
}
