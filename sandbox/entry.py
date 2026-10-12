"""Entrypoint of the sandbox container.

Protocol (no host mounts, no network):
  stdin  : a tar stream with `main.py` (the approved code) and `in/<name>`
           (copies of the approved input files).
  stdout : a tar stream with `meta.json`, `stdout.txt`, `stderr.txt` and
           `out/<name>` (files the code wrote to /work/out).

The user's program runs as a child process in its own process group with its
stdout/stderr redirected to files, a wall-clock timeout and RLIMIT_FSIZE. The
parent only reads the result files once the child is gone. The runner does not
trust anything in the result: it re-validates names, sizes and extensions.
"""
import json
import os
import resource
import shutil
import signal
import subprocess
import sys
import tarfile
import threading
import time

W = "/work"


def env_int(name, default):
    try:
        return int(os.environ.get(name, default))
    except ValueError:
        return default


class CappedReader:
    """Drains a pipe in a thread, keeping at most `cap` bytes (head and tail when it
    is larger); everything else is read and discarded so the child never blocks."""

    def __init__(self, pipe, cap):
        self.cap, self.half = cap, max(cap // 2, 1)
        self.head, self.tail = bytearray(), bytearray()
        self.total = 0
        self.t = threading.Thread(target=self._run, args=(pipe,), daemon=True)
        self.t.start()

    def _run(self, pipe):
        while True:
            chunk = pipe.read1(65536) if hasattr(pipe, "read1") else pipe.read(65536)
            if not chunk:
                break
            self.total += len(chunk)
            if len(self.head) < self.half:
                take = self.half - len(self.head)
                self.head += chunk[:take]
                chunk = chunk[take:]
            if chunk:
                self.tail += chunk
                if len(self.tail) > self.half:
                    del self.tail[: len(self.tail) - self.half]
        pipe.close()

    def result(self):
        """Return (text, truncated)."""
        self.t.join(timeout=5)
        if self.total <= self.cap:
            return (bytes(self.head) + bytes(self.tail)).decode("utf-8", "replace"), False
        return bytes(self.head).decode("utf-8", "replace") + "\n...[truncated]...\n" + bytes(self.tail).decode("utf-8", "replace"), True


def add_bytes(tf, name, data):
    info = tarfile.TarInfo(name)
    info.size = len(data)
    info.mtime = int(time.time())
    info.mode = 0o600
    import io

    tf.addfile(info, io.BytesIO(data))


def main():
    t0 = time.time()
    timeout = env_int("SBX_TIMEOUT", 60)
    max_file = env_int("SBX_MAX_FILE", 5 << 20)
    max_stdio = env_int("SBX_MAX_STDIO", 64 << 10)
    max_files = env_int("SBX_MAX_OUT_FILES", 10)
    max_total = env_int("SBX_MAX_OUT_BYTES", 10 << 20)
    for d in ("in", "out", ".mpl"):
        os.makedirs(os.path.join(W, d), exist_ok=True)

    with tarfile.open(fileobj=sys.stdin.buffer, mode="r|") as tf:
        for m in tf:
            if not m.isreg():
                continue
            if m.name == "main.py":
                dest = os.path.join(W, "main.py")
            elif m.name.startswith("in/") and "/" not in m.name[3:] and m.name[3:] not in ("", ".", ".."):
                dest = os.path.join(W, "in", m.name[3:])
            else:
                continue
            src = tf.extractfile(m)
            with open(dest, "wb") as out:
                shutil.copyfileobj(src, out)

    def child_setup():
        os.setsid()
        resource.setrlimit(resource.RLIMIT_FSIZE, (max_file, max_file))
        resource.setrlimit(resource.RLIMIT_CORE, (0, 0))

    child_env = {
        "PATH": "/usr/local/bin:/usr/bin:/bin",
        "HOME": W,
        "MPLBACKEND": "Agg",
        "MPLCONFIGDIR": os.path.join(W, ".mpl"),
        "PYTHONDONTWRITEBYTECODE": "1",
        "PYTHONUNBUFFERED": "1",
        "LANG": "C.UTF-8",
    }
    # stdout/stderr are read through pipes into memory (capped, the excess is
    # drained and discarded), never through files: code that fills the /work
    # tmpfs must not be able to erase the record of what it did.
    timed_out = False
    sig = 0
    proc = subprocess.Popen(
        [sys.executable, "-B", os.path.join(W, "main.py")],
        cwd=W, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        env=child_env, close_fds=True, preexec_fn=child_setup,
    )
    out_cap, err_cap = CappedReader(proc.stdout, max_stdio), CappedReader(proc.stderr, max_stdio)
    try:
        rc = proc.wait(timeout=timeout)
    except subprocess.TimeoutExpired:
        timed_out = True
        try:
            os.killpg(proc.pid, signal.SIGKILL)
        except OSError:
            pass
        proc.wait()
        rc = -9
    try:  # no stragglers outlive the run
        os.killpg(proc.pid, signal.SIGKILL)
    except OSError:
        pass
    if rc < 0:
        sig, rc = -rc, 128 - rc

    stdout, so_trunc = out_cap.result()
    stderr, se_trunc = err_cap.result()

    files, dropped, total = [], [], 0
    outdir = os.path.join(W, "out")
    for name in sorted(os.listdir(outdir)):
        p = os.path.join(outdir, name)
        try:
            st = os.lstat(p)
        except OSError:
            continue
        if name.startswith(".") or not os.path.isfile(p) or os.path.islink(p):
            dropped.append(name + ": not a regular file")
            continue
        if len(files) >= max_files or total + st.st_size > max_total or st.st_size > max_file:
            dropped.append(name + ": over the output limits")
            continue
        total += st.st_size
        files.append((name, p))

    meta = {
        "exit_code": rc, "signal": sig, "timed_out": timed_out,
        "duration_ms": int((time.time() - t0) * 1000),
        "stdout_truncated": so_trunc, "stderr_truncated": se_trunc, "dropped": dropped,
    }
    with tarfile.open(fileobj=sys.stdout.buffer, mode="w|") as tf:
        add_bytes(tf, "meta.json", json.dumps(meta).encode())
        add_bytes(tf, "stdout.txt", stdout.encode())
        add_bytes(tf, "stderr.txt", stderr.encode())
        for name, p in files:
            tf.add(p, arcname="out/" + name, recursive=False)
    sys.stdout.buffer.flush()


if __name__ == "__main__":
    main()
