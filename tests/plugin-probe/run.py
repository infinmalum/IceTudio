#!/usr/bin/env python3
"""Exercise the isolated Linux worker against the six pinned plugin modules."""

import argparse
import hashlib
import os
from pathlib import Path
import shlex
import select
import signal
import subprocess
from tempfile import TemporaryDirectory


def worker(deps, scratch, path, *args, timeout=15, mapping=None):
    # VST3 locates its .so and resource root from the bundle name.
    plugin_path = f"/plugins/{path.name}"
    command = [
        "prlimit", "--core=0", "--fsize=8388608", "--as=4294967296", "--", "bwrap",
        "--unshare-all", "--die-with-parent", "--clearenv",
        "--setenv", "HOME", "/tmp", "--setenv", "PATH", "/usr/bin",
        "--ro-bind", "/usr", "/usr",
        "--symlink", os.readlink("/lib"), "/lib", "--symlink", os.readlink("/lib64"), "/lib64",
        "--ro-bind", str(path.parent), "/plugins",
        "--ro-bind", str(deps / "build" / "icetudio-plugin-probe"), "/probe",
        "--bind" if args[1] == "save" else "--ro-bind", str(scratch), "/state",
        "--proc", "/proc", "--dev", "/dev",
        "--tmpfs", "/tmp", "--chdir", "/tmp",
    ]
    probe_args = ["/probe", *args[:2], plugin_path, *args[2:]]
    if args[1] == "editor":
        etc = scratch / "etc"
        if not etc.exists():
            etc.mkdir()
            (etc / "passwd").write_text(f"probe:x:{os.getuid()}:{os.getgid()}:Probe:/tmp:/usr/bin/sh\n")
            (etc / "group").write_text(f"probe:x:{os.getgid()}:\n")
            (etc / "machine-id").write_bytes(subprocess.check_output(["dbus-uuidgen"]))
        session = etc / "editor-session"
        session.write_text("#!/usr/bin/sh\nexec " + shlex.join(probe_args) + "\n")
        session.chmod(0o700)
        command += [
            "--ro-bind", str(session), "/editor-session",
            "--dir", "/etc",
            *[part for name in ("passwd", "group", "machine-id") for part in
              ("--ro-bind", str(etc / name), f"/etc/{name}")],
            "--ro-bind", "/etc/fonts", "/etc/fonts", "--tmpfs", "/var/lib/xkb",
            "--symlink", os.readlink("/bin"), "/bin",
            "--setenv", "XDG_RUNTIME_DIR", "/tmp/runtime",
            "--setenv", "WAYLAND_DISPLAY", "probe-wayland", "--setenv", "KWIN_COMPOSE", "Q",
            "--ro-bind", str(Path(__file__).with_name("virtual-display.sh").resolve()), "/virtual-display",
            "/usr/bin/sh", "/virtual-display",
        ]
    else:
        command += probe_args
    with TemporaryDirectory(prefix="ice-probe-log-") as log_dir:
        log_path = Path(log_dir) / "output"
        timed_out = False
        with log_path.open("wb") as log:
            with subprocess.Popen(command, stdin=mapping if mapping is not None else subprocess.DEVNULL,
                                  stdout=log, stderr=subprocess.STDOUT, start_new_session=True) as child:
                try:
                    child.wait(timeout)
                except subprocess.TimeoutExpired:
                    try:
                        os.killpg(child.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                    child.wait()
                    timed_out = True
        with log_path.open("rb") as log:
            output = log.read(65536).decode(errors="replace")
    print(f"{path.name} {' '.join(args[:2])}: exit={child.returncode} timeout={timed_out}", flush=True)
    if output:
        print(output.rstrip(), flush=True)
    return child.returncode, timed_out, output


def success(result):
    code, timed_out, output = result
    if code or timed_out:
        raise RuntimeError("worker failed")
    return output


def plugin_id(output, expected):
    for line in output.splitlines():
        if line.startswith("plugin "):
            _, identifier, name = shlex.split(line)
            if name == expected:
                return identifier
    raise RuntimeError(f"{expected} missing from scan output")


def transport(deps, scratch, path, format_, identity, crash=False):
    mapping = os.memfd_create("ice-probe-blocks", os.MFD_ALLOW_SEALING)
    executable = deps / "build" / "icetudio-plugin-probe"
    try:
        command = [str(executable), "transport-host", str(mapping), "crash" if crash else "stall"]
        with subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True,
                              pass_fds=(mapping,)) as host:
            try:
                ready, _, _ = select.select([host.stdout], [], [], 5)
                if not ready or host.stdout.readline().strip() != "transport mapping ready":
                    raise RuntimeError("transport mapping did not become ready")
                action = "transport-crash" if crash else "transport"
                result = worker(deps, scratch, path, format_, action, identity, "/state/unused", mapping=mapping)
                if crash:
                    code, timed_out, output = result
                    if timed_out or code not in (-signal.SIGABRT, 128 + signal.SIGABRT) or "transport crash at block=" not in output:
                        raise RuntimeError("transport worker did not exit at the injected fault")
                else:
                    success(result)
                output, _ = host.communicate(timeout=5)
                print(output.rstrip(), flush=True)
                if host.returncode:
                    raise RuntimeError("transport callback simulation failed")
            finally:
                if host.poll() is None:
                    host.kill()
    finally:
        os.close(mapping)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("deps", type=Path)
    parser.add_argument("--editors", action="store_true", help="also open editors on isolated Xwayland")
    parser.add_argument("--transport", action="store_true", help="also test paced shared audio buffers")
    parser.add_argument("--latency", action="store_true", help="also test restart and latency with fixtures")
    args = parser.parse_args()
    deps = args.deps.resolve(strict=True)
    with TemporaryDirectory(prefix="ice-probe-state-") as state_dir:
        scratch = Path(state_dir)
        fault = deps / "build" / "scan-fault.clap"
        code, timeout, _ = worker(deps, scratch, fault, "clap", "scan")
        if timeout or code not in (-signal.SIGABRT, 128 + signal.SIGABRT):
            raise RuntimeError("scanner did not report the injected abort")
        if args.latency:
            for format_, filename in (("clap", "latency-fixture.clap"), ("vst3", "latency-vst3.vst3")):
                fixture = deps / "build" / filename
                identity = plugin_id(success(worker(deps, scratch, fixture, format_, "scan")), "IceTudio Latency Fixture")
                success(worker(deps, scratch, fixture, format_, "latency", identity, "/state/unused"))
        for format_, folder, name in [
            ("clap", "dexed", "Dexed"), ("vst3", "dexed", "Dexed"),
            ("clap", "surge", "Surge XT"), ("vst3", "surge", "Surge XT"),
            ("clap", "surge", "Surge XT Effects"), ("vst3", "surge", "Surge XT Effects"),
        ]:
            path = deps / folder / f"{name}.{format_}"
            identity = plugin_id(success(worker(deps, scratch, path, format_, "scan")), name)
            if args.editors:
                editor = success(worker(deps, scratch, path, format_, "editor", identity, "/state/unused"))
                if editor.count("editor visible=1") != 2 or editor.count("editor visible=0") != 2:
                    raise RuntimeError("editor did not complete two open/close cycles")
            if args.transport and name in ("Dexed", "Surge XT Effects"):
                transport(deps, scratch, path, format_, identity)
                if name == "Dexed":
                    transport(deps, scratch, path, format_, identity, crash=True)
            state_name = f"{folder}-{name}-{format_}.state"
            state = scratch / state_name
            state_arg = f"/state/{state_name}"
            parameter = ("Cutoff", "0.375") if name == "Dexed" else ()
            success(worker(deps, scratch, path, format_, "save", identity, state_arg, *parameter))
            checksum = hashlib.sha256(state.read_bytes()).hexdigest()
            success(worker(deps, scratch, path, format_, "restore", identity, state_arg, *parameter))
            if hashlib.sha256(state.read_bytes()).hexdigest() != checksum:
                raise RuntimeError("restore unexpectedly wrote to the source state")
            if name != "Dexed":
                continue
            code, timeout, output = worker(deps, scratch, path, format_, "abort", identity, state_arg)
            if timeout or code not in (-signal.SIGABRT, 128 + signal.SIGABRT) or "processed first block" not in output:
                raise RuntimeError("render worker did not report the injected abort")
            _, timeout, output = worker(deps, scratch, path, format_, "hang", identity, state_arg, timeout=3)
            if not timeout or "processed first block" not in output:
                raise RuntimeError("render worker did not time out after processing")
            success(worker(deps, scratch, path, format_, "restore", identity, state_arg, *parameter))
            if hashlib.sha256(state.read_bytes()).hexdigest() != checksum:
                raise RuntimeError("worker fault modified the last saved state")
    print("plugin probe matrix passed")


if __name__ == "__main__":
    main()
