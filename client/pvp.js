import { Connection } from "./net.js";
import { Engine } from "./engine.js";

const net = new Connection();
const engine = new Engine(net);
const query = new URLSearchParams(location.search);
let lastRosterAt = 0;
let room = null,
  busy = false,
  returnPage = null;
await engine.ready();
const { ctx } = engine;
// Read-only diagnostics consumed by the local graphics settings report.
engine.debug.network = net.metrics;
const menus = ctx.hud.menus;
const $ = (id) => document.getElementById(id);
const visible = (id, value) => {
  $(id).hidden = !value;
};
const label = (id, value) => {
  $(id).textContent = value;
};
const inMatch = () => room && ["live", "countdown"].includes(room.phase);
const current = () => room?.players.find((p) => p.id === net.id);

// Register one more page with the game's own menu controller. All navigation,
// backgrounds, transitions, keyboard controls, gunsmith and HUD stay native.
const deploy = menus.pages.main.querySelector('[data-act="deploy"]');
deploy.dataset.act = "online";
deploy.querySelector("b").textContent = "联机对战";
const training = document.createElement("button");
training.className = "nv";
training.dataset.nav = "";
training.dataset.act = "deploy";
training.innerHTML = "<b>单人训练</b><em>离线 · 人机作战</em>";
deploy.after(training);
const page = menus._page(
  "online",
  `
  <header class="topbar"><button class="backb" data-nav data-act="online-back">‹ <span>返回</span></button><h2>联机作战</h2><span class="online-mode">暮色酒店 · 团队死斗 · 先到 40 分</span></header>
  <div class="online-layout">
    <div class="online-commands">
      <div class="sech"><span id="online-section">好友房间</span><span id="online-members">5 对 5</span></div>
      <div id="online-connect">
        <label class="row online-field" for="online-name"><span>干员呼号</span><input id="online-name" maxlength="20" autocomplete="nickname" placeholder="输入呼号" data-nav></label>
        <button class="nv primary" id="online-create" data-nav data-act="online-create"><b>创建房间</b><em>›</em></button>
        <label class="row online-field" for="online-code"><span>房间码</span><input id="online-code" maxlength="8" autocomplete="off" placeholder="8 位房间码" data-nav></label>
        <button class="nv" id="online-join" data-nav data-act="online-join"><b>加入好友</b><em>输入房间码 ›</em></button>
      </div>
      <div id="online-room" hidden>
        <div class="online-room-code"><span>邀请代码</span><strong id="online-room-code"></strong></div>
        <button class="nv primary" id="online-ready" data-nav data-act="online-ready"><b>准备</b><em>✓</em></button>
        <button class="nv" id="online-start" data-nav data-act="online-start"><b>开始对局</b><em>房主部署 ›</em></button>
        <button class="nv" id="online-resume" data-nav data-act="online-resume" hidden><b>返回战场</b><em>›</em></button>
        <button class="nv" data-nav data-act="online-invite"><b>邀请好友</b><em id="online-invite-label">复制邀请链接 ↗</em></button>
      </div>
      <button class="nv" data-nav data-act="online-gunsmith"><b>枪匠</b><em id="online-loadout">使用当前装备 ›</em></button>
      <button class="nv" id="online-leave" data-nav data-act="online-leave" hidden><b>退出房间</b><em>返回主菜单</em></button>
      <p id="online-status" role="status">创建房间，邀请好友加入。空位自动由人机补齐。</p>
      <p id="online-error" role="alert" hidden></p>
    </div>
    <div class="online-squad">
      <div id="online-brief"><div class="kicker">SOLSTICE HOTEL · SKY LOBBY · 47F</div><h3>并肩<span>作战</span></h3><div class="online-versus">5 <i>v</i> 5</div><div class="sech">团队死斗 / TEAM DEATHMATCH</div><p>双方各 5 名干员，先获得 40 次击杀的队伍获胜。</p><p>空位自动填充人机，好友加入后接替人机席位。一个人也可以部署。</p><p>前往枪匠配置武器，准备后由房主开始对局。</p></div>
      <div id="online-roster" hidden><div class="sech"><span id="online-phase">编队准备</span><span>真人优先 · 自动补位</span></div><h3 id="online-result" hidden></h3><div class="tbl" id="online-teams"></div><div class="online-team-actions"><button class="btn" id="online-team-a" data-nav data-act="online-team-a">加入先锋</button><button class="btn" id="online-team-b" data-nav data-act="online-team-b">加入余烬</button></div><p class="online-fill">人机自动就绪 · 真人加入时替换人机 · 断线后由人机暂时代打</p></div>
    </div>
  </div>
  <footer class="legend"><span><kbd>↑</kbd><kbd>↓</kbd> 导航</span><span><kbd>ENTER</kbd> 选择</span><span><kbd>ESC</kbd> 返回</span></footer>`,
);
page.addEventListener("click", (event) => {
  const button = event.target.closest("button[data-act]");
  if (button) menus._act(button.dataset.act);
});
// Text fields must receive WASD, space and enter without menu navigation.
const originalKey = menus._key;
menus._key = function (event) {
  if (this.page === "online" && event.target.matches("input")) {
    if (event.code === "Escape") {
      event.preventDefault();
      event.target.blur();
      this.back();
    }
    if (event.code === "Enter" && event.target.id === "online-code") {
      event.preventDefault();
      join(false);
    }
    return;
  }
  return originalKey.call(this, event);
};
const originalGo = menus.go;
menus.go = function (name, force) {
  originalGo.call(this, name, force);
  if (name === "online") {
    this.root.dataset.bg = room ? "dim" : "menu";
    const first = page.querySelector("[data-nav]:not([hidden]):not(:disabled)");
    first?.focus({ preventScroll: true });
    updateLoadout();
  }
};
const originalBack = menus.back;
menus.back = function () {
  if (this.page === "gunsmith" && this.gs.mode === "camo")
    return originalBack.call(this);
  if (returnPage && ["gunsmith", "settings"].includes(this.page)) {
    const target = returnPage;
    returnPage = null;
    this.go(target);
    updateLoadout();
    return;
  }
  if (this.page === "online") {
    this.go(inMatch() ? "pause" : "main");
    return;
  }
  return originalBack.call(this);
};
const originalAct = menus._act;
menus._act = function (action, direction) {
  if (actions[action]) {
    this.cb.sound("click");
    actions[action]();
    return;
  }
  return originalAct.call(this, action, direction);
};
const originalResume = menus.cb.onResume,
  originalQuit = menus.cb.onQuit;
