#pragma once
#include "editor.h"
#include "pluginterfaces/gui/iplugview.h"
#include "pluginterfaces/base/funknownimpl.h"
#include <poll.h>

namespace Steinberg {
class X11PlugFrame final : public U::Implements<U::Directly<IPlugFrame, Linux::IRunLoop>> {
public:
    explicit X11PlugFrame(EditorWindow& window) : window(window) {}
    EventLoop loop;

    tresult PLUGIN_API resizeView(IPlugView* view, ViewRect* rect) override {
        return view && rect && window.resize(rect->getWidth(), rect->getHeight()) ? view->onSize(rect) : kResultFalse;
    }
    tresult PLUGIN_API registerEventHandler(Linux::IEventHandler* handler, int fd) override {
        if (!handler || !loop.watch(fd, POLLIN, [held = IPtr(handler), fd](short) { held->onFDIsSet(fd); }))
            return kResultFalse;
        descriptors.emplace(handler, fd);
        return kResultOk;
    }
    tresult PLUGIN_API unregisterEventHandler(Linux::IEventHandler* handler) override {
        auto [begin, end] = descriptors.equal_range(handler);
        if (begin == end) return kResultFalse;
        for (auto it = begin; it != end; ++it) loop.unwatch(it->second);
        descriptors.erase(begin, end);
        return kResultOk;
    }
    tresult PLUGIN_API registerTimer(Linux::ITimerHandler* handler, Linux::TimerInterval ms) override {
        if (!handler || ms > 60000 || timers.contains(handler)) return kResultFalse;
        const auto id = loop.timer(static_cast<unsigned>(ms), [held = IPtr(handler)](short) { held->onTimer(); });
        if (!id) return kResultFalse;
        timers.emplace(handler, id);
        return kResultOk;
    }
    tresult PLUGIN_API unregisterTimer(Linux::ITimerHandler* handler) override {
        auto it = timers.find(handler);
        if (it == timers.end()) return kResultFalse;
        loop.untimer(it->second);
        timers.erase(it);
        return kResultOk;
    }
private:
    EditorWindow& window;
    std::multimap<Linux::IEventHandler*, int> descriptors;
    std::map<Linux::ITimerHandler*, unsigned> timers;
};

}
