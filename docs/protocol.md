# Golden Hour PvP protocol v1

Endpoint: `https://<host>/connect`. TLS 1.3 and HTTP/3 over UDP are mandatory. TCP HTTPS serves the page and config; localhost development additionally serves HTTP on 8080. No WebSocket or polling transport is used.

## Lifecycle and authority

1. Fetch `/api/config` for the endpoint and, in development only, its pinned SHA-256 certificate hash.
2. Open a WebTransport session and one client-created bidirectional stream. Immediately write `hello` so the stream is visible to the server.
3. Reliable frames are UTF-8 JSON lines, capped at 16 KiB. The initial hello is capped at 2 KiB and must arrive within 10 seconds.
4. `welcome` contains the server-assigned player ID, room code, unpredictable resume credential, and canonical weapon configuration. Never include the resume credential in an invitation URL.
5. Datagram input contains intent only. The 60 Hz server clock advances simulation; additional packets do not advance time. At 30 Hz a new snapshot replaces any older pending snapshot instead of blocking the tick.
6. Disconnects retain human seats for 20 seconds and AI controls them during that grace period. Resume increments the binding generation, so an older connection's disconnect cannot remove the restored player.

## Reliable messages

Client → server examples:

```json
{"type":"hello","name":"Alpha","create":true}
{"type":"hello","name":"Bravo","room":"A1B2C3D4"}
{"type":"hello","room":"A1B2C3D4","resume":"<private credential>"}
{"type":"ready","ready":true}
{"type":"team","team":1}
{"type":"start"}
{"type":"equip","weapon":"ar"}
{"type":"reload"}
{"type":"shot","tick":120,"yaw":0.5,"pitch":0.1}
{"type":"ping","nonce":1234.5}
{"type":"leave"}
```

`start` requires the host and all connected humans ready. Both teams always contain five actors: filler bots are ready automatically, so a solo host can start. Human joins replace filler bots on the assigned team; leave/expiry restores a filler. Bots cannot host. A room is removed when its last human leaves or expires, even if bots remain. Team changes are only allowed between rounds. Weapons: `ar`, `smg`, `shotgun`, `pistol`. A shot supplies aim and the last observed server tick, never a victim, origin, damage or hit conclusion. The server verifies phase, life, cooldown, reload, ammunition, solid cover and hitboxes. Target rewind is capped at 12 ticks / 200 ms; shooter origin always comes from the current server state.

Server event types: `welcome`, `room`, `round-start`, `live`, `spawn`, `shot`, `reload`, `equipped`, `damage`, `kill`, `round-end`, `pong`, `left`, `error`. Events are ordered. `room` is the full ten-seat membership, readiness, host and rules view. Each member includes `bot`; filler bots have `bot:true, connected:true`. A disconnected human retains `bot:false, connected:false` while AI temporarily controls their actor. `left` confirms removal before session closure. Slow readers are disconnected without blocking the simulation.

## Input datagrams

Current clients send exactly **25 bytes**, normally 30 packets/second (750 bytes/second of input payload, excluding QUIC/IP overhead). Little-endian wire format:

| Offset | Type | Meaning |
|---|---|---|
| 0 | u16 | Magic `0x4748` |
| 2 / 3 | u8 / u8 | Version `1` / input kind `1` |
| 4 | u32 | Input sequence |
| 8 / 12 | f32 / f32 | Forward / right axis |
| 16 / 20 | f32 / f32 | Yaw / pitch in radians |
| 24 | u8 | Button bits |

The server rejects nonfinite input. For rolling upgrades, the old JSON input format is also accepted, at most 1200 bytes:

```json
{"type":"input","seq":42,"forward":1,"right":0,"yaw":0,"pitch":0,"buttons":2}
```

Sequence numbers increase with 60 Hz prediction steps. Old or implausibly far-ahead sequences are rejected. Axes are clamped and diagonals normalized. Angles are radians. Button bits: jump `1`, sprint `2`, crouch `4`, aim `8`. After 250 ms without fresh intent, movement is released. Reliable actions are independent of movement packet loss.

## Snapshot datagrams

All integers and IEEE-754 float32 values are little-endian. Exact length: `24 + 64 × count`, at most 664 bytes for 10 players. Invalid magic, version, size, nonfinite values or obsolete ticks are discarded.

| Header offset | Type | Meaning |
|---|---|---|
| 0 | u16 | Magic `0x4748` |
| 2 / 3 | u8 / u8 | Version `1` / snapshot kind `2` |
| 4 | u32 | Server tick |
| 8 | u32 | Milliseconds since server start |
| 12 / 13 | u8 / u8 | Phase: lobby `0`, countdown `1`, live `2`, over `3`; player count |
| 14 / 16 | u16 / u16 | Vanguard / Ember score |
| 18 | u32 | Phase time remaining, milliseconds |
| 22 / 23 | i8 / u8 | Winner (`-1` draw) / reserved |

Each 64-byte player entry:

| Entry offset | Type | Meaning |
|---|---|---|
| 0 / 4 | u32 / u32 | Player ID / last accepted input sequence |
| 8–28 | 6 × f32 | x, y, z, vx, vy, vz |
| 32 / 36 | f32 / f32 | Yaw / pitch |
| 40 / 42 / 44 | u16 | Health / magazine / reserve |
| 46 / 48 / 50 | u16 | Kills / deaths / score |
| 52 / 54 | u16 | Respawn / reload remaining milliseconds |
| 56 | u8 | Team |
| 57 | u8 | Flags: alive `1`, crouch `2`, grounded `4`, active actor (human, bot, or grace-period AI) `8`, protected `16`, jump held `32`, filler bot `64` |
| 58 | u8 | Weapon index: ar `0`, smg `1`, shotgun `2`, pistol `3` |
| 59 | u8 | Reserved |
| 60 / 62 | u16 / u16 | Fire cooldown milliseconds / reserved |

Local prediction restores the server state and replays intents newer than the acknowledged sequence. Small corrections decay visually; respawns and large corrections snap. Remote players interpolate 100 ms behind estimated server time, including wrapped yaw, stance, weapon and reload state. Names and membership stay on the reliable stream.

## Bot authority

Bots run only on the Go server. The embedded hotel graph has 119 navigation nodes and links exported from the original renderer. Route planning, line-of-sight target selection, reaction delay, aim error and movement intents feed the same fixed-step movement and weapon rules as humans. Bots use ordinary magazines and reload duration; they can replenish reserve ammo when reloading an empty magazine. They do not bypass world cover, fire cooldown, damage or respawn checks. Reconnecting a human clears AI intent and returns control to that human.
