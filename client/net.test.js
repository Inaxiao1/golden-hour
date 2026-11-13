import { test } from "node:test";
import assert from "node:assert/strict";
import { Connection, decodeSnapshot } from "./net.js";
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

const transportGlobals = new WeakMap();
function mockTransport(t, { ready, open, write, close, modernDatagrams = false } = {}) {
  const transports = [];
  class Transport {
    constructor() {
      transports.push(this);
      this.ready = ready ? ready(this) : Promise.resolve();
      this.closed = new Promise((resolve) => { this.finish = resolve; });
      this.stream = {
        readable: new ReadableStream({ start: (c) => { this.events = c; } }),
        writable: new WritableStream({
          write: (bytes) => {
            const event = JSON.parse(new TextDecoder().decode(bytes));
            if (write) return write(this, event);
            if (event.type === "hello") this.event({ type: "welcome", id: 7, room: "ABCD1234", resume: "test-session" });
            if (event.type === "leave") this.event({ type: "left" });
          },
        }),
      };
      this.datagrams = {
        readable: new ReadableStream({ start: (c) => { this.snapshots = c; } }),
      };
      const outgoing = new WritableStream({ write: (bytes) => { this.inputPacket = Uint8Array.from(bytes); } });
      if (modernDatagrams) this.datagrams.createWritable = () => outgoing;
      else this.datagrams.writable = outgoing;
    }
    event(value) { this.events.enqueue(new TextEncoder().encode(JSON.stringify(value) + "\n")); }
    createBidirectionalStream() { return open ? open(this) : Promise.resolve(this.stream); }
    close() {
      this.didClose = true;
      this.events.close();
      this.snapshots.close();
      this.finish();
      close?.();
    }
  }
  if (!transportGlobals.has(t)) {
    const original = new Map(["WebTransport", "isSecureContext"].map(key => [key, Object.getOwnPropertyDescriptor(globalThis, key)]));
    transportGlobals.set(t, original);
    t.after(() => {
      for (const [key, descriptor] of original)
        if (descriptor) Object.defineProperty(globalThis, key, descriptor);
        else delete globalThis[key];
    });
  }
  for (const [key, value] of [["WebTransport", Transport], ["isSecureContext", true]]) {
    Object.defineProperty(globalThis, key, { configurable: true, value });
  }
  return transports;
}
const hello = { name: "test", create: true };
const config = { url: "https://localhost:8443/connect" };

for (const stage of ["TLS", "stream creation", "hello write", "welcome"]) {
  test(`connection deadline also bounds ${stage} and permits retry`, { timeout: 2000 }, async (t) => {
    const pending = () => new Promise(() => {});
    const transports = mockTransport(t, {
      ready: stage === "TLS" ? pending : undefined,
      open: stage === "stream creation" ? pending : undefined,
      write: ["hello write", "welcome"].includes(stage) ? () => stage === "hello write" ? pending() : undefined : undefined,
    });
    const connection = new Connection({ connectTimeoutMs: 20, leaveTimeoutMs: 20 });
    await assert.rejects(connection.connect(hello, config), /连接超时/);
    assert.ok(transports[0].didClose);
    assert.equal(connection.writer, null);
    assert.equal(connection.handshake, null);
    await connection.leave();
    mockTransport(t);
    await connection.connect(hello, config);
    assert.equal(connection.id, 7);
    await connection.leave();
    assert.equal(connection.writer, null);
  });
}

test("leaving cannot hang behind a stalled write or a throwing browser close", { timeout: 2000 }, async (t) => {
  const transports = mockTransport(t, {
    write: (transport, event) => {
      if (event.type === "hello") transport.event({ type: "welcome", id: 7 });
      else return new Promise(() => {});
    },
    close: () => { throw Error("already closed"); },
  });
  const connection = new Connection({ connectTimeoutMs: 100, leaveTimeoutMs: 20 });
  await connection.connect(hello, config);
  connection.send({ type: "equip" });
  await connection.leave();
  assert.ok(transports[0].didClose);
  assert.equal(connection.writer, null);
  assert.equal(connection.datagrams, null);
  assert.equal(connection.hello, null);
});

test("a stream resolving after timeout cannot restore a failed session", { timeout: 2000 }, async (t) => {
  let finish;
  const transports = mockTransport(t, { open: () => new Promise((resolve) => { finish = resolve; }) });
  const connection = new Connection({ connectTimeoutMs: 20 });
  await assert.rejects(connection.connect(hello, config), /连接超时/);
  finish(transports[0].stream);
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(connection.writer, null);
  assert.equal(connection.transport, null);
});

test("unsupported browsers fail before starting a transport", async (t) => {
  const transports = mockTransport(t);
  Object.defineProperty(globalThis, "WebTransport", { configurable: true, value: undefined });
  const connection = new Connection();
  await assert.rejects(connection.connect(hello, config), /不支持 WebTransport/);
  assert.equal(transports.length, 0);
});

test("Safari datagram streams send inputs and decode ArrayBuffer snapshots", async (t) => {
  const transports = mockTransport(t, { modernDatagrams: true });
  const connection = new Connection();
  await connection.connect(hello, config);
  await connection.input({ seq: 1, forward: 1 });
  assert.equal(transports[0].inputPacket.byteLength, 25);
  const packet = new Uint8Array(88), view = new DataView(packet.buffer);
  view.setUint16(0, 0x4748, true);
  view.setUint8(2, 1);
  view.setUint8(3, 2);
  view.setUint32(4, 1, true);
  view.setUint8(13, 1);
  view.setUint32(24, 7, true);
  view.setUint32(28, 1, true);
  transports[0].snapshots.enqueue(packet.buffer);
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(connection.metrics.snapshots, 1);
  assert.equal(connection.metrics.snapshotBytes, 88);
  assert.equal(connection.metrics.ack, 1);
  await connection.leave();
});
