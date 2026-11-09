#include "editor.h"
#include "probe.h"

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
    require(display, "isolated X11 display unavailable");
    auto* x = static_cast<Display*>(display);
    window = XCreateSimpleWindow(x, DefaultRootWindow(x), 0, 0, 640, 480, 0, 0, 0);
    require(window != 0, "X11 host window allocation failed");
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

void EditorWindow::verify(EventLoop& loop, bool open, const std::function<void()>& dispatch) {
    auto* x = static_cast<Display*>(display);
    const auto until = EventLoop::Clock::now() + std::chrono::milliseconds(500);
    do {
        loop.pump();
        if (dispatch) dispatch();
    } while (EventLoop::Clock::now() < until);
    Window root, parent, *children = nullptr;
    unsigned count = 0;
    require(XQueryTree(x, window, &root, &parent, &children, &count), "query X11 editor children failed");
    bool mapped = false;
    for (unsigned i = 0; i < count; ++i) {
        XWindowAttributes attributes{};
        if (XGetWindowAttributes(x, children[i], &attributes) && attributes.map_state == IsViewable &&
            attributes.width > 1 && attributes.height > 1) mapped = true;
    }
    if (children) XFree(children);
    require(open == mapped, open ? "no visible plugin child" : "plugin child stayed visible after closing");
    std::cout << "editor visible=" << mapped << " children=" << count << '\n';
}
