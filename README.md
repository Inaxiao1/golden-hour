# 暮色酒店 · GOLDEN HOUR PvP

基于酒店 3D 原型，新增 Go 房间制团队死斗，优先使用原生 WebTransport / HTTP/3；不支持或建链失败时自动切换 WebRTC DataChannel。服务端决定玩家位置、命中、伤害、弹药、重生与比分。

## 本地试玩

需要 Go **1.26+**，无需前端构建或 npm 依赖：

```sh
go run ./cmd/server
```

打开 **http://localhost:8080/**，从原生主菜单选择「联机对战」。创建房间或输入好友房间码，真人点「准备」，房主点「开始对局」。双方各 5 个席位，空位自动由人机补满；一个人也能开始。好友加入时替换人机，退出后人机补位。进入战场后使用 WASD、鼠标左键开火、右键瞄准、Shift 冲刺、空格跳跃、C 蹲下、R 换弹、1/2 切枪、Tab 战绩、Esc 释放鼠标。

也可以运行 `./scripts/dev.sh`。本地已构建时会直接使用 `bin/golden-hour-server`。

本机开发自动生成有效期 13 天的 ECDSA 证书，保存在被 Git 忽略的 `.local/`。浏览器通过 `serverCertificateHashes` 校验证书指纹，不需要安装根证书或跳过浏览器安全提示。开发页面必须使用本机 localhost / 127.0.0.1；发给异地好友需要按下文部署 HTTPS 公网服务器。

限制鼠标锁定的内嵌窗口会自动切换到拖动瞄准，避免开局停在暂停页；按住鼠标拖动视角。独立浏览器使用标准 Pointer Lock。移动端触屏双摇杆尚未实现。

联机客户端兼容 `datagrams.writable` 和 Safari 较新的 `datagrams.createWritable()` 接口，并接收 TypedArray / ArrayBuffer 快照。服务器显式通告 WebTransport 会话的初始流与数据额度，避免 Safari 在握手后一直等待流创建。配置请求最多等待 5 秒，完整联机建链最多 12 秒；失败后恢复按钮，退出操作最多等待 500 毫秒，不会因挂起的网络写入一直置灰。

WebTransport 不可用时自动使用 WebRTC；WebTransport 建链失败后也会尝试 WebRTC，每次建链分别限制 12 秒。房间不存在等业务错误直接提示，不切换连接重复请求。WebRTC 的可靠通道传房间和战斗事件，无序且不重传的通道传输入与快照；两种连接的玩家可以同房对战，继续由 Go 服务端裁决。房间状态会显示实际连接方式。需要单独验证兜底时，可打开 `http://localhost:8080/?transport=webrtc`。

## 已实现

- 原生主菜单联机入口、原生枪匠装备、原生暂停页和战斗 HUD；没有独立悬浮入口或替换 HUD。
- 8 位房间码、邀请链接、最多 10 名真人、两队各 5 个席位、换队、准备和房主开局。
- 空位自动由 Go 服务端人机补满；真人接替人机，离开后自动补位，断线保留的 20 秒内由 AI 代打。人机沿酒店 119 个导航节点寻路，按视线、反应时间和瞄准误差交火，复用服务端碰撞、弹药、换弹、伤害和重生规则。最后一名真人离开或过期后回收房间。
- 房主断线后迁移、20 秒断线保留和带随机会话凭据的自动重连；旧连接不能踢掉恢复后的会话。
- 3 秒倒计时、8 分钟 / 40 分团队死斗、双方比分、击杀播报、Tab 战绩、结算与再次准备开局。
- 服务端 60 Hz 固定步长移动与碰撞，30 Hz 二进制快照；10 人快照 664 字节。
- 客户端预测与服务端校正、远端角色 100 ms 插值、武器 / 站姿 / 装填 / 存活状态同步。
- 四类武器、服务端射速 / 弹药 / 换弹判定、墙体遮挡、爆头、关闭友伤、重生保护、3 秒重生、脱战回血。
- 最多 200 ms 的目标命中盒回溯；射手位置始终来自服务端。客户端不能提交位置、伤害值或命中结论。
- 精确 Origin 校验、有限的消息 / 房间 / 会话容量、发送速率限制、慢客户端处理、退出确认，以及静态文件访问白名单。

PvP 的规则与模拟代码均为新增。`dist/game.js` 提供原型的原生菜单、HUD、画面、角色、武器动画和音效，通过 `client/engine.js` 适配。原型单人训练模式仍可使用。

## 低配性能与显卡

