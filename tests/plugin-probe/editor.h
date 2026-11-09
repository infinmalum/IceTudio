#pragma once

#include <chrono>
#include <functional>
#include <map>

class EventLoop {
public:
    using Clock = std::chrono::steady_clock;
    using Callback = std::function<void(short)>;

    bool watch(int fd, short events, Callback callback);
    bool modify(int fd, short events);
    bool unwatch(int fd);
    unsigned timer(unsigned ms, Callback callback);
    bool untimer(unsigned id);
    void pump();

private:
    struct Watch { unsigned id; short events; Callback callback; };
    struct Timer { Clock::time_point next; std::chrono::milliseconds period; Callback callback; };
    unsigned serial = 0;
    std::map<int, Watch> watches;
    std::map<unsigned, Timer> timers;
};

class EditorWindow {
public:
    EditorWindow();
    ~EditorWindow();
    EditorWindow(const EditorWindow&) = delete;
    EditorWindow& operator=(const EditorWindow&) = delete;

    unsigned long id() const { return window; }
    bool resize(int width, int height);
    void verify(EventLoop& loop, bool open, const std::function<void()>& dispatch = {});

private:
    void* display;
    unsigned long window;
};
