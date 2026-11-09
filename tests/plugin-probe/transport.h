#pragma once

#include "probe.h"

inline constexpr int TransportBlocks = 160;
inline constexpr int TransportWorkerFD = 0; // Dedicated worker inherits the mapping as stdin.

struct AudioBlock {
    unsigned sequence = 0;
    Samples left{}, right{};
    enum Note { Silent, NoteOn, NoteOff } note = Silent;
};

class Transport {
public:
    explicit Transport(int fd, bool host);
    ~Transport();
    Transport(const Transport&) = delete;
    Transport& operator=(const Transport&) = delete;

    AudioBlock next(bool crash);
    void publish(const AudioBlock& block);
    void exerciseHost(bool crash);

private:
    struct Shared;
    Shared* shared;
};

void probeTransport(int fd, bool crash);