原生「设置 → 画面」新增「自动」画质，并显示当前 WebGL 显卡、FPS、画质与实际渲染分辨率。本机确认使用 WebGL 2 / ANGLE Metal / Apple M6 硬件加速；其他设备以设置页显示的设备为准。若显示 SwiftShader / llvmpipe 等软件渲染，请检查浏览器硬件加速、显卡驱动及远程桌面限制。游戏已有 `powerPreference: high-performance`，浏览器最终选择哪块 GPU 由系统决定。

- 首次升级会将旧的高画质默认值迁移到自动画质、关闭运动模糊，保留武器、音量、灵敏度等设置。此后手动选择的画质会保存。
- 自动模式从中画质起步；低核心数 / 小内存 / 软件渲染设备从低画质起步。只在前台对局持续掉帧时降至低画质，再逐步将分辨率缩放降至 55% / 45%；有启动宽限与切换冷却，不因菜单、单帧尖峰或后台切换降档。
- 低画质关闭泛光、AO、反射，阴影改为 1K / 10 Hz；中画质减少反射分辨率与更新频率、光束采样，关闭景深与武器模糊通道。高 / 极高画质仍可手动选择。
- 对局最多渲染 60 FPS，菜单 30 FPS，暂停 / 结算 20 FPS，后台 5 FPS。小地图 10 Hz；低画质 PvP 装饰物物理 30 Hz，玩家预测与服务端碰撞仍为 60 Hz，快照仍为 30 Hz。
- 服务端射线与移动碰撞复用 4 米空间网格和查询戳，避免全图扫描与每次查询建立临时集合；客户端预测复用候选缓存与数值网格键。移动输入固定 25 字节，发送缓存复用；快照队列保持只保留最新状态。

复现服务端基准：`go test ./internal/game -run '^$' -bench Hotel -benchmem`。对照数据、适用范围与验证记录见 [性能记录](docs/performance.md)。浏览器诊断仅保存在本页 DOM，不上传。

原型只有打包文件，因此渲染层改动由 `node tools/optimize-renderer.mjs` 以唯一匹配校验的补丁生成；重复运行不会重复修改。更换上游构建后需重新适配补丁与地图导出。

## 启动缓存与预编译

静态资源按内容生成版本 URL，使用一年不可变缓存、gzip 和 ETag；HTML 每次校验版本，房间配置与健康接口不缓存。服务器启动时读取并压缩公开资源，运行中不重复压缩；编辑前端文件后请重启服务，使新版本 URL 生效。私钥、仓库、测试文件及 Go 源码不会进入公开资源表。

武器缩略图、轮廓图与迷彩预览已预烘焙在 `assets/armoury.json`，首次启动也直接复用。材质像素首次生成后在空闲时写入 IndexedDB，下次启动直接恢复，保留 UV、色彩空间与过滤参数。缓存按游戏构建哈希隔离，更新后自动重建；存储被禁用、缓存损坏或空间不足时回退到原始生成流程。不会缓存账号、房间或比赛状态。

武器材质使用 `compileAsync` 并行准备，在进入菜单前等候编译完成，避免逐把同步阻塞；修复了启动时额外生成一把默认步枪的问题。WebGL 程序属于当前显卡和上下文，浏览器会管理驱动编译缓存，应用不存储跨设备 GPU 二进制。

更新武器模型、迷彩或上游构建后，先运行 `npm run optimize`，再运行 `npm run bake-startup`，打开工具输出的本机地址，等「启动资源预烘焙完成」。工具会更新预烘焙文件并记录构建哈希；重启 Go 服务即可生效。哈希不匹配的预生成资源会安全回退。

## 文件结构

```text
cmd/server/              Go 启动入口、配置、退出信号
internal/transport/      HTTP/3、WebTransport、WebRTC 信令与 DataChannel、连接生命周期
internal/game/           房间、服务端模拟、命中与比赛、二进制协议
internal/game/maps/      酒店 735 个静态碰撞体与 119 个导航节点（嵌入 Go 二进制）
internal/certutil/        开发证书生成、复用和指纹
client/net.js            连接选择、重连、共享协议与预测接入
client/webrtc.js         WebRTC 双 DataChannel 适配、背压与取消
client/simulation.js     与 Go 同步的固定步长预测
client/engine.js         3D 原型适配、远端角色、武器反馈
client/telemetry.js      自动画质、帧耗时与显卡诊断、小地图/装饰物降频
client/performance-policy.js  自动画质策略、启动宽限与冷却
client/bootstrap.js      启动预加载、本地缓存恢复、异步编译任务
client/startup-cache.js  材质像素与武器图像缓存校验
assets/armoury.json      预烘焙武器与迷彩图像
client/pvp.js + pvp.css  接入原生菜单、房间编队与邀请
docs/protocol.md         网络协议与权威边界
tools/export-map.mjs     从原型重新导出碰撞地图与导航图
tools/optimize-renderer.mjs  原型渲染层的可重放性能补丁
tools/optimize-startup.mjs   启动缓存与预编译的可重放补丁
tools/bake-startup.mjs       从原生渲染器预烘焙武器与迷彩图像
```