menus.cb.onResume = () => (engine.active ? resume() : originalResume());
menus.cb.onQuit = () => (engine.active ? leave() : originalQuit());
const roomButton = document.createElement("button");
roomButton.className = "nv";
roomButton.dataset.nav = "";
roomButton.dataset.act = "online";
roomButton.innerHTML = "<b>查看房间</b><em>编队 · 邀请好友</em>";
roomButton.hidden = true;
menus.pages.pause.querySelector(".vnav").append(roomButton);
const dragButton = roomButton.cloneNode(true);
dragButton.dataset.act = "online-drag";
dragButton.innerHTML = "<b>拖动瞄准</b><em>当前窗口的备用操作</em>";
menus.pages.pause.querySelector(".vnav").append(dragButton);

function updateLoadout() {
  const names = {
    ar: "KR-7 CARBINE",
    smg: "VESPER 9",
    shotgun: "M12 BREACHER",
  };
  label("online-loadout", (names[ctx.settings.primary] || names.ar) + " ›");
  if (engine.active && engine.primary !== ctx.settings.primary) {
    engine.primary = ctx.settings.primary;
    if (!inMatch()) {
      ctx.arsenal.reset(engine.primary);
      engine.applySpecs();
    }
    if (!inMatch()) command({ type: "equip", weapon: engine.primary });
  }
}
function openRoom() {
  if (engine.active) {
    engine.dragActive = false;
    engine.input.unlock();
    engine.input.keys.clear();
    engine.gh.G.state = inMatch() ? "paused" : "menu";
  }
  ctx.hud.show(inMatch() ? "pause" : "menu");
  menus.go("online");
  renderRoom();
}
function resume(drag = false) {
  if (!inMatch()) return openRoom();
  engine.gh.G.state = "playing";
  ctx.hud.show(null);
  if (drag) engine.enableDrag();
  else engine.lock();
}
function status(value) {
  label("online-status", value);
}
function error(value) {
  label("online-error", value);
  visible("online-error", true);
}
async function command(value) {
  try {
    await net.send(value);
  } catch (e) {
    error(e.message);
  }
}
async function join(create) {
  if (busy || room) return;
  const code = $("online-code").value.trim().toUpperCase();
  if (!create && !/^[A-F0-9]{8}$/.test(code))
    return error("请输入完整的 8 位房间码。");
  busy = true;
  $("online-create").disabled = $("online-join").disabled = true;
  visible("online-error", false);
  status("正在连接战场…");
  try {
    const response = await fetch("/api/config", { cache: "no-store" });
    if (!response.ok) throw Error("联机服务未启动，请通过 Go 服务打开游戏。");
    const config = await response.json();
    await engine.enter(ctx.settings.primary);
    const name = $("online-name").value.trim() || "干员";
    try {
      localStorage.setItem("gh-pvp-name", name);
    } catch {}
    await net.connect({ name, room: create ? "" : code, create }, config);
    await net.send({ type: "equip", weapon: ctx.settings.primary });
    roomButton.hidden = false;
    menus.pages.pause.querySelector("h1").textContent = "战术菜单";
  } catch (e) {
    error(e.message);
    status("连接未完成");
    await net.leave();
    engine.exit();
    room = null;
    renderRoom();
    openRoom();
  } finally {
    busy = false;
    $("online-create").disabled = $("online-join").disabled = false;
  }
}
async function leave() {
  await net.leave();
  engine.exit();
  room = null;
  returnPage = null;
  roomButton.hidden = dragButton.hidden = true;
  menus.pages.pause.querySelector("h1").textContent = "已暂停";
  visible("online-error", false);
  renderRoom();
  menus.go("main");
  status("已退出房间。空位由人机接替。");
}
async function invite() {
  const url = new URL(location.href);
  url.search = "";
  url.searchParams.set("room", room.room);
  try {
    await navigator.clipboard.writeText(url.href);
    label("online-invite-label", "已复制 ✓");
  } catch {
    status("邀请链接：" + url.href);
  }
}
const actions = {
  online: openRoom,
  deploy: async () => {
    if (room) await leave();
    originalAct.call(menus, "deploy", 1);
  },
  "online-back": () => menus.back(),
  "online-create": () => join(true),
  "online-join": () => join(false),
  "online-ready": () => command({ type: "ready", ready: !current()?.ready }),
  "online-start": () => command({ type: "start" }),
  "online-team-a": () => command({ type: "team", team: 0 }),
  "online-team-b": () => command({ type: "team", team: 1 }),
  "online-invite": invite,
  "online-leave": leave,
  "online-resume": () => resume(),
  "online-drag": () => resume(true),
  "online-gunsmith": () => {
    returnPage = "online";
    menus.go("gunsmith");
  },
};
function renderRoom() {
  visible("online-connect", !room);
  visible("online-brief", !room);
  for (const id of ["online-room", "online-roster", "online-leave"])
    visible(id, !!room);
  if (!room) {
    label("online-section", "好友房间");
    label("online-members", "5 对 5");
    return;
  }
  const me = current(),
    active = inMatch();
  if (menus.page === "online") menus.root.dataset.bg = "dim";
  const humans = room.players.filter((p) => !p.bot).length;
  label("online-section", room.host === net.id ? "房主 · 编队" : "好友 · 编队");
  label("online-members", `${humans} 真人 / ${10 - humans} 人机`);
  label("online-room-code", room.room);
  label(
    "online-phase",
    active ? "对局进行中" : room.phase === "over" ? "对局战绩" : "编队准备",
  );
  const table = $("online-teams");
  table.replaceChildren();
  for (let team = 0; team < 2; team++) {
    const section = document.createElement("section");
    section.className = "t" + team;
    const heading = document.createElement("div");
    heading.className = "th";
    const title = document.createElement("b");
    title.textContent = team ? "余烬" : "先锋";
    const score = document.createElement("em");
    score.textContent =
      active || room.phase === "over" ? room.scores[team] : "5 / 5";
    heading.append(title, score);
    section.append(heading);
    for (const [index, p] of room.players
      .filter((p) => p.team === team)
      .entries()) {
      const row = document.createElement("div");
      row.className = "rw online-member" + (p.id === net.id ? " me" : "");
      const n = document.createElement("span");
      n.textContent = String(index + 1).padStart(2, "0");
      const name = document.createElement("span");
      name.textContent = p.name + (p.id === net.id ? "（你）" : "");
      const state = document.createElement("span");
      state.textContent =
        active || room.phase === "over"
          ? p.kills + " / " + p.deaths
          : p.bot
            ? "人机 · 就绪"
            : !p.connected
              ? "人机代打"
              : p.ready
                ? "已准备"
                : "未准备";
      const role = document.createElement("small");
      role.textContent =
        p.id === room.host ? "房主" : p.bot ? "自动补位" : "真人干员";
      name.append(role);
      row.append(n, name, state);
      section.append(row);
    }
    table.append(section);
  }
  visible("online-ready", !active);
  visible("online-start", !active && room.host === net.id);
  visible("online-resume", active);
  $("online-ready").querySelector("b").textContent = me?.ready
    ? "取消准备"
    : "准备";
  $("online-start").disabled = !room.players
    .filter((p) => !p.bot && p.connected)
    .every((p) => p.ready);
  for (const id of ["online-team-a", "online-team-b"]) $(id).disabled = active;
  visible("online-result", room.phase === "over");
  if (room.phase === "over")
    label(
      "online-result",
      room.winner < 0
        ? "平局"
        : room.winner === me?.team
          ? "任务完成 · 胜利"
          : "任务结束 · 再战",
    );
  status(
    active
      ? "战场持续运行，空位由人机补齐。"
      : me?.ready
        ? "你已准备。等待房主部署。"
        : "请准备。一个人也能开始，双方空位由人机补齐。",
  );
}
net.addEventListener("status", ({ detail }) => {
  status(detail);
  if (detail.includes("中断") && inMatch()) engine.pause();
});
net.addEventListener("error", ({ detail }) => error(detail));
net.addEventListener("event", ({ detail: e }) => {
  engine.event(e);
  if (e.type === "error") return error(e.message);
  if (e.type === "welcome") visible("online-error", false);
  if (e.type === "room") {
    room = e;
    if (room.phase === "over") {
      engine.gh.G.state = "menu";
      ctx.hud.show("menu");
      menus.go("online");
    }
    engine.setRoom(e);
    renderRoom();
  }
  if (e.type === "round-end") openRoom();
});
net.addEventListener("snapshot", ({ detail: s }) => {
  engine.snapshot(s);
  if (room) {
    room.scores = s.scores;
    for (const p of s.players) {
      const member = room.players.find((q) => q.id === p.id);
      if (member) {
        member.kills = p.kills;
        member.deaths = p.deaths;
        member.score = p.score;
      }
    }
    if (performance.now() - lastRosterAt > 500) {
      lastRosterAt = performance.now();
      if (menus.page === "online") renderRoom();
      if (menus.page === "pause") menus._refreshPause();
    }
  }
});
engine.addEventListener("lockerror", () => {
  dragButton.hidden = false;
  if (!inMatch()) return;
  resume(true);
  ctx.hud.hint("当前窗口已启用拖动瞄准：按住鼠标拖动视角，WASD 移动。", 7);
});
engine.addEventListener("lock", ({ detail: locked }) => {
  if (!locked && inMatch() && engine.gh.G.state === "playing") engine.pause();
});
setInterval(() => {
  if (net.writer) command({ type: "ping", nonce: performance.now() });
}, 1500);
try {
  $("online-name").value = localStorage.getItem("gh-pvp-name") || "干员";
} catch {}
$("online-code").value = (query.get("room") || "").toUpperCase();
if (query.has("room") || query.has("pvp")) openRoom();
