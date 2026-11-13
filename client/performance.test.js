import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { AdaptiveQuality } from "./performance-policy.js";
import { World, motion } from "./simulation.js";
import { encodeInput } from "./net.js";

test("binary input matches Go wire fixture and safely reuses a buffer", () => {
  const input = {
    seq: 42,
    forward: 1,
    right: -0.5,
    yaw: 0.5,
    pitch: -0.25,
    buttons: 10,
  };
  const packet = encodeInput(input);
  assert.equal(
    Buffer.from(packet).toString("hex"),
    "484701012a0000000000803f000000bf0000003f000080be0a",
  );
  assert.equal(packet.byteLength, 25);
  const backing = new Uint8Array(35);
  assert.equal(encodeInput(input, backing.subarray(5, 30)).byteOffset, 5);
  assert.deepEqual(backing.subarray(5, 30), packet);
});

test("auto quality ignores startup/manual/hidden/menu and isolated spikes", () => {
  const policy = new AdaptiveQuality(0);
  const sample = {
    now: 9000,
    automatic: true,
    playing: true,
    hidden: false,
    count: 100,
    mean: 30,
    p95: 40,
    quality: "medium",
    scale: 0.85,
  };
  assert.equal(policy.observe({ ...sample, now: 1000 }), null);
  for (const override of [
    { automatic: false },
    { playing: false },
    { hidden: true },
    { count: 3 },
  ]) {
    assert.equal(policy.observe({ ...sample, ...override }), null);
  }
  assert.equal(policy.observe(sample), null);
  assert.equal(policy.observe({ ...sample, mean: 16, p95: 20 }), null);
  assert.equal(policy.observe(sample), null);
  assert.deepEqual(policy.observe({ ...sample, now: 11000 }), {
    quality: "low",
    scale: 0.7,
  });
  assert.equal(policy.observe({ ...sample, now: 12000 }), null);
});

test("automatic resolution has cooldown and a hard floor", () => {
  const policy = new AdaptiveQuality(0);
  const sample = {
    automatic: true,
    playing: true,
    hidden: false,
    count: 100,
    mean: 40,
    p95: 55,
    quality: "low",
    scale: 0.7,
  };
  assert.equal(policy.observe({ ...sample, now: 9000 }), null);
  assert.deepEqual(policy.observe({ ...sample, now: 11000 }), {
    quality: "low",
    scale: 0.55,
  });
  assert.equal(policy.observe({ ...sample, now: 12000, scale: 0.55 }), null);
  assert.equal(policy.observe({ ...sample, now: 20000, scale: 0.55 }), null);
  assert.deepEqual(policy.observe({ ...sample, now: 22000, scale: 0.55 }), {
    quality: "low",
    scale: 0.45,
  });
  assert.equal(policy.observe({ ...sample, now: 32000, scale: 0.45 }), null);
  assert.equal(policy.observe({ ...sample, now: 34000, scale: 0.45 }), null);
});

test("very slow devices still trigger automatic quality", () => {
  const policy = new AdaptiveQuality(0);
  const sample = {
    now: 9000,
    automatic: true,
    playing: true,
    hidden: false,
    count: 9,
    mean: 200,
    p95: 250,
    quality: "medium",
    scale: 0.85,
  };
  assert.equal(policy.observe(sample), null);
  assert.deepEqual(policy.observe({ ...sample, now: 11000 }), {
    quality: "low",
    scale: 0.7,
  });
});

test("hotel prediction grid matches a full scan, including stamp wrap", () => {
  const { colliders } = JSON.parse(
    readFileSync(new URL("../internal/game/maps/hotel.json", import.meta.url)),
  );
  const grid = new World(colliders),
    brute = new World(colliders);
  brute.nearby = () => brute.colliders;
  grid.queryId = 0xffffffff;
  grid.seen.fill(1);
  const a = motion({ x: -24.5, y: 0, z: 4.25 }),
    b = motion(a);
  for (let i = 0; i < 1800; i++) {
    const input = {
      forward: Math.sin(i * 0.013),
      right: Math.cos(i * 0.023),
      yaw: i * 0.011,
      buttons: i % 80 < 10 ? 1 : i % 120 < 40 ? 4 : 2,
    };
    grid.move(a, input);
    brute.move(b, input);
    for (const k of ["x", "y", "z", "vx", "vy", "vz"])
      assert.ok(Math.abs(a[k] - b[k]) < 1e-9, `${k} at ${i}`);
    assert.equal(a.ground, b.ground);
    assert.equal(a.crouch, b.crouch);
  }
});
