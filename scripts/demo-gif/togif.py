import json, sys, glob
from PIL import Image
d = sys.argv[1]; out = sys.argv[2]
durs = json.load(open(d + "/durations.json"))
files = sorted(glob.glob(d + "/*.png"))
frames = [Image.open(f).convert("RGB") for f in files]
# One shared palette keeps colours stable across frames and the file small.
pal = frames[len(frames)//2].quantize(colors=255, method=Image.Quantize.MEDIANCUT)
q = [f.quantize(palette=pal, dither=Image.Dither.NONE) for f in frames]
ms = [max(int(t * 1000), 20) for t in durs]
ms[-1] = max(ms[-1], 2500)
q[0].save(out, save_all=True, append_images=q[1:], duration=ms, loop=0, optimize=True, disposal=1)
print(len(q), "frames", sum(ms)/1000, "s")
