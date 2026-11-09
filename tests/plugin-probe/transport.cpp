#include "transport.h"

#include <atomic>
#include <chrono>
#include <cstdlib>
#include <fcntl.h>
#include <new>
#include <sys/mman.h>
#include <sys/stat.h>
#include <thread>
#include <unistd.h>

namespace {
using namespace std::chrono_literals;
using Clock = std::chrono::steady_clock;
constexpr unsigned FaultBlock = 48;

struct Ring {
    static constexpr unsigned capacity = 16;
    static_assert(std::atomic<unsigned>::is_always_lock_free);
    alignas(64) std::atomic<unsigned> head{0};
    alignas(64) std::atomic<unsigned> tail{0};
    std::array<AudioBlock, capacity> slots{};

    bool push(const AudioBlock& block) {
        const auto write = head.load(std::memory_order_relaxed);
        if (write - tail.load(std::memory_order_acquire) == capacity) return false;
        slots[write % capacity] = block;
        head.store(write + 1, std::memory_order_release);
        return true;
    }

    bool pop(AudioBlock& block) {
        const auto read = tail.load(std::memory_order_relaxed);
        if (read == head.load(std::memory_order_acquire)) return false;
        block = slots[read % capacity];
        tail.store(read + 1, std::memory_order_release);
        return true;
    }
};
}

struct Transport::Shared {
    Ring input, output;
    std::atomic<unsigned> ready{0};
};

Transport::Transport(int fd, bool host) {
    constexpr int seals = F_SEAL_SHRINK | F_SEAL_GROW | F_SEAL_SEAL;
    struct stat st{};
    bool valid = host ? ftruncate(fd, sizeof(Shared)) == 0 && fcntl(fd, F_ADD_SEALS, seals) == 0 :
        fstat(fd, &st) == 0 && S_ISREG(st.st_mode) && st.st_size == sizeof(Shared);
    const int applied = fcntl(fd, F_GET_SEALS);
    valid = valid && applied >= 0 && (applied & seals) == seals;
    auto* mapping = valid ? mmap(nullptr, sizeof(Shared), PROT_READ | PROT_WRITE, MAP_SHARED, fd, 0) : MAP_FAILED;
    // Keep worker stdin occupied so a plugin opening a file cannot accidentally
    // inherit descriptor 0 and get it treated as an input stream.
    if (host) close(fd);
    require(mapping != MAP_FAILED, "cannot map transport buffer");
    shared = static_cast<Shared*>(mapping);
    if (host) {
        new (shared) Shared;
        std::cout << "transport mapping ready" << std::endl;
    } else {
        shared->ready.store(1, std::memory_order_release);
    }
}

Transport::~Transport() { munmap(shared, sizeof(Shared)); }

AudioBlock Transport::next(bool crash) {
    const auto deadline = Clock::now() + 2s;
    AudioBlock block;
    while (!shared->input.pop(block)) {
        require(Clock::now() < deadline, "transport input timed out");
        std::this_thread::sleep_for(50us);
    }
    // Inject a stall well beyond the output deadline after normal blocks.
    if (block.sequence == FaultBlock) {
        if (crash) {
            std::cout << "transport crash at block=" << block.sequence << std::endl;
            std::abort();
        }
        std::this_thread::sleep_for(36ms);
    }
    return block;
}

void Transport::publish(const AudioBlock& block) {
    require(shared->output.push(block), "transport output overflow");
}

void Transport::exerciseHost(bool crash) {
    const auto startupDeadline = Clock::now() + 10s;
    while (!shared->ready.load(std::memory_order_acquire)) {
        require(Clock::now() < startupDeadline, "transport worker startup timed out");
        std::this_thread::sleep_for(1ms);
    }
    constexpr int lead = 2;
    constexpr auto period = std::chrono::nanoseconds(1000000000LL * Frames / Rate);
    const auto start = Clock::now();
    std::array<long long, TransportBlocks + lead> costs{};
    AudioBlock pending;
    bool havePending = false;
    unsigned missed = 0, late = 0, dropped = 0, delivered = 0;
    Measurement measurement;
    for (int tick = 0; tick < TransportBlocks + lead; ++tick) {
        std::this_thread::sleep_until(start + period * tick);
        AudioBlock input;
        input.sequence = tick;
        for (int i = 0; i < Frames; ++i)
            input.left[i] = input.right[i] = 0.2f * std::sin(float(tick * Frames + i) * .0576f);
        if (tick == 1) input.note = AudioBlock::NoteOn;
        if (tick == 110) input.note = AudioBlock::NoteOff;
        AudioBlock output;
        const auto begin = Clock::now();
        // Simulated callback. No waits, file calls, heap allocation, or locks.
        if (tick >= lead) {
            const auto expected = unsigned(tick - lead);
            for (unsigned i = 0; i < Ring::capacity; ++i) {
                if (!havePending) havePending = shared->output.pop(pending);
                if (!havePending || pending.sequence >= expected) break;
                ++late;
                havePending = false;
            }
            if (havePending && pending.sequence == expected) {
                output = pending;
                havePending = false;
                ++delivered;
            } else {
                ++missed; // The default output block is silence.
            }
        }
        if (tick < TransportBlocks && !shared->input.push(input)) ++dropped;
        costs[tick] = std::chrono::duration_cast<std::chrono::nanoseconds>(Clock::now() - begin).count();
        measurement.observe(output.left);
        measurement.observe(output.right);
    }
    std::sort(costs.begin(), costs.end());
    const auto p99 = costs[(costs.size() * 99 - 1) / 100];
    std::cout << "transport delivered=" << delivered << " missed=" << missed << " stale_discarded=" << late
              << " dropped_input=" << dropped << " lead_frames=" << lead * Frames << " callback_p99_ns=" << p99 << '\n';
    measurement.report();
    if (crash) {
        require(delivered <= FaultBlock && delivered >= FaultBlock - 4 &&
                missed >= TransportBlocks - FaultBlock && dropped > 0,
                "callback did not continue with silence after the worker exited");
    } else {
        require(dropped == 0 && missed >= 3 && late >= 3 && delivered >= TransportBlocks - 14,
                "transport did not recover within the bounded callback window");
    }
}

void probeTransport(int fd, bool crash) { Transport(fd, true).exerciseHost(crash); }
