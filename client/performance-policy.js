// Only sustained slow gameplay can lower automatic quality. Startup, menus,
// hidden tabs and manually selected presets must never trigger a downgrade.
export class AdaptiveQuality {
  constructor(now = 0) {
    this.reset(now);
  }
  reset(now) {
    this.slow = 0;
    this.changedAt = now;
  }
  observe({
    now,
    automatic,
    playing,
    hidden,
    count,
    mean,
    p95,
    quality,
    scale,
  }) {
    if (!automatic || !playing || hidden || count < 4) {
      this.slow = 0;
      return null;
    }
    if (now - this.changedAt < 8000) return null;
    this.slow = mean > 23 || p95 > 34 ? this.slow + 1 : 0;
    if (this.slow < 2) return null;
    this.reset(now);
    if (quality !== "low") return { quality: "low", scale: 0.7 };
    if (scale > 0.56) return { quality: "low", scale: 0.55 };
    if (scale > 0.46) return { quality: "low", scale: 0.45 };
    return null;
  }
}
