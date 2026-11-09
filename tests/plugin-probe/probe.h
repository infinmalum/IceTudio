#pragma once

#include <algorithm>
#include <array>
#include <cmath>
#include <cstddef>
#include <iostream>
#include <stdexcept>
#include <string>
#include <string_view>
#include <vector>

inline constexpr int Frames = 256;
inline constexpr int Rate = 48000;
inline constexpr int Blocks = 48;
inline constexpr std::size_t MaxState = 4 * 1024 * 1024;
using Samples = std::array<float, Frames>;
using State = std::vector<char>;

enum class Mode { Scan, Save, Restore, Abort, Hang, Editor, Transport, TransportCrash, Latency };
struct Request {
    Mode mode;
    std::string path;
    std::string id;
    std::string statePath;
    std::string parameter;
    double value = 0;
};

inline bool transportMode(Mode mode) {
    return mode == Mode::Transport || mode == Mode::TransportCrash;
}

inline void require(bool success, std::string_view reason) {
    if (!success) throw std::runtime_error(std::string(reason));
}

struct Measurement {
    double energy = 0;
    float peak = 0;

    void observe(const Samples& samples) {
        for (float sample : samples) {
            require(std::isfinite(sample), "nonfinite plugin output");
            peak = std::max(peak, std::abs(sample));
            energy += double(sample) * sample;
        }
    }

    void report() const {
        require(energy > 1e-8, "silent plugin output");
        std::cout << "audio peak=" << peak << " energy=" << energy << '\n';
    }
};

State readState(const std::string& path);
void writeState(const std::string& path, const State& state);
void injectFault(Mode mode);
void probeClap(const Request& request);
void probeVST3(const Request& request);
