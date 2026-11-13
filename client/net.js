import { RTCTransport } from "./webrtc.js";

const PHASES = ["lobby", "countdown", "live", "over"];
export const WEAPONS = ["ar", "smg", "shotgun", "pistol"];
const INPUT_BYTES = 25;
const INPUT_MAGIC = 0x4748;
const MOTION_FIELDS = ["x", "y", "z", "vx", "vy", "vz", "yaw", "pitch"];
const COUNTER_FIELDS = [
  "hp",
  "ammo",
  "reserve",
  "kills",
  "deaths",
  "score",
  "respawn",
  "reload",
];

async function deadline(operation, timeout, message) {
  let timer;
  try {
    return await Promise.race([
      operation(),
      new Promise((_, reject) => {
        timer = setTimeout(() => reject(Error(message)), timeout);
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}

// Movement datagrams are fixed-size binary packets. This removes JSON parsing
// and UTF-8 allocation from the 30 Hz input path while the server still accepts
// JSON for older clients during rolling upgrades.
export function encodeInput(input, bytes = new Uint8Array(INPUT_BYTES)) {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  view.setUint16(0, INPUT_MAGIC, true);
  view.setUint8(2, 1);
  view.setUint8(3, 1);
  view.setUint32(4, input.seq >>> 0, true);
  view.setFloat32(8, input.forward ?? 0, true);
  view.setFloat32(12, input.right ?? 0, true);
  view.setFloat32(16, input.yaw ?? 0, true);
  view.setFloat32(20, input.pitch ?? 0, true);
  view.setUint8(24, input.buttons || 0);
  return bytes;
}
export function decodeSnapshot(bytes) {
  if (bytes instanceof ArrayBuffer) bytes = new Uint8Array(bytes);
  if (bytes.byteLength < 24) return null;
  const v = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  if (
    v.getUint16(0, true) !== 0x4748 ||
    v.getUint8(2) !== 1 ||
    v.getUint8(3) !== 2
  )
    return null;
  const count = v.getUint8(13);
  if (count > 10 || bytes.byteLength !== 24 + 64 * count || v.getUint8(12) > 3)
    return null;
  const out = {
    tick: v.getUint32(4, true),
    time: v.getUint32(8, true),
    phase: PHASES[v.getUint8(12)],
    scores: [v.getUint16(14, true), v.getUint16(16, true)],
    remaining: v.getUint32(18, true),
    winner: v.getInt8(22),
    players: [],
  };
  for (let i = 0; i < count; i++) {
    const o = 24 + i * 64,
      flags = v.getUint8(o + 57);
    const p = { id: v.getUint32(o, true), ack: v.getUint32(o + 4, true) };
    for (let j = 0; j < MOTION_FIELDS.length; j++) {
      const key = MOTION_FIELDS[j];
      p[key] = v.getFloat32(o + 8 + j * 4, true);
      if (!Number.isFinite(p[key])) return null;
    }
    for (let j = 0; j < COUNTER_FIELDS.length; j++)
      p[COUNTER_FIELDS[j]] = v.getUint16(o + 40 + j * 2, true);
    p.team = v.getUint8(o + 56);
    p.alive = !!(flags & 1);
    p.crouch = !!(flags & 2);
    p.ground = !!(flags & 4);
    p.connected = !!(flags & 8);
    p.protected = !!(flags & 16);
    p.jumpHeld = !!(flags & 32);
    p.bot = !!(flags & 64);
    p.weapon = WEAPONS[v.getUint8(o + 58)];
    p.cooldown = v.getUint16(o + 60, true);
    if (p.team > 1 || !p.weapon) return null;
    out.players.push(p);
  }
  return out;
}

export class Connection extends EventTarget {
  constructor({ connectTimeoutMs = 12000, leaveTimeoutMs = 500 } = {}) {
    super();
    this.connectTimeoutMs = connectTimeoutMs;
    this.leaveTimeoutMs = leaveTimeoutMs;
    this.generation = 0;
    this.intentional = true;
    this.sendChain = Promise.resolve();
    this.retry = 0;
    this.latestTick = 0;
    this.inputBytes = new Uint8Array(INPUT_BYTES);
    this.metrics = {
      inputPackets: 0,
      inputBytes: 0,
      snapshots: 0,
      snapshotBytes: 0,
      ack: 0,
      rttMs: 0,
    };
  }
  emit(type, detail) {
    this.dispatchEvent(new CustomEvent(type, { detail }));
  }
  checkSupport() {
    if (typeof globalThis.WebTransport !== "function" && typeof globalThis.RTCPeerConnection !== "function")
      throw Error(
        "当前浏览器不支持 WebTransport 或 WebRTC，请更新浏览器后重试。",
      );
    if (!globalThis.isSecureContext)
      throw Error("联机需要 HTTPS。开发时请使用本机 localhost 地址。");
  }
  async connect(hello, config) {
    this.checkSupport();
    const rtc = config.rtc && typeof globalThis.RTCPeerConnection === "function";
    const kind = config.preferredTransport === "webrtc" || typeof globalThis.WebTransport !== "function" ? "webrtc" : "webtransport";
    if (kind === "webrtc" && !rtc) throw Error("服务器或浏览器未提供 WebRTC 连接支持。");
    const attempt = this.generation + 1;
    try {
      await this.connectAttempt(hello, config, kind);
    } catch (error) {
      if (this.generation !== attempt + 1 || kind !== "webtransport" || !rtc || error.application || this.intentional) throw error;
      this.emit("status", "正在切换备用连接…");
      // A late welcome may already have assigned a room. Resume it so a
      // transport change cannot create a second player or an abandoned room.
      await this.connectAttempt(this.hello?.resume ? this.hello : hello, config, "webrtc");
    }
  }
  async connectAttempt(hello, config, kind) {
    this.intentional = false;
    this.hello = { ...hello };
    this.config = config;
    const generation = ++this.generation;
    this.emit("status", "正在连接服务器…");
    const options = {};
    if (config.certificateHash) {
      options.serverCertificateHashes = [
        {
          algorithm: "sha-256",
          value: Uint8Array.from(atob(config.certificateHash), (c) =>
            c.charCodeAt(0),
          ),
        },
      ];
    }
    let wt;
    const current = () => {
      if (generation !== this.generation) throw Error("连接已取消。");
    };
    try {
      wt = kind === "webrtc" ? new RTCTransport(config.rtc) : new WebTransport(config.url, options);
      this.transport = wt;
      wt.closed.catch(() => {});
      // One deadline covers TLS, stream creation, the hello write and welcome.
      // Some browsers can leave stream creation or a write pending indefinitely.
      await deadline(async () => {
        await wt.ready;
        current();
        this.emit("status", "正在建立对战连接…");
        const stream = await wt.createBidirectionalStream();
        current();
        this.writer = stream.writable.getWriter();
        // Safari implements the newer createWritable() API; Chromium also
        // exposes the original writable stream. Both carry QUIC datagrams.
        const outgoing = wt.datagrams.writable ?? wt.datagrams.createWritable?.();
        if (!outgoing?.getWriter)
          throw Error("当前浏览器缺少 WebTransport 数据报支持，请更新浏览器后重试。");
        this.datagrams = outgoing.getWriter();
        this.sendChain = Promise.resolve();
        const accepted = new Promise((resolve, reject) => {
          this.handshake = { generation, resolve, reject };
        });
        accepted.catch(() => {});
        this.readEvents(stream.readable, generation);
        this.readSnapshots(wt.datagrams.readable, generation);
        this.emit("status", "正在加入房间…");
        await this.send({ ...hello, type: "hello" });
        await accepted;
        current();
      }, this.connectTimeoutMs, "连接超时：请重试，并检查服务器 UDP 端口与证书配置。");
      this.handshake = null;
      this.retry = 0;
      this.metrics.transport = kind;
      this.emit("status", "已连接");
      wt.closed.then(
        () => this.closed(generation),
        () => this.closed(generation),
      );
    } catch (error) {
      if (generation === this.generation) {
        this.generation++;
        this.handshake?.reject(error);
        this.handshake = null;
        this.writer = null;
        this.datagrams = null;
        this.transport = null;
        this.sendChain = Promise.resolve();
      }
      try { wt?.close(); } catch {}
      throw error;
    }
  }
  send(message) {
    if (!this.writer) return Promise.reject(Error("尚未连接服务器"));
    const writer = this.writer,
      data = new TextEncoder().encode(JSON.stringify(message) + "\n");
    const next = this.sendChain.then(() => writer.write(data));
    this.sendChain = next.catch(() => {});
    return next;
  }
  async input(input) {
    if (!this.datagrams || this.sending) return;
    this.sending = true;
    try {
      // One write in flight: reuse storage only after the stream consumes it.
      await this.datagrams.write(encodeInput(input, this.inputBytes));
      this.metrics.inputPackets++;
      this.metrics.inputBytes += INPUT_BYTES;
    } catch {
    } finally {
      this.sending = false;
    }
  }
  async readEvents(readable, generation) {
    const reader = readable.getReader(),
      decoder = new TextDecoder();
    let buffer = "";
    try {
      while (generation === this.generation) {
        const { value, done } = await reader.read();
        if (done || generation !== this.generation) break;
        buffer += decoder.decode(value, { stream: true });
        if (buffer.length > 65536) throw Error("可靠流消息过大");
        let n;
        while ((n = buffer.indexOf("\n")) >= 0) {
          const line = buffer.slice(0, n);
          buffer = buffer.slice(n + 1);
          if (!line) continue;
          const e = JSON.parse(line);
          if (e.type === "pong" && Number.isFinite(e.nonce))
            this.metrics.rttMs = Math.round(performance.now() - e.nonce);
          if (e.type === "welcome") {
            this.hello = {
              ...this.hello,
              create: false,
              room: e.room,
              resume: e.resume,
            };
            this.id = e.id;
            this.latestTick = 0;
            this.handshake?.resolve();
          }
          if (e.type === "error" && this.handshake?.generation === generation) {
            const error = Error(e.message);
            error.application = true;
            this.handshake.reject(error);
          }
          if (e.type === "left") this.leftResolve?.();
          this.emit("event", e);
        }
      }
    } catch (error) {
      if (generation === this.generation) {
        this.handshake?.reject(error);
        this.emit("error", error.message);
      }
    } finally {
      if (this.handshake?.generation === generation)
        this.handshake.reject(Error("服务器在加入房间前关闭了连接。"));
      reader.releaseLock();
    }
  }
  async readSnapshots(readable, generation) {
    const reader = readable.getReader();
    try {
      while (generation === this.generation) {
        const { value, done } = await reader.read();
        if (done || generation !== this.generation) break;
        const snapshot = decodeSnapshot(value);
        if (snapshot && snapshot.tick > this.latestTick) {
          this.metrics.snapshots++;
          this.metrics.snapshotBytes += value.byteLength;
          this.metrics.ack =
            snapshot.players.find((p) => p.id === this.id)?.ack || 0;
          this.latestTick = snapshot.tick;
          this.emit("snapshot", snapshot);
        }
      }
    } catch {
    } finally {
      reader.releaseLock();
    }
  }
  closed(generation) {
    if (generation !== this.generation || this.intentional) return;
    this.writer = null;
    this.datagrams = null;
    this.emit("status", "连接中断，正在恢复");
    if (!this.hello?.resume) {
      this.emit("error", "连接已关闭，请重新加入。");
      return;
    }
    this.reconnect(generation);
  }
  async reconnect(generation) {
    if (this.retry >= 4) {
      this.emit("error", "恢复连接失败，请重新加入房间。");
      return;
    }
    const delay = Math.min(4000, 500 * 2 ** this.retry++);
    await new Promise((r) => setTimeout(r, delay));
    if (this.intentional || generation !== this.generation) return;
    try {
      await this.connect(this.hello, this.config);
    } catch (error) {
      this.emit("status", error.message);
      this.reconnect(this.generation);
    }
  }
  async leave() {
    this.intentional = true;
    if (this.writer) {
      const accepted = new Promise((resolve) => {
        this.leftResolve = resolve;
      });
      try {
        await deadline(async () => {
          await this.send({ type: "leave" });
          await accepted;
        }, this.leaveTimeoutMs, "退出房间超时。");
      } catch {}
    }
    this.leftResolve = null;
    this.generation++;
    try { this.transport?.close(); } catch {}
    this.transport = null;
    this.writer = null;
    this.datagrams = null;
    this.id = null;
    this.hello = null;
    this.sendChain = Promise.resolve();
    this.metrics.transport = null;
  }
}
