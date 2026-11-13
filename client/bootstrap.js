import {
  readCache,
  writeCache,
  captureMaterials,
  restoreMaterials,
  hydrateArmoury,
} from "./startup-cache.js";

const base = new URL("../", import.meta.url);
const version = document.documentElement.dataset.bundle || "development";
const baking = new URLSearchParams(location.search).has("bake");
const key = `materials-v1:${version}`;
const shaderJobs = [];
const stats = {
  version,
  materials: "miss",
  armoury: "fallback",
  cacheReadMs: 0,
  shaderJobs: 0,
};
const start = performance.now();
const boot = (globalThis.GHBoot = {
  stats,
  materials: null,
  armoury: null,
  swatches: new Map(),
  restoreCanvas(record) {
    const canvas = document.createElement("canvas");
    canvas.width = record.width;
    canvas.height = record.height;
    canvas
      .getContext("2d")
      .putImageData(
        new ImageData(
          new Uint8ClampedArray(
            record.data.buffer,
            record.data.byteOffset,
            record.data.byteLength,
          ),
          record.width,
          record.height,
        ),
        0,
        0,
      );
    return canvas;
  },
  captureMaterials(value) {
    this.pendingMaterials = value;
  },
  restoreMaterials(create, anisotropy) {
    if (!this.materials) return null;
    try {
      const result = restoreMaterials(this.materials, create, anisotropy);
      if (result) stats.materials = "hit";
      this.materials = null;
      return result;
    } catch {
      this.materials = null;
      stats.materials = "invalid";
      return null;
    }
  },
  hydrateArmoury(studio) {
    try {
      const hit = hydrateArmoury(studio, this.armoury);
      if (hit) stats.armoury = "prebaked";
      return hit;
    } catch {
      return false;
    }
  },
  trackShader(job) {
    stats.shaderJobs++;
    shaderJobs.push(Promise.resolve(job).catch(() => {}));
  },
  async finishShaders() {
    await Promise.all(shaderJobs.splice(0));
  },
});
// Browser HTTP cache can fetch/parse the stable, versioned bundle while local
// material data and pre-baked icons load in parallel.
const game = new URL("dist/game.js", base);
const preload = document.createElement("link");
preload.rel = "preload";
preload.as = "script";
preload.href = game.href;
document.head.append(preload);
if (!baking) {
  await Promise.allSettled([
    Promise.race([
      readCache(key),
      new Promise((resolve) => setTimeout(resolve, 1200)),
    ]).then((value) => {
      boot.materials = value || null;
    }),
    fetch(new URL("assets/armoury.json", base), {
      signal: AbortSignal.timeout(1500),
    }).then(async (response) => {
      if (response.ok) {
        const pack = await response.json();
        if (pack.source !== version) return;
        boot.armoury = pack;
        await Promise.allSettled(
          (pack.swatches || []).map(async ([key, url]) => {
            if (!/^data:image\/png;base64,/.test(url)) return;
            const image = await createImageBitmap(
              await (await fetch(url)).blob(),
            );
            boot.swatches.set(key, image);
          }),
        );
        stats.swatches = boot.swatches.size;
      }
    }),
  ]);
}
stats.cacheReadMs = Math.round(performance.now() - start);
const script = document.createElement("script");
script.src = game.href;
script.onerror = () => {
  document.querySelector("#ltxt").textContent = "游戏资源加载失败，请刷新重试";
};
document.body.append(script);
await Promise.all([import("./telemetry.js"), import("./pvp.js")]);
while (!globalThis.GH?.ready)
  await new Promise((resolve) => setTimeout(resolve, 100));
if (!baking && boot.pendingMaterials) {
  const save = () => {
    try {
      const pack = captureMaterials(boot.pendingMaterials);
      boot.pendingMaterials = null;
      writeCache(key, pack)
        .then(() => {
          stats.materialsSaved = pack.textures.length;
          stats.materialBytes = pack.bytes;
        })
        .catch(() => {
          stats.cacheWrite = "unavailable";
        });
    } catch {
      boot.pendingMaterials = null;
      stats.cacheWrite = "unavailable";
    }
  };
  if (globalThis.requestIdleCallback)
    requestIdleCallback(save, { timeout: 5000 });
  else setTimeout(save, 1000);
}
