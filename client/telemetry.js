import { AdaptiveQuality } from "./performance-policy.js";

// Local diagnostics only: no uploads, no extra RAF loop and bounded storage.
const report = document.createElement("output");
report.id = "gh-performance";
report.hidden = true;
document.body.append(report);
async function install() {
  while (!window.GH?.ready) await new Promise((r) => setTimeout(r, 100));
  const { GH } = window;
  const d = GH.debug,
    gfx = d.gfx,
    renderer = gfx.renderer;
  const gl = renderer.getContext();
  const startup = document.createElement("output");
  startup.id = "gh-startup";
  startup.hidden = true;
  const startupState = {
    readyMs: Math.round(performance.now()),
    stages: d.boot,
    armouryMs: d.ctx.hud.studio?.bakeMs,
    swatchesMs: d.ctx.hud.warmMs,
    cache: globalThis.GHBoot?.stats,
    transferredBytes: performance
      .getEntriesByType("resource")
      .reduce((sum, item) => sum + item.transferSize, 0),
  };
  startup.textContent = JSON.stringify(startupState);
  document.body.append(startup);
  const extension = gl.getExtension("WEBGL_debug_renderer_info");
  const gpu = extension
    ? gl.getParameter(extension.UNMASKED_RENDERER_WEBGL)
    : gl.getParameter(gl.RENDERER);
  const software = /swiftshader|llvmpipe|software rasterizer|softpipe/i.test(
    gpu,
  );
  const policy = new AdaptiveQuality(performance.now());
  const samples = new Float32Array(600);
  let used = 0,
    index = 0,
    last = 0,
    lastMode = "",
    renderMs = 0;
  function reset() {
    used = index = 0;
    last = 0;
  }
  const originalQuality = gfx.setQuality.bind(gfx);
  function apply(quality, scale) {
    // A resolution-only adjustment can resize existing targets without
    // rebuilding every post-processing pass or recompiling materials.
    if (quality !== gfx.quality || scale === undefined)
      originalQuality(quality);
    if (scale !== undefined && scale !== gfx.q.scale) {
      gfx.q = { ...gfx.q, scale };
      gfx.resize();
    }
    reset();
  }
  gfx.setQuality = (value) => {
    const thin =
      software ||
      navigator.hardwareConcurrency <= 4 ||
      navigator.deviceMemory <= 4;
    const target = value === "auto" ? (thin ? "low" : "medium") : value;
    if (target !== gfx.quality || value !== "auto" || gfx.q.scale < 0.7)
      apply(target);
    policy.reset(performance.now());
  };
  gfx.setQuality(d.ctx.settings.quality);

  const render = gfx.render;
  gfx.render = function (dt) {
    const now = performance.now(),
      mode = GH.G.state;
    if (mode !== lastMode || document.hidden) {
      reset();
      lastMode = mode;
    }
    if (last && !document.hidden) {
      samples[index++ % samples.length] = now - last;
      used = Math.min(used + 1, samples.length);
    }
    last = now;
    const start = performance.now();
    render.call(this, dt);
    renderMs = performance.now() - start;
  };

  // Minimap DOM/layout work does not need to run at the aiming frame rate.
  const map = d.ctx.hud.map,
    updateMap = map.update;
  let mapTime = 0;
  map.update = function (dt, ...args) {
    mapTime += dt;
    if (mapTime < 0.1) return;
    updateMap.call(this, mapTime, ...args);
    mapTime = 0;
  };
  // PvP debris/props are cosmetic, separate from authoritative collision.
  // Offline grenades retain the original update path.
  const physics = d.physics,
    updatePhysics = physics.update;
  let physicsTime = 0;
  physics.update = function (dt) {
    if (
      gfx.quality === "low" &&
      document.body.classList.contains("online-session")
    ) {
      physicsTime += dt;
      if (physicsTime < 1 / 30) return;
      dt = Math.min(physicsTime, 1 / 15);
    }
    physicsTime = 0;
    updatePhysics.call(this, dt);
  };
  setInterval(() => {
    startup.textContent = JSON.stringify(startupState);
    const values = Array.from(samples.subarray(0, used)).sort((a, b) => a - b);
    const mean = used ? values.reduce((a, b) => a + b, 0) / used : 0;
    const p95 = values[Math.floor(values.length * 0.95)] || 0;
    const state = {
      fps: mean ? Math.round(1000 / mean) : 0,
      frameMs: +mean.toFixed(2),
      p95Ms: +p95.toFixed(2),
      samples: used,
      renderSubmitMs: +renderMs.toFixed(2),
      gpu,
      software,
      webgl: gl.getParameter(gl.VERSION),
      automatic: d.ctx.settings.quality === "auto",
      quality: gfx.quality,
      scale: gfx.q.scale,
      motionBlur: gfx.motionBlur,
      buffer: [gl.drawingBufferWidth, gl.drawingBufferHeight],
      calls: renderer.info.render.calls,
      triangles: renderer.info.render.triangles,
      geometries: renderer.info.memory.geometries,
      textures: renderer.info.memory.textures,
      physicsBodies: physics.bodies.length,
      actors: d.ctx.match.actors.length,
      mode: GH.G.state,
      network: d.network,
    };
    report.textContent = JSON.stringify(state);
    const panel = d.ctx.hud.menus.pages.settings.querySelector(
      '.st-panel[data-tab="graphics"] .st-side',
    );
    if (panel && d.ctx.hud.menus.page === "settings") {
      let status = panel.querySelector(".gh-device");
      if (!status) {
        status = document.createElement("p");
        status.className = "gh-device";
        panel.append(status);
      }
      status.textContent = `${software ? "软件渲染" : "显卡加速"} · ${state.fps} FPS · ${state.buffer.join(" × ")} · ${state.automatic ? "自动 / " : ""}${{ low: "低", medium: "中", high: "高", ultra: "极高" }[state.quality]}\n${gpu}\n本次加载 ${(startupState.readyMs / 1000).toFixed(1)} 秒${startupState.cache?.materials === "hit" ? " · 本地材质缓存" : ""}${startupState.cache?.armoury === "prebaked" ? " · 预生成武器图像" : ""}`;
      status.style.overflowWrap = "anywhere";
    }
    const next = policy.observe({
      now: performance.now(),
      automatic: state.automatic,
      playing: state.mode === "playing",
      hidden: document.hidden,
      count: used,
      mean,
      p95,
      quality: state.quality,
      scale: state.scale,
    });
    if (next) apply(next.quality, next.scale);
    reset();
  }, 2000);
}
install().catch((error) => console.error("Performance controller:", error));
