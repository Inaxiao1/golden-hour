# Performance measurements

Measured locally on Apple M6 (darwin/arm64), Go 1.27.4. These numbers measure CPU code on this machine; they are not a low-end device FPS guarantee. Both runs use the same hotel map and benchmark code in `internal/game/performance_test.go`. Background load and compiler warmup affect timing.

| Benchmark | Before | After | Allocations before → after |
|---|---:|---:|---:|
| Hotel ray query | 6026 ns | 669 ns | 0 → 0 |
| Hotel movement step | 2055 ns | 367 ns | 17 → 0 |
| Room tick, 1 human + 9 bots | 112318 ns | 32919 ns | 89 → 3 |
| Hotel route | 532483 ns | 213698 ns | 6 → 6 |

Movement now allocates zero bytes per step (previously 2230 B); room ticks allocate about 1619 B instead of 15620 B. Route improvements come from faster collision/visibility checks; the route algorithm is unchanged. Room measurements average a long running simulation, so combat/navigation workload varies over time.

The former input path serialized JSON 30 times/second. The new path writes 25-byte binary inputs from one reusable buffer and validates finite floats before applying them. Snapshot payload stays 664 bytes for ten players at 30 Hz, about 19.9 KB/s per client before transport overhead. Reliable shots, room messages and retransmissions add traffic; these are payload budgets, not a measured WAN bandwidth or latency result.

Browser checks use the app's bounded local diagnostic report (`#gh-performance`). It samples actual render intervals instead of monitor RAF frequency. `renderSubmitMs` is CPU time submitting the renderer, not GPU timer-query time. Menu and pause FPS are deliberately capped and must not be treated as gameplay speed. Earlier measurements from different camera views/round phases are not comparable FPS benchmarks.

Correctness checks compare 3000 random hotel rays and 3000 collision positions against a full collider scan, test stamp rollover/deduplication, and compare 1800 JS prediction steps against the full-map candidate path. Binary fixtures are shared between Go and JS tests; the real two-client HTTP/3 integration test sends binary movement and waits for an acknowledged movement snapshot.

Browser verification on 2026-11-13: original native menu/settings, 1 human + 9 server bots, live medium quality at 1088 × 612. A foreground gameplay sample measured 60 FPS, mean 16.66 ms, p95 19.8 ms; the renderer reported 357 calls / 2.04 million triangles for that frame. Actual device: `ANGLE Metal Renderer: Apple M6`, WebGL 2, software renderer false. Binary inputs had 370 packets / 9250 bytes, acknowledged sequence 740; 780 snapshots arrived. This is a post-change sample of one camera view, not an FPS uplift ratio against a matched pre-change run. Low quality was also verified at 896 × 503 with no rendering errors; its pause view is capped at 20 FPS.

Validation passed: Go tests, Go race tests, Go vet, nine Node tests, binary HTTP/3 integration, and an exact replay/idempotence check of the guarded renderer patches. Test rooms were removed after verification and the rebuilt local service was left running.

## Startup cache and pre-baked assets

Additional measurements on the same Apple M6 browser at 1280 × 720, with the same saved SMG loadout and automatic/medium quality:

| Startup stage | Before | New build, no local material cache | Repeat load, material cache hit |
|---|---:|---:|---:|
| Navigation → ready | 6782 ms | 4460 ms | 3692 ms |
| Material preparation | 537 ms | 563 ms | 42 ms |
| Armoury preparation | 2253 ms | 96 ms | 86 ms |
| Live weapon icon baking | 1366 ms | 0 ms | 0 ms |

These are individual local samples, including the existing loading UI and at most 100 ms of diagnostic polling delay. They are not a WAN or low-end device guarantee. Repeat loading was about 46% faster in this run. Its resource timing report showed 0 transferred resource bytes (navigation HTML excluded), while the material cache read took 77 ms. The first new build transferred about 1.62 MB of resources; the gzip game bundle is about 1.00 MB instead of 2.8 MB uncompressed.

The shipped pack contains the native weapon silhouettes/cards and ten camo previews, bound to the bundle SHA-256. GPU image baking no longer repeats at startup. About 154 MiB of derived RGBA material pixels is stored in IndexedDB during idle time, bounded to 160 MiB and replaced after bundle changes. Texture classes, colour space, UV transforms, wrapping/filtering and anisotropy limits are restored; WebGL objects and driver binaries are recreated per context. Unsupported storage, invalid cache data, expired versions and network pack failures fall back to the original generators.

Additional checks cover compressed/conditional HTTP responses, immutable version URLs, source/private-file exclusion, obsolete asset versions, binary/canvas texture round trips, shared texture references, corrupt data, and pre-baked asset hashes. All 13 Node tests and Go tests/race/vet passed. Native gunsmith/camo previews and the ten-actor WebTransport match were verified after a material-cache hit without console errors. Renderer/startup patches reproduce exactly from the original bundle and can be rerun safely.
