import {
  World,
  motion,
  STEP,
  clamp,
  JUMP,
  SPRINT,
  CROUCH,
  ADS,
} from "./simulation.js";
import { WEAPONS } from "./net.js";

// The bundle is used only for rendering, models, audio and weapon animation.
// PvP simulation and authority live in the new client modules and Go server.
export class Engine extends EventTarget {
  constructor(connection) {
    super();
    this.net = connection;
    this.remotes = new Map();
    this.pending = [];
    this.seq = 0;
    this.accumulator = 0;
    this.snapshots = [];
    this.active = false;
    this.error = { x: 0, y: 0, z: 0 };
  }
  async ready() {
    if (this.gh) return;
    const start = performance.now();
    while (!window.GH?.ready) {
      if (document.querySelector("#err") || performance.now() - start > 60000)
        throw Error("3D 场景未能启动，请刷新页面并检查浏览器的 WebGL 支持。");
      await new Promise((r) => setTimeout(r, 50));
    }
    this.gh = window.GH;
    this.debug = this.gh.debug;
    this.ctx = this.debug.ctx;
    this.player = this.debug.player;
    this.input = this.debug.input;
    if (!this.ctx?.botCtx?.createCharacter || !this.player?._camera)
      throw Error("当前游戏构建与联机适配器不兼容。");
    this.world = new World(
      this.debug.world.cols
        .filter((c) => !c.off && !c.pane)
        .map((c) => ({
          min: [c.x0, c.y0, c.z0],
          max: [c.x1, c.y1, c.z1],
          noMove: !!c.noMove,
          noRay: !!c.noRay,
        })),
    );
  }
  async enter(primary = "ar") {
    await this.ready();
    if (this.active) return;
    this.active = true;
    document.body.classList.add("online-session");
    this.primary = primary;
    this.phase = "lobby";
    this.pending = [];
    this.seq = 0;
    this.snapshots = [];
    this.lastSpawn = null;
    this.roundStarted = false;
    this.lastSnapshot = null;
    const { ctx, debug: d, player: p, input } = this;
    this.original = {
      playerUpdate: p.update,
      damage: p.damage,
      matchUpdate: ctx.match.update,
      matchDamage: ctx.match.damage,
      actors: ctx.match.actors,
      bots: ctx.match.bots,
      hudUpdate: ctx.hud.update,
      fire: ctx.combat.fire,
      melee: ctx.combat.melee,
      reload: ctx.arsenal.startReload,
      swap: ctx.arsenal.swap,
      lockChange: input.onLockChange,
      lock: input.lock,
      primary: ctx.settings.primary,
      scores: ctx.match.scores,
      time: ctx.match.time,
    };
    input.unlock();
    ctx.match.k9.clear();
    for (const bot of ctx.match.bots)
      for (const rig of bot.rigs) rig.root.visible = false;
    ctx.match.bots = [];
    ctx.match.actors = [p];
    ctx.match.update = () => {};
    ctx.match.damage = () => {};
    ctx.hud.update = (dt, state) => {
      if (!this.lastSnapshot || !this.roundStarted) return;
      if (this.gh.G.state !== "playing") this.renderRemotes(dt);
      this.original.hudUpdate.call(ctx.hud, dt, state);
    };
    p.damage = () => {};
    ctx.settings.primary = primary;
    ctx.arsenal.reset(primary);
    ctx.arsenal.grenades = 0;
    p.update = (dt, ars) => this.update(dt, ars);
    ctx.combat.fire = (actor, eye, dir) => {
      if (this.phase === "live" && p.alive)
        this.net
          .send({
            type: "shot",
            tick: this.net.latestTick,
            yaw: Math.atan2(-dir.x, -dir.z),
            pitch: Math.asin(clamp(dir.y, -1, 1)),
          })
          .catch(() => {});
      return {
        point: eye.clone().addScaledVector(dir, 140),
        actor: null,
        head: false,
        any: false,
        dist: 140,
      };
    };
    ctx.combat.melee = () => null;
    ctx.arsenal.startReload = () => {
      const before = ctx.arsenal.state;
      this.original.reload.call(ctx.arsenal);
      if (before !== "reload" && ctx.arsenal.state === "reload")
        this.net.send({ type: "reload" }).catch(() => {});
    };
    ctx.arsenal.swap = (slot) => {
      const w = ctx.arsenal.slots[slot];
      if (w && slot !== ctx.arsenal.cur)
        this.net.send({ type: "equip", weapon: w.def.id }).catch(() => {});
      this.original.swap.call(ctx.arsenal, slot);
    };
    input.onLockChange = (locked) => {
      if (!locked) {
        input.keys.clear();
        input.down.clear();
      }
      this.dispatchEvent(new CustomEvent("lock", { detail: locked }));
    };
    input.lock = () => this.lock();
    this.dragMode = false;
    this.dragActive = false;
    this.controls = new AbortController();
    const opts = { signal: this.controls.signal };
    const canvas = document.querySelector("#c");
    canvas.addEventListener(
      "pointerdown",
      (e) => {
        if (!this.dragActive) return;
        this.dragging = true;
        this.lastPointer = { x: e.clientX, y: e.clientY };
        canvas.setPointerCapture(e.pointerId);
        input.keys.add("Mouse" + e.button);
        input.down.add("Mouse" + e.button);
      },
      opts,
    );
    canvas.addEventListener(
      "pointermove",
      (e) => {
        if (this.dragActive && this.dragging) {
          input.mx += e.clientX - this.lastPointer.x;
          input.my += e.clientY - this.lastPointer.y;
          this.lastPointer = { x: e.clientX, y: e.clientY };
        }
      },
      opts,
    );
    const release = () => {
      this.dragging = false;
      for (const key of ["Mouse0", "Mouse1", "Mouse2"]) {
        input.keys.delete(key);
        input.up.add(key);
      }
    };
    canvas.addEventListener("pointerup", release, opts);
    canvas.addEventListener("pointercancel", release, opts);
    addEventListener(
      "keydown",
      (e) => {
        if (
          this.dragActive &&
          [
            "Tab",
            "Space",
            "ArrowUp",
            "ArrowDown",
            "ArrowLeft",
            "ArrowRight",
          ].includes(e.code)
        )
          e.preventDefault();
        if (e.code === "Escape" && this.dragActive) {
          this.dragActive = false;
          release();
          this.dispatchEvent(new CustomEvent("lock", { detail: false }));
        }
      },
      opts,
    );
    addEventListener(
      "blur",
      () => {
        if (this.dragActive) {
          this.dragActive = false;
          release();
          this.dispatchEvent(new CustomEvent("lock", { detail: false }));
        }
      },
      opts,
    );
    ctx.hud.show("menu");
    ctx.hud.menus.go("online");
    this.gh.G.state = "menu";
    this.gh.G.timeScale = 1;
    ctx.match.state = "idle";
    ctx.match.live = false;
  }
  setRoom(room) {
    this.room = room;
    this.phase = room.phase;
    if (room.phase === "lobby") this.roundStarted = false;
    if (!this.active) return;
    for (const [id, r] of this.remotes) {
      const member = room.players.find((p) => p.id === id);
      if (!member || member.team !== r.team) {
        r.rig.root.removeFromParent();
        this.remotes.delete(id);
      }
    }
  }
  deploy() {
    if (!this.active) return;
    this.ctx.audio.init();
    this.ctx.audio.music.menu(false);
    this.ctx.hud.show(null);
    this.gh.G.state = "playing";
    this.ctx.match.state = "live";
    this.ctx.match.live = false;
    this.input.lock();
  }
  async lock() {
    if (!this.active) return;
    this.ctx.audio.init();
    if (this.input.locked) return;
    if (this.dragMode) {
      this.enableDrag();
      return;
    }
    try {
      await this.input.el.requestPointerLock();
    } catch {
      this.dispatchEvent(new CustomEvent("lockerror"));
    }
  }
  enableDrag() {
    this.dragMode = true;
    this.dragActive = true;
    this.input.el.tabIndex = 0;
    this.input.el.focus({ preventScroll: true });
    this.dispatchEvent(new CustomEvent("lock", { detail: true }));
  }
  applySpecs() {
    for (const slot of this.ctx.arsenal.slots) {
      const spec = this.specs?.find((w) => w.id === slot.def.id);
      if (spec)
        slot.def = {
          ...slot.def,
          rpm: 60 / spec.interval,
          mag: spec.mag,
          reserve: spec.reserve,
          pellets: spec.pellets,
        };
    }
  }
  readInput() {
    const k = this.input.keys,
      has = (...codes) => codes.some((c) => k.has(c));
    const enabled =
      !!this.net.writer &&
      (this.input.locked || this.dragActive) &&
      this.phase === "live" &&
      this.player.alive;
    return {
      seq: ++this.seq,
      forward: enabled
        ? Number(has("KeyW", "ArrowUp")) - Number(has("KeyS", "ArrowDown"))
        : 0,
      right: enabled
        ? Number(has("KeyD", "ArrowRight")) - Number(has("KeyA", "ArrowLeft"))
        : 0,
      yaw: this.player.yaw,
      pitch: this.player.pitch,
      buttons: enabled
        ? Number(has("Space")) * JUMP +
          Number(has("ShiftLeft", "ShiftRight")) * SPRINT +
          Number(has("KeyC", "ControlLeft")) * CROUCH +
          Number(has("Mouse1", "Mouse2")) * ADS
        : 0,
    };
  }
  update(dt, ars) {
    const p = this.player;
    if (!this.predicted) return;
    if (this.input.locked || this.dragActive) {
      const sens =
        0.0022 *
        this.ctx.settings.sens *
        (1 + (this.ctx.settings.adsSens - 1) * p.adsK);
      p.yaw -= this.input.mx * sens;
      p.pitch = clamp(
        p.pitch - this.input.my * sens * (this.ctx.settings.invertY ? -1 : 1),
        -1.5,
        1.5,
      );
    }
    this.accumulator = Math.min(this.accumulator + dt, 0.1);
    while (this.accumulator >= STEP) {
      this.accumulator -= STEP;
      const input = this.readInput();
      if (this.phase === "live" && p.alive)
        this.world.move(this.predicted, input);
      this.pending.push(input);
      if (this.pending.length > 120) this.pending.shift();
      if (input.seq % 2 === 0) this.net.input(input);
    }
    const m = this.predicted,
      decay = Math.exp(-15 * dt);
    for (const key of ["x", "y", "z"]) this.error[key] *= decay;
    p.e.x = m.x + this.error.x;
    p.e.y = m.y + this.error.y;
    p.e.z = m.z + this.error.z;
    p.e.h = m.crouch ? 1.2 : 1.8;
    p.e.onGround = m.ground;
    p.pos.set(p.e.x, p.e.y, p.e.z);
    p.vel.set(m.vx, m.vy, m.vz);
    p.stance = m.crouch ? "crouch" : "stand";
    p.mode = "normal";
    p.speed = Math.hypot(m.vx, m.vz);
    p.sprinting = this.input.keys.has("ShiftLeft") && p.speed > 5;
    p.stepPhase += dt * p.speed * 1.2;
    const sy = Math.sin(p.yaw),
      cy = Math.cos(p.yaw);
    p.moveX = (m.vx * cy - m.vz * sy) / (p.speed || 1);
    p.moveZ = (m.vx * sy + m.vz * cy) / (p.speed || 1);
    p._camera(dt, ars);
    this.input.enabled =
      !!this.net.writer &&
      this.phase === "live" &&
      p.alive &&
      (this.input.locked || this.dragActive);
    this.renderRemotes(dt);
  }
  snapshot(s) {
    if (!this.active) return;
    const me = s.players.find((p) => p.id === this.net.id);
    if (!me) return;
    this.phase = s.phase;
    this.lastSnapshot = s;
    this.receivedAt = performance.now();
    const match = this.ctx.match;
    match.state = s.phase === "over" ? "ending" : s.phase;
    match.scores = s.scores;
    match.time = s.remaining / 1000;
    match.scoreLimit = this.room?.scoreLimit || 40;
    if ((s.phase === "countdown" || s.phase === "live") && !this.roundStarted) {
      this.roundStarted = true;
      this.count = -1;
      this.ctx.hud.matchStart();
      // PvP only exposes actions implemented by the authoritative server.
      this.ctx.hud.hint(
        "<span><kbd>WASD</kbd> 移动</span><span><kbd>R</kbd> 换弹</span><span><kbd>TAB</kbd> 战绩</span>",
        6,
      );
      this.ctx.audio.music.menu(false);
      this.deploy();
    }
    const count = s.phase === "countdown" ? Math.ceil(s.remaining / 1000) : 0;
    if (this.roundStarted && count !== this.count && s.phase !== "over") {
      this.count = count;
      this.ctx.hud.count(count);
    }
    const wasAlive = this.player.alive,
      first = !this.predicted,
      respawn = me.alive && (!wasAlive || first);
    if (respawn) {
      this.primary = this.ctx.settings.primary;
      if (this.roundStarted) this.ctx.hud.respawned();
      this.player.reset();
      this.player.yaw = me.yaw;
      this.player.pitch = me.pitch;
      this.pending = [];
      this.error = { x: 0, y: 0, z: 0 };
      this.ctx.arsenal.reset(this.primary);
      this.applySpecs();
      this.ctx.arsenal.grenades = 0;
      this.net.send({ type: "equip", weapon: this.primary }).catch(() => {});
    }
    const before = this.predicted ? { ...this.predicted } : motion(me);
    this.pending = this.pending.filter((i) => i.seq > me.ack);
    this.predicted = motion(me);
    if (s.phase === "live" && me.alive)
      for (const input of this.pending) this.world.move(this.predicted, input);
    if (
      !first &&
      !respawn &&
      Math.hypot(
        before.x - this.predicted.x,
        before.y - this.predicted.y,
        before.z - this.predicted.z,
      ) < 2
    ) {
      this.error.x += before.x - this.predicted.x;
      this.error.y += before.y - this.predicted.y;
      this.error.z += before.z - this.predicted.z;
    }
    this.player.health = me.hp;
    this.player.alive = me.alive;
    this.player.team = me.team;
    this.ctx.hud.e.hpname.textContent = me.team ? "余烬" : "先锋";
    this.player.kills = me.kills;
    this.player.deaths = me.deaths;
    this.player.score = me.score;
    this.player.name =
      this.room?.players.find((p) => p.id === me.id)?.name || "干员";
    this.player.def = this.ctx.arsenal.slots.find(
      (w) => w.def.id === me.weapon,
    )?.def;
    if (!me.alive) this.ctx.hud.respawnIn(me.respawn / 1000);
    const slot = this.ctx.arsenal.slots.find((w) => w.def.id === me.weapon);
    if (slot) {
      slot.ammo = me.ammo;
      slot.reserve = me.reserve;
    }
    if (
      this.reloading &&
      me.reload === 0 &&
      this.ctx.arsenal.state === "reload"
    ) {
      this.ctx.arsenal.state = "ready";
      this.ctx.arsenal.clip = null;
      this.ctx.vm.release();
    }
    this.reloading = me.reload > 0;
    this.snapshots.push({ ...s, received: performance.now() });
    if (this.snapshots.length > 12) this.snapshots.shift();
    for (const other of s.players)
      if (other.id !== this.net.id && !this.remotes.has(other.id))
        this.createRemote(other);
    this.syncActors(s);
  }
  pause() {
    if (!this.active) return;
    this.dragActive = false;
    this.input.keys.clear();
    this.input.down.clear();
    this.gh.G.state = "paused";
    this.gh.G.timeScale = 1;
    this.ctx.hud.show("pause");
  }
  syncActors(s) {
    const actors = this.ctx.match.actors;
    actors.length = 1;
    actors[0] = this.player;
    for (const p of s.players) {
      if (p.id === this.net.id) continue;
      const r = this.remotes.get(p.id);
      if (!r) continue;
      const actor = r.actor;
      Object.assign(actor, {
        id: p.id,
        name: this.room?.players.find((q) => q.id === p.id)?.name || "干员",
        team: p.team,
        health: p.hp,
        alive: p.alive,
        kills: p.kills,
        deaths: p.deaths,
        score: p.score,
      });
      actor.def.id = p.weapon;
      actor.e.h = p.crouch ? 1.2 : 1.8;
      actor.pos.set(p.x, p.y, p.z);
      actors.push(actor);
    }
    this.ctx.match.actors = actors;
  }
  actor(id) {
    return id === this.net.id ? this.player : this.remotes.get(id)?.actor;
  }
  createRemote(p) {
    const ctx = this.ctx.botCtx,
      rig = ctx.createCharacter({
        team: p.team,
        gunFactory: (id) => ctx.worldGun(id),
        ground: (x, z, y) => ctx.world.groundAt(x, z, y),
      });
    rig.setWeapon(p.weapon);
    rig.root.visible = p.alive;
    ctx.scene.add(rig.root);
    rig.root.traverse((n) => {
      if (n.isMesh) {
        // Remote rigs are cosmetic. Let Three.js cull off-screen meshes and
        // keep them out of the expensive shadow-map pass on low-end GPUs.
        n.castShadow = false;
        n.receiveShadow = false;
        // Skinned bounds change with the pose; retain their native behavior.
        n.frustumCulled = !n.isSkinnedMesh;
      }
    });
    this.remotes.set(p.id, {
      rig,
      team: p.team,
      weapon: p.weapon,
      actor: {
        id: p.id,
        isPlayer: false,
        lastShot: -100,
        pos: this.player.pos.clone(),
        e: { h: 1.8 },
        def: { id: p.weapon },
      },
      pos: this.player.pos.clone(),
      state: {
        pos: this.player.pos.clone(),
        yaw: p.yaw,
        pitch: p.pitch,
        speed: 0,
        moveX: 0,
        moveZ: 0,
        stance: "stand",
        sprint: false,
        airborne: false,
        reloading: false,
        dead: false,
      },
    });
  }
  renderRemotes(dt) {
    if (!this.snapshots.length) return;
    const latest = this.snapshots.at(-1),
      target = latest.time + (performance.now() - latest.received) - 100;
    let a = this.snapshots[0],
      b = latest;
    for (let i = 1; i < this.snapshots.length; i++)
      if (this.snapshots[i].time >= target) {
        a = this.snapshots[i - 1];
        b = this.snapshots[i];
        break;
      }
    const t = clamp((target - a.time) / (b.time - a.time || 1), 0, 1);
    for (const p of b.players) {
      const r = this.remotes.get(p.id);
      if (!r) continue;
      const previous = a.players.find((q) => q.id === p.id) || p;
      const prev = previous.alive === p.alive ? previous : p;
      r.rig.root.visible = p.alive && p.connected;
      if (!r.rig.root.visible) continue;
      if (r.weapon !== p.weapon) {
        r.rig.setWeapon(p.weapon);
        r.weapon = p.weapon;
      }
      const state = r.state;
      state.pos.set(
        prev.x + (p.x - prev.x) * t,
        prev.y + (p.y - prev.y) * t,
        prev.z + (p.z - prev.z) * t,
      );
      let delta = (p.yaw - prev.yaw) % (2 * Math.PI);
      if (delta > Math.PI) delta -= 2 * Math.PI;
      if (delta < -Math.PI) delta += 2 * Math.PI;
      state.yaw = prev.yaw + delta * t;
      state.pitch = prev.pitch + (p.pitch - prev.pitch) * t;
      state.speed = Math.hypot(p.vx, p.vz);
      state.moveX =
        (p.vx * Math.cos(p.yaw) - p.vz * Math.sin(p.yaw)) / (state.speed || 1);
      state.moveZ =
        (p.vx * Math.sin(p.yaw) + p.vz * Math.cos(p.yaw)) / (state.speed || 1);
      state.stance = p.crouch ? "crouch" : "stand";
      state.sprint = state.speed > 5;
      state.airborne = !p.ground;
      state.reloading = p.reload > 0;
      state.dead = false;
      r.actor.pos.copy(state.pos);
      r.rig.update(dt, state);
    }
  }
  event(e) {
    if (!this.active) return;
    const p = this.player;
    if (e.type === "shot" && e.id !== this.net.id) {
      const r = this.remotes.get(e.id);
      r?.rig.kick();
      if (r) r.actor.lastShot = this.ctx.time();
      const pos = p.pos.clone().set(e.x, e.y, e.z),
        dir = p.pos.clone().set(e.dx, e.dy, e.dz);
      this.ctx.audio.gunshot(e.weapon, pos);
      this.ctx.fx.muzzle?.(pos, dir, 1, true);
    }
    if (e.type === "damage" && e.id === this.net.id) {
      p.health = e.hp;
      p.addShake(0.25);
      this.debug.gfx.uniforms.uDamage.value = 0.6;
      this.ctx.audio.hurt(0.4);
    }
    if (e.type === "damage" && e.attacker === this.net.id)
      this.ctx.hud.hit(e.head, false, false);
    if (e.type === "kill") {
      const killer = this.actor(e.killer),
        victim = this.actor(e.victim);
      if (victim) {
        victim.alive = false;
        this.ctx.hud.killfeed(killer, victim, e.weapon, e.head);
        if (e.killer === this.net.id) {
          this.ctx.hud.hit(e.head, true, false);
          this.ctx.hud.kill(victim, []);
        }
        if (e.victim === this.net.id) this.ctx.hud.died(killer, e.weapon);
      }
    }
    if (e.type === "welcome") {
      this.seq = Math.max(this.seq, e.inputSeq || 0);
      this.specs = e.weapons;
      this.applySpecs();
    }
    if (e.type === "round-start") {
      this.roundStarted = false;
      this.predicted = null;
    }
    if (e.type === "round-end") {
      this.dragActive = false;
      this.input.unlock();
      this.input.keys.clear();
    }
    if (e.type === "spawn" && e.id === this.net.id) {
      this.player.alive = false;
      this.pending = [];
    }
  }
  exit() {
    if (!this.active) return;
    this.active = false;
    document.body.classList.remove("online-session");
    this.dragActive = false;
    this.controls.abort();
    this.input.unlock();
    for (const r of this.remotes.values()) r.rig.root.removeFromParent();
    this.remotes.clear();
    const o = this.original,
      c = this.ctx,
      p = this.player;
    p.update = o.playerUpdate;
    p.damage = o.damage;
    c.match.update = o.matchUpdate;
    c.match.damage = o.matchDamage;
    c.match.actors = o.actors;
    c.match.bots = o.bots;
    c.match.scores = o.scores;
    c.match.time = o.time;
    c.hud.update = o.hudUpdate;
    c.combat.fire = o.fire;
    c.combat.melee = o.melee;
    c.arsenal.startReload = o.reload;
    c.arsenal.swap = o.swap;
    this.input.onLockChange = o.lockChange;
    this.input.lock = o.lock;

    this.predicted = null;
    this.gh.G.state = "menu";
    c.match.state = "idle";
    document.querySelector("#hud").style.display = "";
    c.hud.show("menu");
    c.audio.music.menu(true);
    p.reset();
    c.arsenal.reset(c.settings.primary);
  }
}
