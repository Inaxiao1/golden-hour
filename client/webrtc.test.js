import { test } from "node:test";
import assert from "node:assert/strict";
import { Connection } from "./net.js";
import { RTCTransport } from "./webrtc.js";

function fixture(t, { gather = true, open = true, offerStatus = 200, pendingFetch = false } = {}) {
  const peers = [], offers = [], transports = [];
  class Channel extends EventTarget {
    constructor(label, options) { super(); Object.assign(this, { label, options, readyState: "connecting", bufferedAmount: 0, sent: [] }); }
    send(data) {
      this.sent.push(typeof data === "string" ? data : Uint8Array.from(data));
      if (this.label !== "reliable") return;
      const command = JSON.parse(data);
      if (command.type === "hello") this.message(JSON.stringify({ type: "welcome", id: 17, room: "RTC12345", resume: "rtc-resume" }) + "\n");
      if (command.type === "leave") this.message('{"type":"left"}\n');
    }
    message(data) { this.dispatchEvent(new MessageEvent("message", { data })); }
  }
  class PC extends EventTarget {
    constructor(config) { super(); peers.push(this); Object.assign(this, { config, channels: [], iceGatheringState: gather ? "complete" : "gathering" }); }
    createDataChannel(label, options) { const dc = new Channel(label, options); this.channels.push(dc); return dc; }
    async createOffer() { return { type: "offer", sdp: "test" }; }
    async setLocalDescription(sdp) { this.localDescription = sdp; }
    async setRemoteDescription(sdp) {
      this.remoteDescription = sdp;
      if (open) for (const dc of this.channels) { dc.readyState = "open"; dc.dispatchEvent(new Event("open")); }
    }
    close() { if (this.didClose) return; this.didClose = true; this.connectionState = "closed"; this.dispatchEvent(new Event("connectionstatechange")); }
  }
  class WT {
    constructor() {
      transports.push(this);
      this.closed = new Promise(resolve => { this.finish = resolve; });
      this.ready = Promise.reject(Error("QUIC blocked"));
    }
    close() { this.didClose = true; this.finish(); }
  }
  const globals = {
    RTCPeerConnection: PC, WebTransport: WT, isSecureContext: true,
    location: { href: "http://localhost:8080/", origin: "http://localhost:8080" },
    fetch: async (url, init) => {
      offers.push({ url, init });
      if (pendingFetch) return new Promise((resolve, reject) => init.signal.addEventListener("abort", () => reject(Error("aborted")), { once: true }));
      return { ok: offerStatus === 200, json: async () => ({ type: "answer", sdp: "test answer" }) };
    },
  };
  for (const [key, value] of Object.entries(globals)) {
    const descriptor = Object.getOwnPropertyDescriptor(globalThis, key);
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
    t.after(() => descriptor ? Object.defineProperty(globalThis, key, descriptor) : delete globalThis[key]);
  }
  return { peers, offers, transports };
}
const config = { url: "https://localhost:8443/connect", rtc: { url: "/api/rtc/offer", iceServers: [{ urls: "stun:example.test" }] } };
const hello = { create: true, name: "test" };

for (const kind of ["unsupported", "failed", "forced"]) {
  test(`WebRTC fallback when WebTransport is ${kind}`, async t => {
    const { peers, offers, transports } = fixture(t);
    if (kind === "unsupported") globalThis.WebTransport = undefined;
    const connection = new Connection();
    await connection.connect(hello, { ...config, ...(kind === "forced" ? { preferredTransport: "webrtc" } : {}) });
    assert.equal(connection.metrics.transport, "webrtc");
    assert.equal(connection.id, 17);
    assert.equal(peers.length, 1);
    assert.equal(offers.length, 1);
    assert.deepEqual(peers[0].config.iceServers, config.rtc.iceServers);
    assert.equal(offers[0].url.href, "http://localhost:8080/api/rtc/offer");
    assert.equal(offers[0].init.cache, "no-store");
    assert.deepEqual(peers[0].channels.map(c => c.options), [
      { ordered: true, protocol: "gh-pvp-v1" },
      { ordered: false, maxRetransmits: 0, protocol: "gh-pvp-v1" },
    ]);
    assert.equal(transports.length, kind === "failed" ? 1 : 0);
    if (kind === "failed") assert.ok(transports[0].didClose);
    await connection.input({ seq: 3, forward: 1 });
    assert.equal(peers[0].channels[1].sent[0].byteLength, 25);
    const packet = new Uint8Array(88), v = new DataView(packet.buffer);
    v.setUint16(0, 0x4748, true); v.setUint8(2, 1); v.setUint8(3, 2);
    v.setUint32(4, 1, true); v.setUint8(13, 1); v.setUint32(24, 17, true); v.setUint32(28, 3, true);
    peers[0].channels[1].message(packet.buffer);
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(connection.metrics.ack, 3);
    await connection.leave();
    assert.ok(peers[0].didClose);
    assert.equal(connection.metrics.transport, null);
    assert.equal(connection.writer, null);
  });
}

