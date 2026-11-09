#include "probe.h"
#include "transport.h"

#include <chrono>
#include <cstdlib>
#include <fstream>
#include <string_view>
#include <thread>

State readState(const std::string& path) {
    std::ifstream stream(path, std::ios::binary | std::ios::ate);
    const auto size = stream.tellg();
    require(size > 0 && size <= std::streamoff(MaxState), "invalid state size");
    State data(static_cast<std::size_t>(size));
    stream.seekg(0);
    require(bool(stream.read(data.data(), size)), "state read failed");
    return data;
}

void writeState(const std::string& path, const State& state) {
    require(!state.empty() && state.size() <= MaxState, "invalid state size");
    std::ofstream stream(path, std::ios::binary | std::ios::trunc);
    stream.write(state.data(), state.size());
    stream.close();
    require(bool(stream), "state write failed");
    std::cout << "state bytes=" << state.size() << '\n';
}

void injectFault(Mode mode) {
    if (mode == Mode::Abort) std::abort();
    if (mode == Mode::Hang) std::this_thread::sleep_for(std::chrono::minutes(1));
}

int main(int argc, char** argv) {
    try {
        if (argc == 4 && std::string_view(argv[1]) == "transport-host") {
            const std::string_view fault = argv[3];
            require(fault == "stall" || fault == "crash", "unknown transport fault");
            std::size_t parsed = 0;
            const auto fd = std::stoi(argv[2], &parsed);
            require(parsed == std::string_view(argv[2]).size() && fd > 2, "invalid transport descriptor");
            probeTransport(fd, fault == "crash");
            return 0;
        }
        require(argc == 4 || argc == 6 || argc == 8,
                "usage: probe clap|vst3 scan PATH | FORMAT save|restore|abort|hang|editor|transport|transport-crash|latency PATH ID STATE [PARAM VALUE]");
        const std::string_view format = argv[1], action = argv[2];
        Request request{Mode::Scan, argv[3], {}, {}, {}};
        if (action != "scan") {
            require(argc >= 6, "missing plugin ID or state path");
            request.id = argv[4];
            request.statePath = argv[5];
            if (action == "save") request.mode = Mode::Save;
            else if (action == "restore") request.mode = Mode::Restore;
            else if (action == "abort") request.mode = Mode::Abort;
            else if (action == "hang") request.mode = Mode::Hang;
            else if (action == "editor") request.mode = Mode::Editor;
            else if (action == "transport") request.mode = Mode::Transport;
            else if (action == "transport-crash") request.mode = Mode::TransportCrash;
            else if (action == "latency") request.mode = Mode::Latency;
            else throw std::runtime_error("unknown action");
            if (argc == 8) {
                require(request.mode == Mode::Save || request.mode == Mode::Restore, "unexpected parameter override");
                request.parameter = argv[6];
                std::size_t parsed = 0;
                request.value = std::stod(argv[7], &parsed);
                require(parsed == std::string_view(argv[7]).size() &&
                        request.value >= 0 && request.value <= 1, "invalid normalized value");
            }
        } else {
            require(argc == 4, "scan accepts a single module path");
        }
        if (format == "clap") probeClap(request);
        else if (format == "vst3") probeVST3(request);
        else throw std::runtime_error("unknown plugin format");
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
