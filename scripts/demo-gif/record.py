"""Record nagare in a pseudo-terminal, as frames for render.js and togif.py.

    python3 record.py '["./nagare-go","demo","--speed","1.5"]' '<steps json>'

Runs the command in a pty rendered by pyte (a terminal emulator in Python),
drives it with the steps, and writes the distinct frames with their durations to
$OUT (default frames.json). Steps: ["rec", seconds], ["rkey", name],
["rtype", text], ["wait", seconds], ["key", name], ["type", text].

pyte lacks REP and SU/SD, which Bubble Tea's renderer uses; they are added
below, or rows go missing from the frames.
"""
import os, pty, select, time, struct, fcntl, termios, html, sys, json, pyte
W, H = int(os.environ.get("W", 132)), int(os.environ.get("H", 34))
class RepScreen(pyte.Screen):
    """pyte lacks REP (CSI b, repeat the last graphic character), which Bubble
    Tea's renderer uses for runs of identical cells."""
    last = " "
    def draw(self, data):
        if data:
            self.last = data[-1]
        super().draw(data)
    def repeat_last(self, count=1, *a, **k):
        super().draw(self.last * max(count, 1))
    def scroll_up(self, count=1, *a, **k):
        top, bottom = (self.margins.top, self.margins.bottom) if self.margins else (0, self.lines - 1)
        for _ in range(max(count, 1)):
            for y in range(top, bottom):
                self.buffer[y] = self.buffer[y + 1].copy()
            self.buffer[bottom].clear()
        self.dirty.update(range(self.lines))
    def scroll_down(self, count=1, *a, **k):
        top, bottom = (self.margins.top, self.margins.bottom) if self.margins else (0, self.lines - 1)
        for _ in range(max(count, 1)):
            for y in range(bottom, top, -1):
                self.buffer[y] = self.buffer[y - 1].copy()
            self.buffer[top].clear()
        self.dirty.update(range(self.lines))
pyte.Stream.csi["b"] = "repeat_last"
pyte.Stream.csi["S"] = "scroll_up"
pyte.Stream.csi["T"] = "scroll_down"
screen = RepScreen(W, H); stream = pyte.ByteStream(screen)
cmd = json.loads(sys.argv[1]); steps = json.loads(sys.argv[2])
pid, fd = pty.fork()
if pid == 0:
    env = dict(os.environ, HOME=os.environ.get("RECORD_HOME", os.environ["HOME"]), TERM="xterm-256color", COLORTERM="truecolor", SHELL="/bin/bash")
    env.pop("TMUX", None)
    os.execve(cmd[0], cmd, env)
fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", H, W, 0, 0))
def pump(t):
    end = time.time() + t
    while time.time() < end:
        r, _, _ = select.select([fd], [], [], 0.03)
        if r:
            try: data = os.read(fd, 1 << 16)
            except OSError: return
            if b"\x1b]11;?" in data: os.write(fd, b"\x1b]11;rgb:1a1a/1b1b/2626\x1b\\")
            if b"\x1b[c" in data: os.write(fd, b"\x1b[?62;22c")
            stream.feed(data)
NAMED = {"black":"15161e","red":"f7768e","green":"9ece6a","brown":"e0af68","yellow":"e0af68","blue":"7aa2f7",
         "magenta":"bb9af7","cyan":"7dcfff","white":"a9b1d6","default":None}
def col(c, d): return (NAMED[c] or d) if c in NAMED else c
def shot(name):
    rows = []
    for y in range(H):
        line = screen.buffer[y]; out = []
        for x in range(W):
            ch = line[x]
            fg, bg = col(ch.fg, "c0caf5"), col(ch.bg, "1a1b26")
            if ch.reverse: fg, bg = bg, fg
            out.append('<span style="color:#%s;background:#%s;%s">%s</span>' % (fg, bg, "font-weight:bold;" if ch.bold else "", html.escape(ch.data or " ")))
        rows.append('<div class="r">' + "".join(out) + '</div>')
    open("%s.html" % name, "w").write('<html><head><style>body{margin:0;background:#1a1b26}.t{font-family:"DejaVu Sans Mono",monospace;font-size:13px;margin:10px;white-space:pre}.r{height:17px;line-height:17px;overflow:hidden}.r span{display:inline-block;height:17px}</style></head><body><div class="t">%s</div></body></html>' % "".join(rows))
    open("%s.txt" % name, "w").write("\n".join(screen.display))
FPS = float(os.environ.get("FPS", 8))
frames = []  # (html, duration_seconds)
def frame_html():
    rows = []
    for y in range(H):
        line = screen.buffer[y]; out = []
        run_style, run_text = None, []
        for x in range(W):
            ch = line[x]
            fg, bg = col(ch.fg, "c0caf5"), col(ch.bg, "1a1b26")
            if ch.reverse: fg, bg = bg, fg
            st = "color:#%s;background:#%s;%s" % (fg, bg, "font-weight:bold;" if ch.bold else "")
            out.append('<span style="%s">%s</span>' % (st, html.escape(ch.data or " ")))
        rows.append('<div class="r">' + "".join(out) + '</div>')
    return "".join(rows)
def record(t):
    end = time.time() + t
    while time.time() < end:
        pump(1.0 / FPS)
        h = frame_html()
        if frames and frames[-1][0] == h:
            frames[-1][1] += 1.0 / FPS
        else:
            frames.append([h, 1.0 / FPS])
KEYS = {"enter": b"\r", "f4": b"\x1bOS", "alt+s": b"\x1bs", "alt+z": b"\x1bz", "alt+down": b"\x1b[1;3B", "ctrl+]": b"\x1d", "esc": b"\x1b", "tab": b"\t", "f1": b"\x1bOP", "down": b"\x1b[B", "up": b"\x1b[A"}
for st in steps:
    kind, val = st[0], st[1]
    if kind == "wait": pump(val)
    elif kind == "key": os.write(fd, KEYS[val]); pump(0.3)
    elif kind == "type": os.write(fd, val.encode()); pump(0.3)
    elif kind == "shot": shot(val)
    elif kind == "rec": record(val)
    elif kind == "rkey": os.write(fd, KEYS[val]); record(0.35)
    elif kind == "rtype":
        if val.startswith("\x1b") or len(val) == 1:
            os.write(fd, val.encode()); record(0.3)   # a key chord arrives in one burst
        else:
            for ch in val:
                os.write(fd, ch.encode()); record(0.07)
if frames:
    json.dump({"W": W, "H": H, "frames": frames}, open(os.environ.get("OUT", "frames.json"), "w"))
os.kill(pid, 15); pump(1.0)
try: os.kill(pid, 9)
except: pass
