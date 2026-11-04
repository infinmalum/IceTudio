"""Select only a test-owned GTK native file chooser via AT-SPI (Linux opt-in)."""
import json
import os
import sys
import time
import gi

gi.require_version('Atspi', '2.0')
from gi.repository import Atspi

pid, mode, path = int(sys.argv[1]), sys.argv[2], os.path.abspath(sys.argv[3])
assert mode in ('save', 'open')
assert path.startswith(os.path.abspath('.cache/p2-closeout/native-final') + os.sep)
root = Atspi.get_desktop(0)

def nodes(node, depth=0):
    yield node
    if depth < 12:
        for i in range(node.get_child_count()):
            child = node.get_child_at_index(i)
            if child:
                yield from nodes(child, depth + 1)

chooser = None
for _ in range(100):
    for i in range(root.get_child_count()):
        app = root.get_child_at_index(i)
        if app.get_process_id() != pid:
            continue
        for j in range(app.get_child_count()):
            window = app.get_child_at_index(j)
            if window.get_role_name() in ('file chooser', 'dialog') or window.get_name() not in ('IceTudio', ''):
                chooser = window
                break
    if chooser:
        break
    time.sleep(.1)
if not chooser:
    raise RuntimeError('No accessible GTK native chooser belonging to the test process; no other application was touched')

if mode == 'open':
    # GTK's location entry is revealed by Ctrl+L. The chooser belongs to our PID.
    chooser.get_component_iface().grab_focus()
    Atspi.generate_keyboard_event(37, None, Atspi.KeySynthType.PRESS)
    Atspi.generate_keyboard_event(0, 'l', Atspi.KeySynthType.STRING)
    Atspi.generate_keyboard_event(37, None, Atspi.KeySynthType.RELEASE)
    time.sleep(.15)

entries = []
for node in nodes(chooser):
    try:
        editable = node.get_editable_text_iface()
        state = node.get_state_set()
        if editable and state.contains(Atspi.StateType.EDITABLE) and state.contains(Atspi.StateType.SHOWING):
            entries.append(node)
    except Exception:
        pass
if not entries:
    raise RuntimeError('Native chooser has no accessible editable filename/location entry')
entry = entries[0]
entry.get_editable_text_iface().set_text_contents(path)
time.sleep(.15)
labels = ('保存', 'Save', '保存(_S)', '_Save') if mode == 'save' else ('打开', 'Open', '打开(_O)', '_Open')
buttons = [node for node in nodes(chooser) if node.get_role_name() in ('push button', 'button') and node.get_name() in labels]
if not buttons:
    raise RuntimeError('Native chooser accept button unavailable: ' + repr([(n.get_role_name(), n.get_name()) for n in nodes(chooser) if n.get_role_name() in ('push button', 'button')]))
buttons[-1].get_action_iface().do_action(0)
print(json.dumps({'chooser': chooser.get_name(), 'mode': mode, 'entry': entry.get_name(), 'path': path}, ensure_ascii=False))