for (const stage of ["ICE gathering", "signaling", "channel open"]) {
  test(`WebRTC deadline cancels ${stage} and permits retry`, async t => {
    const { peers } = fixture(t, { gather: stage !== "ICE gathering", pendingFetch: stage === "signaling", open: stage !== "channel open" });
    const connection = new Connection({ connectTimeoutMs: 20 });
    await assert.rejects(connection.connect(hello, { ...config, preferredTransport: "webrtc" }), /连接超时/);
    assert.ok(peers[0].didClose);
    assert.equal(connection.transport, null);
    fixture(t);
    await connection.connect(hello, { ...config, preferredTransport: "webrtc" });
    await connection.leave();
  });
}

test("WebRTC limits buffering and releases failed negotiations", async t => {
  const { peers } = fixture(t);
  const rtc = new RTCTransport(config.rtc);
  await rtc.ready;
  const [reliable, state] = peers[0].channels;
  state.bufferedAmount = 8193;
  await rtc.datagrams.writable.getWriter().write(new Uint8Array(25));
  assert.equal(state.sent.length, 0);
  reliable.bufferedAmount = 65537;
  await assert.rejects(rtc.stream.writable.getWriter().write(new Uint8Array(1)), /队列已满/);
  rtc.close();
  await rtc.closed;
  fixture(t, { offerStatus: 503 });
  const failed = new RTCTransport(config.rtc);
  await assert.rejects(failed.ready, /服务连接失败/);
  assert.ok(failed.pc.didClose);
});

test("application rejection on WebTransport does not retry on WebRTC", async t => {
  const { peers } = fixture(t);
  class RejectingWT {
    constructor() {
      this.ready = Promise.resolve(); this.closed = new Promise(() => {});
      this.stream = {
        readable: new ReadableStream({ start: c => { this.events = c; } }),
        writable: new WritableStream({ write: () => this.events.enqueue(new TextEncoder().encode('{"type":"error","message":"房间不存在"}\n')) }),
      };
      this.datagrams = { writable: new WritableStream(), readable: new ReadableStream({ start: c => { this.snapshots = c; } }) };
    }
    createBidirectionalStream() { return Promise.resolve(this.stream); }
    close() { this.events.close(); this.snapshots.close(); }
  }
  globalThis.WebTransport = RejectingWT;
  await assert.rejects(new Connection().connect(hello, config), /房间不存在/);
  assert.equal(peers.length, 0);
});

test("WebTransport remains preferred when healthy", async t => {
  const { peers } = fixture(t);
  const connection = new Connection();
  const attempts = [];
  connection.connectAttempt = async (_hello, _config, kind) => { attempts.push(kind); };
  await connection.connect(hello, config);
  assert.deepEqual(attempts, ["webtransport"]);
  assert.equal(peers.length, 0);
});

test("a canceled WT attempt cannot replace a newer RTC connection", async t => {
  const { peers } = fixture(t);
  let fail;
  globalThis.WebTransport = class {
    constructor() { this.ready = new Promise((_resolve, reject) => { fail = reject; }); this.closed = new Promise(() => {}); }
    close() {}
  };
  const connection = new Connection();
  const old = assert.rejects(connection.connect(hello, config));
  await connection.leave();
  await connection.connect(hello, { ...config, preferredTransport: "webrtc" });
  fail(Error("old attempt failed late"));
  await old;
  assert.equal(peers.length, 1);
  assert.equal(connection.metrics.transport, "webrtc");
  assert.equal(connection.id, 17);
  await connection.leave();
});