更新游戏构建后，运行 `node tools/export-map.mjs` 并打开 http://localhost:3001/，等待「已导出」提示。碰撞数据写入 `internal/game/maps/hotel.json`；重新运行 Go 测试以验证出生点，再发布。

## 验证与构建

```sh
go test ./...
go test -race ./...
go vet ./...
npm test
mkdir -p bin
CGO_ENABLED=0 go build -buildvcs=false -trimpath -o bin/golden-hour-server ./cmd/server
```

测试覆盖真实 QUIC / HTTP/3 两客户端联机、WebTransport 与 WebRTC 混合对战、真实 ICE / DTLS / SCTP、WebRTC 单人开局和九个人机、可靠事件与二进制输入快照、命中击杀结算、跨传输会话恢复、退出确认、信令跨源与非法请求拒绝，以及人机、物理、枪械和 Go/JS 预测一致性。浏览器单元测试还覆盖自动切换、建链超时、资源取消和发送队列上限。race 检查需要本机 C 编译器。

## 公网运行

使用受信任 CA 签发的域名证书，为 WebTransport 同时开放 **TCP 和 UDP 的同一个端口**，并为 WebRTC 开放 **UDP 8444**（`-rtc-addr` 可改）。普通 HTTP 反向代理不会自动代理 WebTransport，也不会代理 WebRTC 的 ICE 数据端口；`/api/rtc/offer` 是同源 HTTPS 信令接口，可以随页面通过反向代理。

```sh
./bin/golden-hour-server \
  -addr=:443 \
  -http-addr= \
  -public-url=https://game.example.com \
  -origins=https://game.example.com \
  -rtc-addr=:8444 \
  -rtc-public-ip=203.0.113.10 \
  -cert=/etc/golden-hour/fullchain.pem \
  -key=/etc/golden-hour/privkey.pem
```

将示例 `203.0.113.10` 替换为服务器实际公网 IP；直接绑定公网 IP 的主机可省略 `-rtc-public-ip`，位于 NAT / Docker 后时需设置，并将公网 UDP 端口原样映射到 `-rtc-addr`。游戏页、`/api/config`、`/connect` 和 `/api/rtc/offer` 由同一个 Go 进程提供。正式证书模式不返回开发指纹，并要求关闭明文 HTTP 开发端口。浏览器需支持 WebTransport 或 WebRTC，且页面必须使用 HTTPS（本机 localhost 除外）。

只配置直连不能穿过所有网络限制。需要 TURN 中继时，通过 `GH_ICE_SERVERS` 环境变量或 `-ice-servers` 设置标准 ICE server JSON，例如：

```sh
export GH_ICE_SERVERS='[{"urls":["stun:turn.example.com:3478"]},{"urls":["turns:turn.example.com:5349?transport=tcp"],"username":"short-lived-user","credential":"short-lived-secret"}]'
```

该配置会下发浏览器并用于 Go 端 ICE；请使用可供游戏客户端使用的限时 TURN 凭据。TURN 服务需另行部署，实际可达性取决于该服务和网络配置。

Docker 示例（证书目录须对容器运行用户可读）：

```sh
docker build -t golden-hour .
docker run --rm -p 8443:8443/tcp -p 8443:8443/udp -p 8444:8444/udp \
  -v /etc/golden-hour:/certs:ro golden-hour \
  -addr=:8443 -http-addr= \
  -public-url=https://game.example.com:8443 \
  -origins=https://game.example.com:8443 \
  -rtc-public-ip=203.0.113.10 \
  -cert=/certs/fullchain.pem -key=/certs/privkey.pem
```

`GET /api/health` 返回在线人数、房间数和服务端 tick。房间存在于单个进程内存中，重启后清空；多实例需按房间路由到同一个进程。本版不包含账号、自动匹配、持久化战绩，以及 PvP 军犬、投掷物、近战和动态环境破坏同步。

游戏名称、角色与原有美术素材版权归其各自权利人所有。
