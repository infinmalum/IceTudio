#include "editor.h"
#include "runtime.h"
#include <array>

#include <X11/Xlib.h>
#include <poll.h>

bool EventLoop::watch(int fd, short events, Callback callback) {
    return fd >= 0 && watches.size() < 64 &&
        watches.emplace(fd, Watch{++serial, events, std::move(callback)}).second;
}

bool EventLoop::modify(int fd, short events) {
    auto iter = watches.find(fd);
    if (iter == watches.end()) return false;
    iter->second.events = events;
    return true;
}

bool EventLoop::unwatch(int fd) { return watches.erase(fd) != 0; }

unsigned EventLoop::timer(unsigned ms, Callback callback) {
    if (timers.size() == 64 || ms > 60000) return 0;
    const auto period = std::chrono::milliseconds(std::max(ms, 1u));
    const unsigned id = ++serial;
    timers.emplace(id, Timer{Clock::now() + period, period, std::move(callback)});
    return id;
}

bool EventLoop::untimer(unsigned id) { return timers.erase(id) != 0; }

void EventLoop::pump() {
    std::array<pollfd, 64> fds{};
    std::array<unsigned, 64> ids{};
    std::size_t count = 0;
    for (auto& [fd, watch] : watches) {
        ids[count] = watch.id;
        fds[count++] = {fd, watch.events, 0};
    }
    if (poll(fds.data(), count, 4) > 0) {
        for (std::size_t i = 0; i < count; ++i) {
            const auto it = watches.find(fds[i].fd);
            if (fds[i].revents && it != watches.end() && it->second.id == ids[i]) {
                auto callback = it->second.callback;
                callback(fds[i].revents);
            }
        }
    }
    const auto now = Clock::now();
    std::array<unsigned, 64> ready{};
    count = 0;
    for (auto& [id, timer] : timers)
        if (timer.next <= now) ready[count++] = id;
    for (std::size_t i = 0; i < count; ++i) {
        if (auto it = timers.find(ready[i]); it != timers.end()) {
            it->second.next = now + it->second.period;
            auto callback = it->second.callback;
            callback(0);
        }
    }
}

EditorWindow::EditorWindow() : display(XOpenDisplay(nullptr)) {
    check(display, "isolated X11 display unavailable");
    auto* x = static_cast<Display*>(display);
    window = XCreateSimpleWindow(x, DefaultRootWindow(x), 0, 0, 640, 480, 0, 0, 0);
    check(window != 0, "X11 host window allocation failed");
    closeAtom = XInternAtom(x, "WM_DELETE_WINDOW", False);
    Atom closeProtocol = closeAtom;
    XSetWMProtocols(x, window, &closeProtocol, 1);
    XStoreName(x, window, "IceTudio Plugin");
    XMapWindow(x, window);
    XSync(x, False);
}

EditorWindow::~EditorWindow() {
    auto* x = static_cast<Display*>(display);
    XDestroyWindow(x, window);
    XCloseDisplay(x);
}

bool EditorWindow::resize(int width, int height) {
    if (width < 1 || height < 1 || width > 4096 || height > 4096) return false;
    XResizeWindow(static_cast<Display*>(display), window, width, height);
    XFlush(static_cast<Display*>(display));
    return true;
}

bool EditorWindow::closed() {
    auto* x = static_cast<Display*>(display);
    while (XPending(x)) {
        XEvent event;
        XNextEvent(x, &event);
        if (event.type == ClientMessage && static_cast<unsigned long>(event.xclient.data.l[0]) == closeAtom)
            return true;
    }
    return false;
}
