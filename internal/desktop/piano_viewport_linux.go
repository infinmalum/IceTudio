//go:build linux && (amd64 || arm64)

package desktop

import (
	"sync"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo/ui"
)

// GTK owns the widgets. Only the GList returned by getChildren is temporary.
type gtkChild struct {
	widget uintptr
	next   *gtkChild
	prev   *gtkChild
}

type gtkAllocation struct {
	x, y, width, height int32
}

var schedulePianoAllocation = sync.OnceValue(func() func(uintptr) {
	lib, err := purego.Dlopen("libgtk-3.so.0", purego.RTLD_LAZY|purego.RTLD_LOCAL)
	if err != nil {
		return nil
	}
	var containerType func() uintptr
	var isA func(uintptr, uintptr) int32
	var typeName func(uintptr) string
	var getChildren func(uintptr) *gtkChild
	var freeList func(*gtkChild)
	var isMapped func(uintptr) int32
	var childGet func(uintptr, uintptr, string, *int32, string, *int32, uintptr)
	var sizeRequest func(uintptr, *int32, *int32)
	var getAllocation, allocate func(uintptr, *gtkAllocation)
	var queueDraw func(uintptr)
	var idleAdd func(int32, uintptr, uintptr, uintptr) uint32
	var ref func(uintptr) uintptr
	for _, binding := range []struct {
		name string
		dest any
	}{
		{"gtk_container_get_type", &containerType},
		{"g_type_check_instance_is_a", &isA},
		{"g_type_name_from_instance", &typeName},
		{"gtk_container_get_children", &getChildren},
		{"g_list_free", &freeList},
		{"gtk_widget_get_mapped", &isMapped},
		{"gtk_container_child_get", &childGet},
		{"gtk_widget_get_size_request", &sizeRequest},
		{"gtk_widget_get_allocation", &getAllocation},
		{"gtk_widget_size_allocate", &allocate},
		{"gtk_widget_queue_draw", &queueDraw},
		{"g_idle_add_full", &idleAdd},
		{"g_object_ref", &ref},
	} {
		symbol, err := purego.Dlsym(lib, binding.name)
		if err != nil {
			_ = purego.Dlclose(lib)
			return nil
		}
		purego.RegisterFunc(binding.dest, symbol)
	}
	unref, err := purego.Dlsym(lib, "g_object_unref")
	if err != nil {
		_ = purego.Dlclose(lib)
		return nil
	}

	var visit func(parent, widget uintptr, parentType string)
	visit = func(parent, widget uintptr, parentType string) {
		if isMapped(widget) == 0 {
			return
		}
		name := typeName(widget)
		if name == "WebKitWebView" && parentType == "GtkLayout" {
			var requested, current gtkAllocation
			sizeRequest(widget, &requested.width, &requested.height)
			if requested.width <= 0 || requested.height <= 0 {
				return
			}
			// gtk_container_child_get takes property/output pairs and a
			// NULL terminator. These arguments use only integer registers.
			childGet(parent, widget, "x", &requested.x, "y", &requested.y, 0)
			getAllocation(widget, &current)
			if requested != current {
				allocate(widget, &requested)
			}
			queueDraw(widget)
			return
		}
		if isA(widget, containerType()) != 0 {
			list := getChildren(widget)
			for child := list; child != nil; child = child.next {
				visit(widget, child.widget, name)
			}
			freeList(list)
		}
	}
	idle := purego.NewCallback(func(window uintptr) int32 {
		visit(0, window, "")
		return 0
	})
	return func(window uintptr) {
		const defaultIdlePriority = 200
		idleAdd(defaultIdlePriority, idle, ref(window), unref)
	}
})

// MyGo 0.8.3 sets the GtkLayout child position and requests a size after
// painting. GTK can leave the child allocated at the old size. At idle,
// apply that same requested allocation if it has not taken effect.
func (a *Workspace) syncPianoViewport(_ *ui.Painter, bounds ui.Rect) {
	if a.win == nil || bounds == a.pianoPlacement {
		return
	}
	a.pianoPlacement = bounds
	if schedule := schedulePianoAllocation(); schedule != nil {
		if window := a.win.NativeHandle(); window != 0 {
			schedule(window)
		}
	}
}
