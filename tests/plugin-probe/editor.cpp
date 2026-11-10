#include "editor.h"
#include "probe.h"
#include <X11/Xlib.h>

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
