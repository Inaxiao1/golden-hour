import { test } from "node:test";
import assert from "node:assert/strict";
import { decodeSnapshot } from "./net.js";
import { World, motion } from "./simulation.js";

test("reject malformed, truncated, future-version and nonfinite snapshots", () => {
  assert.equal(decodeSnapshot(new Uint8Array(12)), null);
  const b = new Uint8Array(88),
    v = new DataView(b.buffer);
  v.setUint16(0, 0x4748, true);
  v.setUint8(2, 1);
  v.setUint8(3, 2);
  v.setUint8(13, 1);
  assert.equal(decodeSnapshot(b).players.length, 1);
  assert.equal(decodeSnapshot(b.subarray(0, 87)), null);
  v.setFloat32(32, NaN, true);
  assert.equal(decodeSnapshot(b), null);
  v.setFloat32(32, 0, true);
  v.setUint8(2, 2);
  assert.equal(decodeSnapshot(b), null);
});
test("decode authoritative flags, sequence, weapons and countdown", () => {
  const b = new Uint8Array(88),
    v = new DataView(b.buffer);
  v.setUint16(0, 0x4748, true);
  v.setUint8(2, 1);
  v.setUint8(3, 2);
  v.setUint8(13, 1);
  v.setUint8(12, 1);
  v.setUint32(18, 3000, true);
  v.setUint32(24, 42, true);
  v.setUint32(28, 99, true);
  v.setFloat32(32, -12.5, true);
  v.setUint8(81, 1 | 2 | 8 | 64);
  v.setUint8(82, 2);
  const s = decodeSnapshot(b);
  assert.equal(s.phase, "countdown");
  assert.equal(s.remaining, 3000);
  assert.equal(s.players[0].id, 42);
  assert.equal(s.players[0].ack, 99);
  assert.equal(s.players[0].x, -12.5);
  assert.equal(s.players[0].weapon, "shotgun");
  assert.ok(s.players[0].crouch);
  assert.ok(s.players[0].connected);
  assert.ok(s.players[0].bot);
});
const floor = { min: [-100, -1, -100], max: [100, 0, 100] };
test("client prediction matches Go fixed-step conformance trace", () => {
  const w = new World([floor]),
    m = motion({ x: 0, y: 0.01, z: 0 });
  for (let i = 0; i < 120; i++)
    w.move(m, { forward: 1, right: 0, yaw: 0, buttons: 0 });
  assert.ok(Math.abs(m.z - -8.943333333333333) < 1e-9);
  assert.equal(m.y, 0);
});
test("prediction stops at walls and normalizes diagonal movement", () => {
  const w = new World([floor, { min: [-2, 0, -4], max: [2, 3, -3] }]),
    m = motion({ x: 0, y: 0.01, z: 0 });
  for (let i = 0; i < 120; i++)
    w.move(m, { forward: 1, right: 0, yaw: 0, buttons: 0 });
  assert.ok(m.z >= -2.681);
  const a = motion({ x: 0, y: 0.01, z: 0 }),
    b = motion(a),
    open = new World([floor]);
  for (let i = 0; i < 60; i++) {
    open.move(a, { forward: 1, right: 0, yaw: 0, buttons: 0 });
    open.move(b, { forward: 1, right: 1, yaw: 0, buttons: 0 });
  }
  assert.ok(Math.abs(Math.hypot(a.x, a.z) - Math.hypot(b.x, b.z)) < 1e-9);
});
