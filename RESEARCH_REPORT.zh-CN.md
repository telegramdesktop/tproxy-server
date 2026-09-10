# telegramdesktop/tproxy-server 完整深度调研报告

> 调研对象：https://github.com/telegramdesktop/tproxy-server（master 分支，commit `f7a6acc4d536a787d442fd7df3ba4ebfd728f406`）
> 调研日期：2026-09-06
> 调研方式：完整克隆仓库，逐文件精读全部 49 个受版本控制文件（含 git 历史、被删除文件、GitHub 仓库元数据、全部 14 个 issue/PR），并本地运行全部 Go 测试与 `go vet` 验证
> 报告语言：中文

---

## 目录

1. [执行摘要](#1-执行摘要)
2. [仓库概况与元数据（看得见与看不见的）](#2-仓库概况与元数据看得见与看不见的)
3. [项目背景与定位](#3-项目背景与定位)
4. [完整文件清单（49/49）](#4-完整文件清单4949)
5. [Git 历史与演化时间线](#5-git-历史与演化时间线)
6. [系统架构总览](#6-系统架构总览)
7. [协议规范深度解析（PROTOCOL.md 全量拆解）](#7-协议规范深度解析protocolmd-全量拆解)
8. [Go 服务端源码逐文件精读](#8-go-服务端源码逐文件精读)
9. [Bridge 前端 JavaScript 逐段精读](#9-bridge-前端-javascript-逐段精读)
10. [部署体系逐文件精读（deploy/ 全部 13 个文件）](#10-部署体系逐文件精读deploy-全部-13-个文件)
11. [安全设计专项分析](#11-安全设计专项分析)
12. [资源限制与配额体系全表](#12-资源限制与配额体系全表)
13. [性能模型与吞吐量上界](#13-性能模型与吞吐量上界)
14. [测试体系全量盘点（77 个测试函数）](#14-测试体系全量盘点77-个测试函数)
15. [客户端平台文档要点（ANDROID.md / IOS.md）](#15-客户端平台文档要点androidmd--iosmd)
16. [社区状态：全部 14 个 Issue 与 PR](#16-社区状态全部-14-个-issue-与-pr)
17. [代码质量与安全评估](#17-代码质量与安全评估)
18. [本地验证记录（本次调研实测）](#18-本地验证记录本次调研实测)
19. [结论与建议](#19-结论与建议)
20. [附录：常量速查表 / 术语表 / 复现命令](#20-附录常量速查表--术语表--复现命令)

---

## 1. 执行摘要

**一句话定位**：`tproxy-server` 是 Telegram 官方（telegramdesktop 组织、Telegram Desktop 主开发 John Preston 个人署名）开发的 **WEB 代理（WEB proxy）概念验证服务端**——即"托管在服务器上的一半"。它让 Telegram 客户端把原本直连 MTProxy 的 TCP 连接，全部塞进**一个由 App 持有的 WebView（网页视图）**里，通过普通 HTTPS 网站（或 WebSocket）作为"载波（carrier）"传输，服务端再把多路复用的逻辑流还原成到本机官方 MTProxy 的 TCP 连接。

**本质**：这是一个**抗审查/抗封锁的流量伪装中继**。用户的代理地址看起来就是一个普通 HTTPS 网站；MTProto 流量被封装在看起来像普通网页 fetch/轮询的请求里，穿过后被解复用转发给本地官方 MTProxy。服务端全程不接触明文、不选择目的地、不持有客户端可见的密钥之外任何机密。

**技术形态**：Go 1.20+ 单二进制（约 8000 行含测试），标准库 + gorilla/websocket v1.5.3 唯一外部依赖；前端是一段约 370 行的零依赖内联 JavaScript；部署由 737 行 Bash + Caddy + systemd + nftables 组成；文档 5 篇共约 2500 行，规格之细致远超一般 PoC。

**成熟度判断**：概念验证（PoC）后期形态——功能完整（4 种载波模式、全套限额、防主动探测加固、自动安装/回滚），但明确声明了非目标（无跨重启会话恢复、无 H3/WebTransport、无 CDN、移动端仅前台运行），且 t.me 尚未注册 `webproxy` 链接路由。仓库无 LICENSE、无 CI、单一维护者。

**本次调研实测**：全部 5 个 Go 包测试通过（`ok ... 0.003s~1.302s`），`go vet` 零告警，Go 1.26.5 可直接构建。

---

## 2. 仓库概况与元数据（看得见与看不见的）

### 2.1 GitHub API 实测数据（2026-09-06 抓取）

| 项目 | 值 | 备注 |
|---|---|---|
| full_name | `telegramdesktop/tproxy-server` | |
| description | "Proof-of-concept WEB proxy server for Telegram" | 官方自述 |
| created_at | **2026-08-10T09:51:17Z** | 当前形态仓库的创建时间 |
| pushed_at | 2026-09-03T19:13:01Z | 最后一次推送 |
| default_branch | `master` | 唯一分支（含 `origin/HEAD` 指向） |
| stars / forks / watchers | 274 / 29 / 7 | 社区关注度中等偏热 |
| open_issues（含 PR） | 12 | 见第 16 节 |
| size | 513 KB | |
| license | **无（None）** | ⚠️ 部署前必须注意：无任何开源许可证 |
| archived | false | 活跃 |
| topics | 无 | 未设置话题 |
| homepage | 无 | |
| 语言构成 | Go（主体）、JavaScript（内嵌于 Go 字符串）、Bash（部署）、Markdown（文档）、JSON（示例配置） | |

### 2.2 "看不见"的仓库事实

以下细节不在 README 里，需要深挖 git 与 API 才能看到：

1. **仓库被整体重写过**。GitHub API 显示 created_at 为 2026-08-10，而 git 历史 initial commit 为 2026-08-09 22:40——`telegramdesktop/tproxy-server` 这个名字历史上长期托管的是一个 **C 语言实现的 MTProxy 变体**（TelegramMessenger/MTProxy 的衍生项目）。当前仓库为 2026 年 8 月推倒重来的全新 Go 项目，旧的 C 代码与历史在当前 git 历史中**零残留**（`git log --all` 仅 23 个提交、单人、无合并提交）。
2. **无 tag、无 release**：`git tag` 输出为空，从未打过版本号。
3. **无 `.github/` 目录**：没有任何 CI（GitHub Actions）、议题模板、行为准则、贡献指南。所有质量保障都在本地测试与部署脚本里。
4. **无 LICENSE**：代码在版权上"保留所有权利"状态，第三方商用/自部署在法律上是灰色的。
5. **无 CHANGELOG**：变更史只能读 git log。
6. **作者唯一**：22 次提交全部来自 `John Preston <johnprestonmail@gmail.com>`（Telegram Desktop 的首席开发者），提交时区 +0400，2026-08-09 至 2026-09-03 期间以约每 1-2 天 1-3 个提交的节奏推进，最后一周连续两次安全加固（主动探测防护 + websocket-lanes DDoS 修复）。
7. **仓库历史上曾有内置演示网站**：commit `e0a45ed`（2026-08-18 "Remove the website, provider should choose it's own"）删除了 `web/public/` 下 7 个文件（`404.html`、`about.html`、`favicon.svg`、`index.html`、`privacy.html`、`robots.txt`、`styles.css`）——理由写得很清楚：**所有运营商都用同一套起始网站，会变成主动探测的指纹**。这是"看得见的设计决策"，但被删的文件内容需要翻 git 才能看到。
8. `.gitignore` 揭示的本地开发习惯：忽略 `/tproxy-server`（本地构建产物）、`/config.json`、`/profiles.json`（本地真实配置）、`/websites/`（本地网站注册表目录）、`/coverage.out`、`/*.test`、`/*.prof`（Go 测试/覆盖率/性能分析产物）、`.DS_Store`（作者在 macOS 上开发）。
9. **go.mod 揭示的约束**：module 名为 `github.com/telegramdesktop/tproxy-server`；`go 1.20` 是最低版本；唯一依赖 `github.com/gorilla/websocket v1.5.3`，go.sum 双行校验齐全。文档声明"能用标准库就用标准库"。
10. **仓库内部交叉引用了未公开的姊妹仓库**：PLAN.md/PROTOCOL.md/README.md 多处引用 `../tproxy/Telegram/SourceFiles/mtproto/web_proxy/web_proxy_frame.h`、`web_proxy_webview.cpp`、`web_proxy_transport.cpp` 与 `../tproxy/docs/web-proxy-test-plan.md`（Telegram Desktop 侧实现）；ANDROID.md 引用 `../Other/Telegram-Android`；IOS.md 引用 `../Other/Telegram-iOS`。即作者工作区是一个多仓库并列检出的 monorepo 式布局，客户端实现（Desktop 的 `mtproto/web_proxy/`、Android 的 `WebProxyTransport.java`、iOS 的 Swift `WebProxyTransport` 模块）**均不在本仓库内**，本仓库只有服务端半边。
11. **文档中的"冻结 v1 兼容名"**：`tdesktop-web-proxy-bridge-v1`、`#android=<nonce>` 片段、`tproxy-android-init` 控制消息、`android=webview-nonce`——名字带 "android"/"tdesktop" 但明确声明是**冻结的 v1 线缆兼容名，不是平台标识**；改名会使所有已部署 capability 失效。
12. **仓库不含任何机密**：全部 secret 都是 `000102030405060708090a0b0c0d0e0f` 这类文档测试向量。

---

## 3. 项目背景与定位

### 3.1 解决什么问题

传统 MTProxy（Telegram 的专用代理协议）特征明显：非 TLS 的自定义 TCP 协议（或 `ee` 伪 TLS），在深度包检测（DPI）环境下容易被识别、封锁。`ee` 模式模拟 TLS 握手但仍是"假 TLS"，而 `dd` 只是随机填充。

WEB proxy 的思路完全不同：

- **不再自己发明传输层**，直接借用浏览器 WebView 的**真 HTTPS**（真证书、真 TLS 指纹、真 HTTP/2、可加 WebSocket）；
- 客户端 App 在本地把 MTProxy 变换后的字节流交给一个**隐藏的 WebView**，由网页 JS 用 `fetch`/`WebSocket` 与一个看起来完全正常的网站通信；
- 该网站（本仓库的 relay）把多路复用帧还原成到本机官方 MTProxy 的 TCP 连接。

由此获得三个关键属性（README/PLAN 原文归纳）：

1. **流量即普通网站流量**：DPI 看到的是 Caddy(HTTPS) + 网页轮询/WSS，与任何正常站点无形态差异；
2. **服务端零解密**：relay 收到的 DATA 是 App 完成 MTProxy 变换后的**不透明字节**，relay 无法（也无意）解密或选择目的地；
3. **客户端改动量最小化**：复用每个平台现成的 MTProxy 变换（tgnet / MtProtoKit / tdesktop），只是把 socket 目的地址换成 127.0.0.1 的本地 sidecar。

### 3.2 "一个 WebView 传输"的准确含义

README 特别澄清：**"One WebView transport" 指一个逻辑载波与一个 relay 会话，不是一条 HTTP 请求或一条后端连接**。一个 App 的全部 MTProxy TCP 连接（主连接、媒体上传、下载、连接测试等）都被映射为一个 relay 会话内的多个逻辑流（stream），而承载方式由服务端 profile 从 4 种模式中选择：

| 模式 | 载波行为 | 主要取舍 |
|---|---|---|
| `https`（默认/基线） | 一条串行 POST 上行 + 一条长轮询下行 | 保守基线；单方向吞吐上限 ≈ `批次字节数 / RTT` |
| `https-lanes` | 每个逻辑流独立一对 POST/长轮询（lane 0 保留给会话级 PONG） | 模拟 Telegram 原生多 TCP 会话，消除流间队头阻塞；依赖 HTTP/2 并发 |
| `websocket` | 一条有序 WebSocket 复用全部流 | 消除 HTTP 停等窗口；所有流共享同一 TCP 拥塞域与故障域 |
| `websocket-lanes` | 每个逻辑流一条独立 WebSocket | 流间队列/写入器隔离；连接与握手成本上升 |

模式由**服务端 profile（即 secret）决定**并嵌入桥接页，客户端无需新设置、无需实现传输代码——客户端只实现统一的 `TelegramWebProxy` 边界。

### 3.3 与官方后端的关系（不可更改的官方组件）

relay 的后端是**原封不动的官方 MTProxy**（TelegramMessenger/MTProxy，pinned commit `f36d8af769ffaeac36978d38c2c0f6d1104c2137`，构建产物 sha256 `919795c4...fdf4655` 校验），监听本机 2398 端口：

```bash
/opt/MTProxy/objs/bin/mtproto-proxy \
  -u mtproxy -p 8888 -H 2398 -S <16字节hex secret> \
  --aes-pwd /etc/mtproxy/proxy-secret /etc/mtproxy/proxy-multi.conf \
  -M 1 -C 4096
```

文档强调三个"坑"（都是作者实测踩过的）：

- 官方 MTProxy 的 `-H` 是**端口**不是地址，`-H 127.0.0.1:2398` 是非法参数——因此只能靠 nftables + 云商防火墙把 2398/8888 挡在外网之外；
- `proxy-secret`（128 字节）与 `proxy-multi.conf`（含 `default ` 与 `proxy_for ` 行）从 `core.telegram.org` 每日 HTTPS 拉取刷新，内容变了才重启 MTProxy；
- x86_64 only（官方 Makefile 的汇编/时序代码限制），安装器在其它架构直接退出。

### 3.4 项目明确声明的非目标（v1）

PLAN.md 第 14 节列出的**显式非目标**（对理解边界极重要）：

- 独立生产 `/bridge` 路由或无会话凭据的 API 访问；
- 把官方 MTProxy 当作独立公共服务运行；
- 桥接 JS 使用原始 MTProxy secret；
- 流式 fetch、HTTP/3、WebTransport、CDN 前置（*注：后来 v1 实现了 WebSocket 载波，该列表反映的是最初基线，文档未同步修订——这是文档内部一个自相矛盾点*）；
- Telegram Web A/K 集成；通话或 UDP 中继；客户端选择目的地；跨标签页/跨进程/跨 relay 重启的流恢复；`AUTH_CHAL`/`AUTH_RESP` 认证；一个 relay 进程多个公共域名。

---

## 4. 完整文件清单（49/49）

`git ls-files` 共 49 个文件，全量清单如下（✅=已逐行精读）：

```
tproxy-server/
├── .gitignore                      8 行   ✅ 忽略构建产物/本地配置/网站注册表
├── README.md                       636 行 ✅ 运维主文档（安装/验证/限额/多secret/排障）
├── ANDROID.md                      296 行 ✅ Android PoC 架构/加固/构建/测试
├── HARDENING.md                    152 行 ✅ 主动探测加固与令牌迁移方案
├── IOS.md                          300 行 ✅ iOS PoC 设计（WKWebView 边界）
├── PLAN.md                         978 行 ✅ 架构/协议/限额/里程碑/测试清单总纲
├── PROTOCOL.md                     470 行 ✅ 规范性线缆协议（normative）
├── PUBLIC_SITE.md                  111 行 ✅ 公网站点两种后端契约
├── go.mod / go.sum                 5/2 行 ✅ 模块声明与依赖锁定
├── config.example.json             44 行  ✅ 全字段示例（含全部限额默认值）
├── profiles.example.json           10 行  ✅ profile 示例
├── cmd/tproxy-server/main.go       95 行  ✅ 入口：加载配置/监听/优雅停机
├── internal/
│   ├── bridge/page.go              448 行 ✅ 桥接页生成（CSP/nonce/内联JS模板）
│   ├── bridge/page_test.go         237 行 ✅ 渲染策略测试（7个）
│   ├── bridge/runtime_test.go      27 行  ✅ Node vm 运行时测试（1个）
│   ├── bridge/testdata/lane_cancellation.js 67 行 ✅ WS-lane 取消回归测试（DDoS修复）
│   ├── config/config.go            576 行 ✅ 严格配置/主机名校验/capability派生
│   ├── config/config_test.go       281 行 ✅（11个）
│   ├── config/token_key.go         30 行  ✅ 持久签名密钥读取（0400/32B）
│   ├── config/token_key_test.go    33 行  ✅（1个）
│   ├── frame/frame.go              147 行 ✅ 帧编解码/方向校验
│   ├── frame/frame_test.go         46 行  ✅（4个）
│   ├── server/caddy_test.go        377 行 ✅ 可选真Caddy对等性套件（1个）
│   ├── server/parity_test.go       378 行 ✅ 公开/认证行为对等性（5个）
│   ├── server/public_test.go       200 行 ✅ 公开语义保持（2个）
│   ├── server/secrets.go           111 行 ✅ 全元数据凭据识别（防探测核心）
│   ├── server/secrets_test.go      159 行 ✅（4个）
│   ├── server/server.go            763 行 ✅ HTTP/WS 载体处理器+管理端
│   ├── server/server_test.go       940 行 ✅（11个）
│   ├── server/site.go              124 行 ✅ 内存静态站点（ETag/条件/范围）
│   ├── session/manager.go          650 行 ✅ bootstrap/会话/限额/清理循环
│   ├── session/session.go          1593 行✅ 会话状态机/流控/后端流（核心）
│   ├── session/session_test.go     1101 行✅（29个）
│   ├── session/token.go            55 行  ✅ 令牌 HMAC 构造与分类
│   └── session/token_test.go       65 行  ✅（1个）
└── deploy/
    ├── Caddyfile                   44 行  ✅ 唯一公网入口配置
    ├── caddy.service               35 行  ✅ Caddy systemd 单元（最小特权）
    ├── ensure-token-key.sh         32 行  ✅ 密钥制备（硬链接防替换）
    ├── firewall.nft                6 行   ✅ 本机后端端口防火墙
    ├── install-mtproxy.sh          67 行  ✅ 校验固定commit并编译官方MTProxy
    ├── install.sh                  262 行 ✅ 一键安装器（Caddy/Go/MTProxy/全套单元）
    ├── mtproxy.service             33 行  ✅ MTProxy systemd 单元
    ├── refresh-mtproxy-config.service 14 行 ✅ 路由刷新 oneshot
    ├── refresh-mtproxy-config.sh   22 行  ✅ 拉取 proxy-multi.conf（变更才重启）
    ├── refresh-mtproxy-config.timer   11 行 ✅ 每日随机延迟定时器
    ├── tproxy-firewall.service     23 行  ✅ nft 表重应用单元（绑定 nftables.service）
    ├── tproxy-server.service       45 行  ✅ relay 单元（最强沙箱矩阵）
    └── update-relay.sh             143 行 ✅ 原子更新器（测试/校验/回滚/迁移）
```

**统计**：Go 源码（非测试）约 3,350 行；Go 测试约 3,990 行（含 JS 测试数据 67 行）；Bash 737 行；文档 2,943 行。测试代码与产品代码比例约 **1.19 : 1**。

不存在的东西（同样重要）：无 vendor 目录、无 Makefile、无 Dockerfile（社区 PR #8 试图补）、无 LICENSE、无 CI 配置、无 `.github`、无 CHANGELOG、无任何二进制产物入库。

---

## 5. Git 历史与演化时间线

23 个提交（全部 John Preston，+0400 时区），按时间正序呈现项目演化：

| # | 日期 | commit | 主题 | 阶段解读 |
|---:|---|---|---|---|
| 1 | 08-09 22:40 | `37c754f` | Initial commit. | PoC 首版（https 基线载波 + 内置演示网站） |
| 2 | 08-10 00:21 | `748ee16` | Apply some fixes. | 快速修错 |
| 3 | 08-10 13:49 | `fc391d5` | Show stats in proxy page. | 桥接页向客户端上报流量统计 |
| 4 | 08-10 14:10 | `4243925` | Add update-relay.sh | 原子更新器（测试/回滚） |
| 5 | 08-10 20:10 | `b0d1701` | Add Android WebView proxy bridge | Android 边界（`#android=nonce` + `TelegramWebProxy`） |
| 6-7 | 08-10 | `02b329b`/`44cae93` | Document WEB proxy availability state / links | 客户端状态显示与分享链接文档 |
| 8-9 | 08-11 | `2dc8f6e`/`353404b` | Document iOS WEB proxy compatibility / carrier log guidance | iOS 兼容文档 |
| 10 | 08-12 | `f2ab6b8` | Make the documentation platform-agnostic. | 去平台化措辞（名字冻结说明的由来） |
| 11 | 08-14 | `df62ea2` | Prepare for work in restricted webviews. | 受限 WebView（禁cookie/存储/worker）适配 |
| 12 | 08-17 17:20 | `e7675f0` | Add HTTPS lanes and WebSocket carriers | **载波扩展**：https-lanes + websocket |
| 13 | 08-17 20:56 | `f401822` | Describe restricting webview to required operations. | 文档：最小能力清单 |
| 14 | 08-18 09:48 | `bdc74e3` | Hide the proxy from non-authenticated clients. | **防探测第一步**：未认证走公网站点 |
| 15 | 08-18 11:39 | `fea13db` | Support websocket-lanes mode. | 第 4 种载波模式 |
| 16 | 08-18 12:12 | `e0a45ed` | Remove the website… | 删除内置网站（防指纹） |
| 17 | 08-18 12:26 | `7dee009` | Add local website registry to gitignore. | `/websites/` 入 gitignore |
| 18 | 08-18 15:11 | `0e0de0e` | Allow requests from different IPs. | bootstrap 不绑定来源 IP（VPN/双栈切换） |
| 19 | 08-18 16:03 | `2873a08` | Remove origin checks. | 不依赖 Origin 头（原生 WebView 可省略、非浏览器可伪造） |
| 20 | 08-24 21:23 | `52a5feb` | Clarify cross-origin requests policy. | 跨域策略澄清 |
| 21 | 09-03 22:50 | `c0e9adf` | **Harden for active probing.** | **大安全加固**（+947/−398 行，13 个文件）：secrets.go 全元数据扫描、HMAC 令牌、token.key、Caddy 对等测试、HARDENING.md |
| 22 | 09-03 23:12 | `f7a6acc` | Fix websocket-lanes ddos problem. | **DDoS 修复**：客户端提前 CLOSE 取消排队中的 WS 握手（+105 行，含 Node vm 回归测试） |

**演化叙事**：8 月上旬快速出 PoC → 8 月中旬把载波从 1 种扩到 4 种并做"去指纹"（删网站）→ 8 月下旬安全语义打磨（IP 无关、无 Origin 依赖）→ 9 月初两连击完成主动探测防护与拒绝服务修复。节奏是一个人高强度迭代的官方实验仓特征。

被删除的 `web/public/` 文件（git 可恢复）：`index.html`(30 行，一个"Connection"标题的极简页)、`about.html`、`privacy.html`、`404.html`、`robots.txt`（内容为 `User-agent: *\nDisallow: `，即允许全部抓取）、`styles.css`（1 行）、`favicon.svg`（1 行 SVG）——本身无害，但"人人一样"就是指纹，故删。

---

## 6. 系统架构总览

### 6.1 部署拓扑（README 权威图）

```text
Internet :80/:443 → Caddy → 127.0.0.1:8080 tproxy-server → 运营商网站
                                              |              （内存静态 或 loopback 应用）
                                              \→ 127.0.0.1:2398 官方 MTProxy → Telegram DC
```

- **只有 Caddy 听公网**（80/443）。relay 公共口 8080、管理口 8081、MTProxy 2398、MTProxy 统计口 8888 全部 loopback；nftables 把 2398/8888 在非 lo 接口上丢弃，云商防火墙是第二道边界。
- Caddy **把所有路径**反代给 relay（不做静态文件旁路），保证"不存在可与公网站点对比的第二个传输面"；桥接页只在 `GET /?bridge=<合法capability>` 出现。
- TLS 证书由 Caddy ACME 管理并 HSTS；HTTP/1.1+HTTP/2（显式禁用了 h3）。

### 6.2 数据面（PLAN 图，加上本文标注）

```text
Telegram app（MTProto + MTProxy 变换后的字节）
   │  本地 sidecar：每个 App TCP 连接 = 一个逻辑流
   ▼
本地 WEB 适配器（loopback 监听）                    [客户端侧，不在本仓库]
   │  一个进程级隐藏 WebView；二进制帧/JSON 控制
   ▼
WebView 主框架 = 桥接页 https://H/?bridge=<cap>#android=<nonce>
   │  同源 fetch 长轮询 / WSS（载波模式由 profile 决定）
   ▼
Caddy :443 → Go relay 127.0.0.1:8080
   │  会话/帧多路复用；每流一条 TCP
   ▼
官方 MTProxy 127.0.0.1:2398 → Telegram DC
```

### 6.3 进程内组件（Go）

| 包 | 职责 | 关键文件 |
|---|---|---|
| `cmd/tproxy-server` | 启动/监听/信号/优雅停机 | main.go（95 行） |
| `internal/config` | 严格 JSON 配置、主机名/回环校验、secret 解码、capability 派生、profile 装载、token.key 读取 | config.go、token_key.go |
| `internal/frame` | 8 字节帧头编解码、客户端方向形状校验、HELLO/WINDOW 语义 | frame.go |
| `internal/session` | 令牌签发/分类（HMAC）、bootstrap 与会话生命周期、全局限额与令牌桶、每会话状态机（序列/游标/重放/流控/墓碑）、每流后端 TCP 读写循环、lane 状态 | manager.go、session.go、token.go |
| `internal/server` | HTTP 路由（公共/内部二分）、桥接页响应、API 端点（session/up/down/ws）、WebSocket 升级与读写泵、防探测元数据扫描（secrets.go）、静态站点、反向代理公共上游、admin（healthz/readyz/metrics/pprof） | server.go、secrets.go、site.go |
| `internal/bridge` | 桥接页模板渲染：18 字节 nonce、CSP、Permissions-Policy、内联 JS 四载波实现注入 | page.go |

### 6.4 并发模型（PLAN 第 8 节 + 代码实证）

- 每会话**一把短持有的互斥锁**串行化全部映射/序列/游标/窗口/墓碑/记账；HTTP 处理器与后端 goroutine 的解析、拨号、socket I/O 都在锁外，只在**状态转换的提交点**进锁。
- 后端每流两个 goroutine（读泵/写泵）+ 拨号 goroutine，由 `context.WithCancel` + `sync.WaitGroup`（`backendWG`）管理；会话关闭 → `close(s.done)` 广播 + 逐个关 socket。
- 管理器层一把大互斥锁管全局记账；指标用 `atomic.Uint64` 无锁读。
- 清理循环每 30 秒 tick：过期 bootstrap 回收、静默超过 `reconnect_grace` 的会话关闭。
- 关停顺序：停止接纳 → 取消长轮询 → 会话失效 → 关后端 socket → 进程退出（systemd 可重启）。V1 停机不发 BYE。

---
## 7. 协议规范深度解析（PROTOCOL.md 全量拆解）

PROTOCOL.md 是**规范性（normative）**文档，定义客户端、桥接页、relay 三方共享的线缆契约。以下逐节全量拆解。

### 7.1 总则

- 所有二进制整数**无符号大端**。
- 客户端先做 Telegram 正常的 MTProxy 变换；本地 WEB 适配器把每条 TCP 连接映射为一个逻辑流并多路复用进一个 WebView 载波会话；桥接页把完整帧批次转换为 profile 选定的载波；relay 把每个逻辑流还原成一条到官方 MTProxy 的 TCP 连接。
- **DATA 载荷在 App 的 MTProxy 变换之后的每一层都是不透明的**。

### 7.2 桥接 URL 与 capability 派生

用户只配置两个值：规范小写 ASCII/IDNA 主机名 `H` 和 MTProxy secret。secret 解码为字节 `S`（选择随机填充模式时保留前导 `dd` 字节），然后：

```text
context   = UTF-8("tdesktop-web-proxy-bridge-v1\n" + H)
bridge    = base64url-no-padding(HMAC-SHA256(key=S, message=context))
URL       = https://H/?bridge=bridge
```

**规范测试向量**（双端共用，config_test.go 与客户端都验证）：

| Host | Secret hex | Capability |
|---|---|---|
| `proxy.example.com` | `000102030405060708090a0b0c0d0e0f` | `MHLEY5PmW1GWqJkSrlmJpvJUiLhBH_QKy6yKg8a0JPk` |
| `proxy.example.com` | `dd000102030405060708090a0b0c0d0e0f` | `IpJrt3e7sKtzPyoXy6w-Zj6GGEvsvclN66JzQEfPYLA` |

关键语义：

- 只有**精确的 `GET /` 且 query 恰好是一个 43 字符规范 base64url 的 `bridge` 参数**才选中桥接页；
- 无真实 relay 凭据的请求→公网站点；真实 capability 出现在非规范 query 或错误路径→**本地 404（no-store）**，绝不透给公网应用；
- `tdesktop-web-proxy-bridge-v1` 是冻结的 v1 域分离标签（改名=轮换所有已部署 capability）；
- plain 与 `dd` secret 派生**不同** capability（有意为之）；同一 secret 的 hex 与 base64url 拼写只要解码字节相同即同一 capability。

### 7.3 客户端-桥接边界（两种，互斥）

**(a) 注入式 WebView 边界（正常原生实现）**

- App 把桥接文档作为 WebView 主框架加载，并附加客户端私有片段：`https://H/?bridge=…#android=webview-nonce`，nonce 为 32 随机字节的规范无填充 base64url（43 字符）。**片段不随 HTTPS 请求发送**。
- 导航前 App 向**精确的 `https://H` 主框架**暴露名为 `TelegramWebProxy` 的页面对象：`postMessage(value)`（页→App）与 `onmessage` 回调（App→页）。平台绑定必须认证"活动 WebView + 主框架 + 精确 origin + 当前导航 + nonce"五要素；通配 origin 与不受限 JS 接口**不符合规范**。
- 桥接页用 `history.replaceState` 抹掉 query 与 fragment，把对象适配为内部 port 契约，然后发送 JSON 控制值：`{"t":"tproxy-android-init","v":1,"nonce":"webview-nonce"}`。App 仅在 nonce 与认证上下文匹配时接受。
- 页↔App 消息为 JSON 控制值或**一个完整共享帧**；平台可用 ArrayBuffer / 共享原生缓冲 / 私有 base64 信封跨 IPC，私有编码在到达桥接页之前剥离。
- 桥接页把聚合的 HTTP 下行体在**已验证的帧边界**切开再过 IPC；客户端 DATA 帧应保持在 relay 的 64 KiB 块上限内。

**(b) 回环父页面边界（可选系统浏览器载波）**

- 客户端控制的 loopback 页面可内嵌 HTTPS 桥接页并转移一个 `MessagePort`：

```javascript
iframe.contentWindow.postMessage(
  {t: 'tproxy-init', v: 1},
  'https://proxy.example.com',
  [channel.port2]);
```

- 桥接页只接受**一次**、仅来自 parent、仅精确对象+一个端口、且 `event.origin` 必须解析为**显式端口的 `http://127.0.0.1:<port>`**。
- 二进制 MessagePort 消息=完整载波批次；控制对象为 `{t:'status',state}`、诊断 `{t:'traffic',up,down}`（非负，校验后可丢弃）、`{t:'close'}`。

### 7.4 加固执行档案（Hardened WebView execution profile）

桥接页**刻意兼容**"网络契约只允许文档读写精确代理源响应数据"的私有 WebView。明确声明：这不是保证浏览器引擎不发跨源请求——代理文档**不得**尝试跨源请求，客户端可阻断，其网络行为不作规范。

桥接页的技术白名单（全部、仅有）：Fetch、`AbortController`、定时器、同文档 history 替换、TypedArray、一个认证过的原生/MessagePort 边界。HTTPS 请求固定 `mode:'same-origin'`、`credentials:'omit'`、`cache:'no-store'`、`redirect:'error'`、`referrerPolicy:'no-referrer'`。

**禁用清单**（客户端因此可整体关闭而不影响协议）：外部脚本/样式/字体/图片/帧/对象/媒体/manifest；cookies、DOM 存储、IndexedDB、Cache Storage、Service Worker、专用/共享 Worker、弹窗、下载、表单、WebRTC、设备权限、剪贴板、跨源请求。**必须保留**：nonce 内联脚本、精确源 HTTPS、profile 选定 WSS 时的同源 wss、普通定时器、选定的认证边界。

**参考 CSP**（响应头实际下发，逐字段）：

```text
default-src 'none';  base-uri 'none';  child-src 'none';
connect-src 'self' wss://H;
font-src 'none';  form-action 'none';
frame-ancestors http://127.0.0.1:*;
frame-src 'none';  img-src 'none';  manifest-src 'none';
media-src 'none';  object-src 'none';
script-src 'nonce-<per-response nonce>';
style-src 'none';  worker-src 'none';
sandbox allow-same-origin allow-scripts
```

- `allow-scripts`：载波由页脚本实现，必需；
- `allow-same-origin`：让 Fetch 发出精确 `https://H` origin 而非 opaque null；
- `frame-ancestors http://127.0.0.1:*`：保留可选浏览器载波，同时不让任意站点内嵌。

配套响应头：`Cache-Control: no-store`、`Referrer-Policy: no-referrer`、`X-Content-Type-Options: nosniff`、`X-DNS-Prefetch-Control: off`、`Permissions-Policy`（拒绝 autoplay/采集设备/传感器/剪贴板/屏幕采集/支付/唤醒锁/USB/HID/串行等全部未用能力）。**刻意不发** `X-Frame-Options`/COOP/COEP（会杀死回环父边界）。

文档同时声明：这些头只描述并保护参考实现；加固的 Telegram 载波必须独立施加自己的导航/响应隔离/请求阻断/存储/媒体/权限限制，因为**另一个代理运营商控制其自己的文档与响应头**（信任模型的坦白）。

### 7.5 载波模式与请求规范

通用规则：

- 载波请求通常带 `Origin: https://H`，但 relay **不认证该头**（原生 WebView 可省略、非浏览器可伪造）；认证来自不可猜测的 bootstrap/会话 bearer。
- HTTP 载波请求**无 cookies**；relay 拒绝带 cookie 的 HTTP API 请求；**WebSocket 升级豁免**（浏览器 WebSocket 构造器无法省略站点已设置的 cookie）。
- 二进制 HTTP 体精确 `Content-Type: application/octet-stream`。
- 无真实凭据的请求（含原请求体和随机 bearer）走公共处理器；真实令牌配错方法/畸形头/缺会话状态→本地不可缓存 404，体仍受载波读期限约束。**create 体是单个 HELLO 帧，上限 64 字节**。

**会话创建**（bootstrap 原子幂等交换，2 分钟有效）：

```text
POST /api/v1/session
Authorization: Bearer bootstrap-token
Body: 一个 HELLO 帧

200 OK
X-Session-Token: session-token
X-Down-Cursor: 0
X-Carrier-Mode: https | https-lanes | websocket | websocket-lanes
Body: 一个 WELCOME 帧
```

- bootstrap 是 **bearer capability 而非绑定源地址的令牌**：浏览器加载桥接页与创建会话之间可能切换 VPN/运营商/负载均衡/双栈出口。relay 仍把 bootstrap 记在签发地址、把会话记在首个有效创建请求的地址上（仅用于记账）。
- 认证通过后，临时会话容量/创建速率耗尽返回 `503 + Retry-After: 1`，**bootstrap 保持未消费**，字节相同请求可重试。

**令牌表示与重启行为**（HARDENING.md 引入）：

- bootstrap/会话令牌=32 字节不透明值，43 字符规范无填充 base64url。服务端布局：16 字节随机 nonce + 16 字节截断 HMAC-SHA256；MAC 输入为 `"tproxy-server-token-v1\x00" || kind || nonce`，kind：bootstrap=1、session=2；独立 32 字节签名密钥**跨重启持久**（`/etc/tproxy-server/token.key`）。
- 合法 MAC 只建立**来源证明**；会话状态、bootstrap 过期、重放、方法、体、容量检查仍逐操作授权。重启仍作废活动状态（会话不恢复）；签名的过期/错类令牌本地拒绝，绝不交给网站；迁移前的随机令牌在重启后无法证明来源（见 HARDENING 迁移节）。

**串行 HTTPS（`https`）**：

```text
POST /api/v1/up     Authorization: Bearer session-token
X-Up-Seq: 1（从1开始）  Body: 一个或多个完整帧
→ 204 No Content  X-Up-Ack: 1
```

- 一次至多一条上行在途；relay 只接受**下一序列**或**最后已提交序列的字节相同重试**；
- 下一有效批次暂放不进 DATA 队列预算、或解析上一请求期间同序列重试到达→`503 + Retry-After: 1`，序列不提交、批次零生效；桥接页遵守 Retry-After 后**字节相同重试**（503 不适用固定重试数，适用 90 秒预算）；
- 下行一次一条长轮询，**最新轮询获胜**：新轮询到达时旧轮询以 `204`+自身游标完成（连接还活着则无害，死了则无人观察）；轮询永远不会因并发被拒；游标确认上一批次，重复旧游标**逐字节重放**未确认批次；上行 POST 与下行轮询并发执行；2 MiB 批次下连续繁忙方向的应用层天花板 ≈ `2 MiB / 载波RTT`。

**HTTPS lanes（`https-lanes`）**：

- 端点与重试规则同上，每条共享流一条独立 lane，`/up`、`/down` 都加 `X-Lane-ID: <十进制流id>`；
- lane 0 保留给会话级 PONG；非零 lane 上每个帧的 `stream_id` 必须等于 `X-Lane-ID`；新 lane 必须以 `OPEN` 开始；每 lane 独立 `X-Up-Seq`/`X-Up-Ack`/`X-Down-Cursor`/字节相同重试状态/每方向至多一条活动请求——两个 MTProto 会话可同时用上行序列 1 且独立推进；
- 流关闭且队列排空确认后，relay 返回空响应 + `X-Lane-Closed: 1`，桥接页停止轮询该 lane；近期关闭的 lane 状态随流墓碑保留（丢失的最终上行响应仍可幂等确认）；墓碑被逐出时释放 lane 剩余的未投递帧与字节/项记账，格式良好的迟到 `DATA`/`WINDOW`/`CLOSE` 被确认并忽略（而非杀死会话）；lane 轮询同样是每 lane 无 ErrConcurrent 的最新者胜语义；
- 镜像 Telegram 原生连接分配，消除载波级队头阻塞；假设公网 origin 有正常 HTTP/2（HTTP/1.1 每源连接数会限制大量并发长轮询）。

**多路复用 WebSocket（`websocket`）**：

```text
GET /api/v1/ws
Origin: https://H
Sec-WebSocket-Protocol: tproxy-v1.<session-token>
```

- relay 回显该精确子协议；**会话 bearer 走在请求头里→运营商绝不能在前端代理或 relay 上开启头日志**；
- relay 在每个空闲长轮询周期后 ping 对端，连续两个周期无消息无 pong 就关闭（静默死端不再钉住会话直到 TCP keep-alive）；
- 每条客户端二进制消息=有界完整客户端→relay 帧批次；每条 relay 二进制消息=有界完整 relay→客户端帧批次；WebSocket 有序可靠取代 HTTP 序列/游标头，**共享的每流 WINDOW 协议仍是端到端背压的权威**；
- 拒绝：文本消息、同一 relay 会话的额外复用 WebSocket、超长消息、畸形帧批次、错误子协议；
- 桥接页排队+浏览器缓冲上行上限 32 MiB；relay 对临时后端写背压最多等 30 秒再关闭载波；WebSocket 断开=会话与全部逻辑流关闭（**本参考实现不做部分投递的 WebSocket 会话恢复**）；
- 单条 WebSocket 足够正确并消除 HTTP 停等天花板，但不保留 Telegram 原生为 API/上传/下载分开 TCP 连接的队列与故障隔离。

**WebSocket lanes（`websocket-lanes`）**：

```text
GET /api/v1/ws
Sec-WebSocket-Protocol: tproxy-lane-v1.<session-token>.<十进制流id>
```

- 流 id 规范十进制、永不为零、父会话内不可复用；一个流 id 只能挂一个 socket；首条二进制消息必须以 `OPEN` 开始，该 socket 上收发的每个帧都是选定流 id；**没有 lane-0 WebSocket**（HTTPS bootstrap 承载 HELLO/WELCOME，WS 协议 ping/pong 提供活性）；
- 每 socket 独立有序投递、浏览器缓冲、relay 写入器、后端连接；意外关闭已建立的 lane 只关那一条后端流，桥接页向 App 回 `CLOSE` 帧，其余 lane 与父会话保持；客户端/后端 `CLOSE` 完成 lane 关停后关 socket；**新 lane 建立失败视为父载波故障**（通常意味着会话/origin/网络不可用）；删除或过期父会话仍关闭全部 lane；文本/超长/畸形/跨 lane 客户端消息只关受影响的已建立 lane（relay 端），桥接页把无效 relay 消息视为父载波故障；保留 32 MiB 全局上行界并另加每 lane 8 MiB + 1024 排队项；
- 消除交互 API 与批量媒体流之间的应用层队头阻塞；当 WebView 给多条 WS 分配独立网络连接时还能隔离 TCP 拥塞与丢包（不保证）；活跃 lane socket 数受 profile 与会话流限约束；代价是更多 WS/TLS 握手、连接与服务器资源。

**会话删除**：`DELETE /api/v1/session`（会话 bearer）关闭全部流且幂等；缺失/随机凭据走公网站点；真实但不可用的凭据收到本地不可缓存 404。

### 7.6 共享帧格式（v1 全部 9 种）

```text
type:u8 | stream_id:u24 | payload_length:u32 | payload
```

| 值 | 名 | 方向 | 流 | 载荷 |
|---:|---|---|---:|---|
| `0x01` | `OPEN` | 客户端→relay | 非零 | 空 |
| `0x02` | `DATA` | 双向 | 非零 | 不透明、非空 |
| `0x03` | `CLOSE` | 双向 | 非零 | 空 |
| `0x04` | `WINDOW` | 双向 | 非零 | 非零 u32 增量 |
| `0x05` | `PING` | relay→客户端 | 零 | 不透明回显令牌 |
| `0x06` | `PONG` | 客户端→relay | 零 | 精确回显令牌 |
| `0x10` | `HELLO` | 客户端→relay | 零 | 字节 `01` |
| `0x11` | `WELCOME` | relay→客户端 | 零 | 空 |
| `0x1f` | `BYE` | relay→客户端 | 零 | 可选有界原因 |

`AUTH_CHAL`/`AUTH_RESP` 保留且 v1 客户端不支持。v1 实现不发出共享帧 PING/BYE；父载波故障直接关会话由桥接页通知客户端换新。

### 7.7 客户端流生命周期（规范性 6 步）

1. 边界认证后客户端发一个 `HELLO`；收到 `WELCOME` 才能建流；
2. 每个 App 打开的 MTProxy TCP 连接=一个新的、永不复用的非零流 id + 一个 `OPEN`；
3. 已变换字节成为该 id 的一个或多个 `DATA`；**只在 relay 授予的窗口内发送**；
4. relay `DATA` 写入对应本地连接；本地网络引擎排空这些字节后，客户端按排空量返回 `WINDOW` 信用；
5. 任一侧 EOF/失败→该流 `CLOSE`；其他流与共享载波继续；**`CLOSE` 是中止不是半关闭**（该流两侧仍排队的 DATA 丢弃——与桌面客户端现有 TCP 路径一致，后者也从不半关闭 MTProxy socket）；
6. 替换/禁用 WEB 代理→关闭载波会话及其全部逻辑流。

桥接页只批处理完整帧、只解析共享头与帧边界（选 lane 时），**从不解释 DATA、从不构造 MTProxy 载荷、从不分配流 id**。

### 7.8 帧与流量数值界（全量）

- 最大帧载荷 **1 MiB**；relay DATA 块 ≤ **64 KiB**；每流每方向初始信用 **4 MiB**；
- 客户端 DATA 消耗 relay 接收信用；**relay 只在字节真正抵达本地 MTProxy TCP socket 后才发 WINDOW**；后端读消耗客户端授予的信用、归零即停读；
- 一个载波体至多 **4096 个完整帧**；默认载波体目标 **2 MiB**（可调到 HTTP 体上限）；
- 排队记账=编码字节 + **每项保守 256 字节**成本；即使相邻写不能合并，独立的项数限额仍具权威性；帧移入唯一可重放下行批次时保留其字节与项记账，直到下一游标确认该批次；
- 下行 DATA 准入为**一个最大上行批次**+WINDOW/CLOSE/会话控制帧的保留字节与项数余量留出空间；同一流的 WINDOW 授予可跨其它流控制帧交错合并；下行 DATA 分区满时后端读暂停、确认释放容量后恢复；
- `OPEN` 只拨号 profile 配置的**数字回环后端**，客户端无法选择目的地；流 id 会话内不可复用；至多 **4096** 个近期关闭 id 作为墓碑（跨方向关闭竞态的良构迟到帧被忽略）；超限的 `OPEN` 收到该流的 `CLOSE`，**会话与其它流不受影响**。

---

## 8. Go 服务端源码逐文件精读

### 8.1 cmd/tproxy-server/main.go（95 行，入口）

- 旗标：`-config`（默认 `config.json`）、`-profiles-file`（覆盖 profile 路径）、`-check`（校验配置后退出）。
- 流程：`config.Load` → 读取环境 `TPROXY_LEGACY_TOKEN_DRAIN == "1"` 写入 `value.LegacyTokenDrain` → `session.ValidateBudget` → `appserver.New`（初始化 relay）→ `-check` 模式打印 "configuration is valid" 后 `Shutdown()` 返回。
- 监听：公共口 `net.Listen(value.Listen)` 与管理口，任一失败关闭另一个并 fatal。
- **ReadTimeout 的精心计算**（含关键注释）：`readTimeout = max(60s, 2×long_poll)`，注释原文说明 ReadTimeout 覆盖含慢体的整个请求，必须显著高于 long_poll，否则服务器的后台读会取消每一个停泊中的长轮询 context。
- 公共 `http.Server`：`ReadHeaderTimeout`（10s 默认）、`ReadTimeout`（上述）、`IdleTimeout`（75s）、`MaxHeaderBytes`（16 KiB）；管理口更紧：5s/30s/4096。
- 两 goroutine 分别 `Serve`，错误进 channel；`signal.Notify(SIGINT, SIGTERM)`；收到信号或 listener 错误后按 `Shutdown` 超时（默认 15s）优雅停两个 server，再 `application.Shutdown()`（关全部会话与后端）。
- 日志风格：`event=started public=%s admin=%s profiles=%d legacy_token_drain=%t` / `event=shutdown signal=%s` / `event=stopped` / `event=listener_failed class=http`——**只记事件类，不记任何地址、路径、凭据**。

### 8.2 internal/frame/frame.go（147 行，编解码）

常量（全项目线缆常量的唯一权威来源）：

```go
HeaderSize = 8; MaxPayload = 1 MiB; InitialStreamWindow = 4 MiB
DataChunk = 64 KiB; MaxStreamID = 0xFFFFFF; MaxBatchFrames = 4096
```

帧类型：`Open=0x01, Data=0x02, Close=0x03, Window=0x04, Ping=0x05, Pong=0x06, Hello=0x10, Welcome=0x11, Bye=0x1F`。

- `Encode`：stream id 超 24 位直接 **panic**（程序员错误）；构造 8 字节头+载荷。
- `ParseAll(input, maxPayload)`：批量解析；先钳制 `maxPayload ∈ (0, MaxPayload]`；循环内检查：批次至多 4096 帧（`ErrPayload` 类错误 "too many frames"）、不足 8 字节头→`ErrIncomplete`、length > maxPayload→`ErrPayload`、整帧超出输入→`ErrIncomplete`；**载荷切片直接引用输入内存（零拷贝）**；空批次是错误（"empty frame batch"）。
- `ParseHello`：必须是**唯一**帧且 Type=Hello、stream=0、载荷恰 1 字节 `0x01`。
- `WindowAmount/WindowPayload`：载荷必须恰 4 字节且增量非零。
- `ValidateClientShape`（客户端→relay 方向校验）：流 0 只允许 `Pong` 且载荷 ≤64 字节（回显令牌上限）；流非零时 `Open`/`Close` 载荷必须为空、`Data` 载荷非空、`Window` 必须通过 4 字节非零校验；其余类型一律 "invalid client-to-relay"。

### 8.3 internal/config/config.go（576 行）

- `capabilityContext = "tdesktop-web-proxy-bridge-v1\n"`（冻结标签）；`MaxCarrierBatchBytes = 2 MiB`（注释：桌面客户端浏览器回退回环 WebSocket 拒绝 >2 MiB 消息，更大批次会杀死该载波——**上限的真实原因**）。
- `CarrierMode` 四值 + `Valid()` + `WithDefault()`（空串回落 `https`）。
- `Duration` 自定义 JSON 反序列化（字符串如 "5s"，拒绝数字）；`Value()` 转回 `time.Duration`。
- `Limits` 结构 23 个字段（与 config.example.json 一一对应）；`Timeouts` 7 个字段。
- `ProfileLimits` 9 个字段；`WithDefaults(global)`：0 值继承全局，其中 `MaxBackendDialsInFlight` 取 `min(全局, profile.MaxStreams)`、`MaxStreamsPerSession` 取 `min(全局, MaxStreams)` ——**继承即钳制**，保证 profile 永不高于全局。
- `Defaults()`：全部默认值（见第 12 节全表）；`StaticRoutes` 默认 `"legacy"`（兼容旧部署），`TokenKeyFile` 默认 `"token.key"`。
- `Load(path, profilesOverride...)`：读文件→`DisallowUnknownFields` 严格解码→再解码一次要求 `io.EOF`（拒绝尾随数据）→相对路径（public_dir/profiles_file/token_key_file）按配置文件所在目录解析→override 至多一个→`validate()`→`loadProfiles(...)`。
- `validate()` 全部规则（逐条）：
  - `static_routes ∈ {exact, legacy}`；
  - `public_hostname` 通过 `ValidateHostname` 且已小写；
  - `listen`/`admin_listen` 必须数字回环+端口 1-65535，且两者不同；
  - **恰好一个** `public_dir` 或 `public_upstream`（同空同满都拒绝）；
  - `public_dir` 必须存在、是目录、含 `index.html`；`public_upstream` 通过 `validatePublicUpstream`（scheme=http、无 user/path/query/fragment、数字回环 host）；
  - `profiles_file` 必填；
  - HTTP/帧界：`max_header_bytes ≥ 4096`、`max_body_bytes ≥ 1024`、`0 < max_frame_payload ≤ 1 MiB`、`256 KiB ≤ carrier_batch_bytes ≤ max_body_bytes` 且 **≤ 2 MiB 硬顶**；
  - 全部资源限必须为正（列出的 16 个）；per-IP 两个限不许为负；
  - 全局 ≥ 每会话/每 IP 系（6 条包含关系校验）；
  - 全部超时必须为正。
- `ValidateHostname`：≤253、无尾点、无 `:/@?#[]`、不是 IP、含点（拒绝单标签）、全 ASCII（`>127` 拒绝→必须 IDNA A 标签）、每标签 1-63、不以 `-` 开头结尾、字符集 `[a-z0-9-]`。
- `DeriveCapability(host, secret)`：`HMAC-SHA256(key=secret, msg=context+host)`；`CapabilityString`：RawURLEncoding。
- `DecodeSecret`：先 TrimSpace；长度 32/34 走 hex；否则尝试 RawURLEncoding 再 URLEncoding（带填充）；结果必须 16 或 17 字节；17 字节必须 `dd` 前缀（`ee` 也被 test 验证为合法 16 字节随机 secret——`TestPlainSecretMayBeginWithEE`）。
- `loadProfiles`：stat 检查权限——`Perm()&0077` 必须为 0，**除非**是 systemd 凭据目录（`CREDENTIALS_DIRECTORY` 下的文件允许 root 0400）；读入后 `defer` **逐字节清零 secret 缓冲**；严格 JSON（禁未知字段+禁尾随）；1..MaxProfiles 个；名字 1-64 字符且唯一；backend 数字回环；carrier_mode 四选一；profile limits 不超全局（9 项区间校验+解析后的两条自洽校验）；secret 解码→派生 capability→**再次清零 secret 字节**→capability 唯一（重复拒绝）。
- `isSystemdCredential`：`CREDENTIALS_DIRECTORY` 环境变量存在且为绝对路径，且 profile 文件位于该目录。

### 8.4 internal/config/token_key.go（30 行）

`ReadTokenKey`：打开→必须是**普通文件**且**恰 32 字节**→`Perm()&0077==0`（组/其它不可访问）→`io.ReadFull`。错误文案直接指导运维："provision a persistent 32-byte key before starting the relay"。**缺失或过宽直接启动失败，绝不静默生成临时密钥**（防止重启后令牌来源证明全部丢失）。

### 8.5 internal/session/token.go（55 行)

- `TokenClass`：`External(0)/Bootstrap(1)/Session(2)`。
- `tokenMAC`：`HMAC-SHA256(key=tokenKey, msg="tproxy-server-token-v1\0" + kind + nonce)` 截取前 16 字节。
- `newToken`：crypto/rand 填 16 字节 nonce→拼 MAC→返回（base64url 原文, sha256(原文)）。**Map 键永远是 sha256(token)**，原文不进 map。
- `ClassifyToken`（防探测扫描用）：**宽松解码**（非规范拼写也本地包含）；两个 `subtle.ConstantTimeCompare` 分别比对 bootstrap/session MAC。注释明确：分类只证明来源，不证明活性或使用权；宽松解码让非规范拼写也被本地拦截，而 `tokenHash` 在授权操作时仍要求规范编码。

### 8.6 internal/session/manager.go（650 行）

错误集：`ErrAuthentication/ErrBackpressure/ErrLimit/ErrProtocol/ErrConcurrent/ErrClosed`。

**bootstrap 结构**：`{expires, profile, issuanceIP, bodyDigest[32], sessionToken, session, used}`——幂等重放所需的全部状态。

**Manager 状态**：互斥锁 + bootstraps（hash→entry）、bootstrapsPerIP、sessions（hash→*Session）、closedTokens（hash→过期时刻，幂等 DELETE 的墓碑，容量 `MaxSessionsGlobal*16`，逐出最旧）、sessionsPerIP/Profile、streamsPerProfile、dialsPerProfile、三个全局令牌桶（bootstrap/session/stream rate）+ 每 profile 两个桶、streamsLive、backendDialsInFlight、pendingGlobalCost/Items、closed/stop/done、8 个原子计数器。

核心方法逐个：

- `MatchCapability(value)`：长度先验 32 字节→**全 profile 常数时间扫描**（`subtle.ConstantTimeCompare`，全部比较完才返回，不短路——抗时序侧信道）。
- `IssueBootstrap(profile, clientIP)`：先 `MatchCapability` 重验 profile 归属→`newToken(Bootstrap)`→锁内：过期回收→（per-IP 上限，默认 0=禁用）或全局 bootstrap 令牌桶 `NewBootstrapsPerMinute/Burst` 不过→`limitHits++` 返回 `ErrLimit`；全局条目满时尝试**逐出最旧未用** bootstrap，逐不出才 ErrLimit→登记 entry（2 分钟过期）→ per-IP 计数 +1。
- `Create(token, clientIP, body)`：**锁外先 `frame.ParseHello`**（体已由 server 层限到 64 字节）→ `tokenHash`（规范编码校验）→ bodyDigest → 锁内查 bootstrap：不存在/过期→ErrAuthentication；**已用**：bodyDigest 常数时间比对一致且 session 仍在→返回同一 sessionToken+WELCOME（幂等）；不一致→ErrAuthentication。未用：容量检查（closed、全局会话数、profile 会话数、per-IP）→ErrLimit（**503+Retry-After 语义，bootstrap 未消费可重试**）；全局+profile 双令牌桶；`newToken(Session)`；构造 `newSession`（注入回调：`budget=changePendingBudget`、`onFinished=sessionFinished`、`acquireStream`、`onBackendDialFinished`、`onStreamFinished`、`onStreamRejected`、`onUp/onDown` 计数）；把 profile 的 `MaxStreamsPerSession/MaxPendingPerSession` 覆盖进会话 limits；登记 sessions/PerIP/PerProfile；`releaseUnusedBootstrapLocked`（把未用 bootstrap 的 per-IP 记账退回）；entry 标记 used、存 digest/token/session；`sessionsCreated++`。
- `HasBootstrap`：存在且未过期。
- `Get(token)`：hash→session 查找。
- `CloseToken(token)`：会话存在→`Close()`；不存在但在 closedTokens 墓碑内→`nil`（幂等 DELETE）；否则 ErrAuthentication。
- `Metrics()/Capacity()`：原子/锁内快照。
- `Shutdown`：closed 标记→close(stop)→复制会话列表→逐个 `Close()`→逐个 `wait()`（等后端 goroutine 排空）→`<-done` 等清理循环退出；幂等（已 closed 则只等 done）。
- `sessionFinished(value)`（会话结束回调）：锁内遍历 sessions 找到指针→删除；closedTokens 满 `MaxSessionsGlobal*16` 时逐出最旧，登记 `now+BootstrapLifetime`；PerIP/PerProfile 递减清零删除；遍历 bootstraps 把 session 指向该会话的未消费条目一并删除；锁外 `sessionsClosed++`。
- `acquireStream(profile)`：全局（closed、streamsLive≥MaxStreamsGlobal、backendDialsInFlight≥MaxBackendDialsInFlight）+ profile（streams、dials）+ 双速率桶（全局 streamRate + profileStreamRates）任一不过→false；全过则四个计数 +1 并 `streamsOpened++`。
- `backendDialFinished(profile, failed)`：拨号计数递减（若为 0 panic "invalid backend dial accounting"——记账不变式自检）；failed→`backendDialFailures++`。
- `streamFinished(profile)`：streamsLive/profile streams 递减（不变式 panic 同上）。
- `changePendingBudget(costDelta, itemDelta, class)`：增支时——非控制类要先扣**控制保留**（`pendingControlReserve × MaxSessionsGlobal`，除法防溢出后减法；保留过大直接把限额钳到 0）；两项检查（cost>limit、超余量）任一失败→`limitHits++` 返回 false；通过则累加，负值 panic（不变式）。
- 令牌桶 `takeRate`：`tokens += Δt×(perMinute/60)`，钳到 burst；`tokens<1` 拒绝；扣 1。`allowProfileRateLocked`：全局与 profile 两桶**都要过才扣**（避免单桶通过就消耗另一桶）。
- `cleanupLoop`：30 秒 ticker；过期 bootstrap/closedTokens 回收；对每个会话 `now-lastActivity > reconnect_grace` 则 `Close()`；stop→close(done)。
- `tokenHash(token)`：RawURLEncoding 解码 32 字节 + **再编码必须等于原文**（拒绝非规范拼写）→ sha256。

**设计解读**：Manager 把"准入控制"（谁能建会话/流）与 Session（流内状态机）彻底分层；所有跨会话记账（全局/每 profile/每 IP）集中在一把锁内、操作 O(1) 或 O(小)；记账错误用 panic 暴露而不是静默漂移。

### 8.7 internal/session/session.go（1593 行，核心状态机）

#### 常量与类型

```go
queueItemCost = 256              // 每排队项保守成本
controlReserveExtraItems = 16    // 控制保留额外项
controlReserveItemsPerStream = 3 // 每流控制项（WINDOW/CLOSE等）
pendingClass: uplink / downlink / control
```

`streamState`（每逻辑流）：`backend`、`receiveWindow u32`（relay 收信用）、`sendCredit u64`（relay 发信用，封顶 MaxUint32）、`pendingWriteBytes/Cost/Items`（待写后端的记账）、`creditNotify`（信用唤醒 chan,1）、`writes [][]byte`（待写缓冲）、`writeNotify`（写唤醒 chan,1）。

`queuedFrame`：`{encoded, typeCode, streamID, cost}`——下行队列条目。
`downBatch`：`{body, cost, items}`——一次可重放下行批次。

`carrierLane`（lane 模式的每 lane 状态）：独立 `lastUpSequence/lastUpDigest/upActive/websocketActive/pendingFrames/pendingWindows/unacked 系/downCursor/downActive/superseded/notify`——**每 lane 一套完整 https 会话语义**。

`Session`：profile/clientIP/limits/timeouts/budget 回调 + 载波模式 + 大互斥锁保护的全部状态（streams、closedStreams+closedOrder 环形墓碑+closedStart、pendingFrames/pendingWindows、pending 记账、unacked 批次、downCursor、lastUpSequence/Digest、upActive/downActive、superseded、websocketActive、closed、lastActivity、notify/budgetNotify/done、carrierLanes、finishOnce、backendWG、8 个回调）。

`newSession`：lane 模式（https-lanes）预置 lane 0（会话级 PONG 用）；websocket-lanes 的 lane 按需建。

#### 关键公开方法（含锁外/锁内边界）

**`ProcessUp(sequence, body)`（https 模式上行）**——精读：

1. lane 模式调用→ErrProtocol；计算 `sha256(body)` 作幂等摘要；
2. 锁内：closed→ErrClosed；`sequence == lastUpSequence && !=0`：**字节相同重复**→比对摘要，一致→直接返回 ack（幂等重放）；不一致→protocolFailure（摘要不同=协议错）；
3. `sequence != lastUpSequence+1 || ==0`→protocolFailure（间隙/重排/归零）；
4. `upActive`（已有一条上行在解析）→**ErrConcurrent（可重试的 503）**；置位后**解锁**；
5. 锁外 `ParseAll + ValidateClientShape`（每帧形状）；
6. 重新锁内：复位 upActive；closed→ErrClosed；`validateBatchLocked`（模拟窗口运算的全批次合法性）失败→protocolFailure；
7. `backendWriteReservationLocked`：先按帧流演算这批 DATA 的写入记账需求（含 256/项），`reservePendingLocked(..., pendingUplink)` 预占——失败→**ErrBackpressure（503，零生效，序列不提交）**；
8. `applyBatchLocked`（真正落状态）；未用掉的预占退回；
9. `backendWG.Add(len(opened))`；提交 `lastUpSequence/Digest`；解锁；
10. 锁外：closed 流的 backend 关闭；opened 的每流 `go runBackend`；`!applied`（内部限额关流导致整体失败）→`Close()`；`onUp(len(body))` 计数。

`validateBatchLocked` 的**模拟器式校验**值得展开：复制当前全部流的 `{receiveWindow, sendCredit}` 快照，逐帧推演——流 0 只许 Pong；OPEN 对已存在/已关闭 id 拒绝并把快照初始化为 4 MiB/4 MiB；DATA 对已关闭 id **跳过**（墓碑语义），超窗拒绝并扣减模拟窗；WINDOW 累加 sendCredit（封顶 MaxUint32）；CLOSE 对已关闭跳过、对不存在拒绝、否则从快照删除并标记 closed。**整批要么全合法要么拒绝——不存在半应用**。

`applyBatchLocked`（只在 validate 通过后调用）：OPEN 时如果 `len(streams) ≥ MaxStreamsPerSession` 或 `acquireStream()`（全局/profile 限额）失败→**只 rememberClosed+queueFrame(CLOSE) 该流**（其余不受影响，`onStreamRejected` 计数）；成功则 `newBackendStream` 建流、初始化 4 MiB 双向信用、登记 streams、列入 opened；DATA：`appendBackendWriteLocked`（**64 KiB 内与上一缓冲合并**以省记账：合并时 cost=纯字节、items=0）+ 扣 receiveWindow + `signal(writeNotify)`；WINDOW：sendCredit 封顶累加 + `signal(creditNotify)`；CLOSE：`releaseStreamWritesLocked`（退记账）、删流、墓碑、backend 列入 closed。

**`Poll(ctx, cursor)`（https 下行长轮询）**——精读：

1. 25 秒（`timeouts.LongPoll`）deadline 定时器；
2. 锁内：closed→ErrClosed；有未确认批次：`cursor == unackedBase`→**原样重放**（返回 body+当前 downCursor）；`cursor != downCursor`→protocolFailure；否则（cursor==downCursor）释放未确认批次的记账并清空；
3. 无未确认：`cursor != downCursor`→protocolFailure；
4. **最新轮询获胜**：`downActive && superseded != nil`→`close(superseded)`（把旧轮询踢出停泊，旧轮询将以 `204+自身游标` 返回）；登记自己的 superseded 通道并 downActive=true；
5. 循环（锁内检查→锁外等待）：
   - 不是自己持有→返回空（被取代）；
   - `pendingFrames` 非空→`takeDownBatchLocked`（打包到 `CarrierBatchBytes`/4096 帧，**先到先得不做优先级**，但控制保留保证 WINDOW/CLOSE 有路走）→downCursor++→记 unacked（base=调用者游标）→返回 200 body；
   - closed→ErrClosed；
   - 锁外 select：`ctx.Done()`（客户端断开，若仍持有则复位）/`mine`（被取代，唤醒 notify 再返回空）/`deadline.C`（25 秒到：若不是自己已被取代则刷新 lastActivity 返回空 204；有 pending 则回环再取）/`s.notify`（新帧到达，若不属于自己则转发唤醒 `signal(s.notify)`）。

`PollLane`（lane 版）：同语义但针对 `carrierLanes[laneID]`；额外两条出口：非零 lane 的流已死且在墓碑中→返回 `laneClosed=true`（**`X-Lane-Closed: 1`**）；lane 对象已被逐出/替换→同样 laneClosed；还监听 `s.done`（父会话关闭）。

**WebSocket lane 生命周期方法**：

- `AcquireWebSocket()`：仅 websocket 模式、未关闭、未占用→占用。
- `AcquireWebSocketLane(laneID)`：仅 websocket-lanes；laneID 非 0、≤MaxStreamID、不在墓碑；lane 不存在则受 `MaxStreamsPerSession` 约束新建；未占用→占用并刷新 lastActivity。
- `ReleaseWebSocketLane(laneID)`：释放占用；若该 id 仍有活流→释放写记账、删流、墓碑、取 backend；`releaseLaneLocked`（归还 lane 的 pending/unacked 记账、清队列、唤醒停泊轮询）；删 lane；锁外关 backend。
- `laneProtocolFailure(laneID)`：websocket-lanes→**只释放该 lane**（其余会话不受影响）；https-lanes→整个会话 protocolFailure。

**后端记账回调**（backendStream 回调进 Session）：

- `backendDrained(id, amount)`：写循环每写 N 字节调用；校验 N ≤ pendingWriteBytes/Cost、`receiveWindow+N ≤ InitialStreamWindow`（防溢出回卷）；退 pendingWrite 记账→`releasePendingLocked(amount, 0)`→**receiveWindow += N**→`queueFrameLocked(Window, id, N)`（**字节真正写进 TCP 才授信用**）。任何校验失败返回 false（写循环退出）。
- `backendWriteFinished(id)`：一个排队写项完成→items−1、cost−256、全局退 256/1。
- `backendData(id, data)`：读循环读到数据→sendCredit 扣减→`queueFrameLocked(Data, ...)`。
- `backendClosed(id, backend)`：后端 EOF/错误→删流、墓碑、给客户端排一个 CLOSE 帧；`queueFrame` 失败（全局预算耗尽）→只能关整个会话；锁外 backend.close()。
- `nextWrite(id, done)`：写循环取数——锁内有则弹出队头返回，无则锁外等 `writeNotify/s.done/done`。
- `nextReadAllowance(id, done)`：读循环取额度——`min(sendCredit, DataChunk)` 再经 `dataFrameAllowanceLocked`（下行预算内）钳制；无额度则锁外等 `creditNotify/budgetNotify/s.done/done`。**这就是"不用 io.Copy"的流控闸门**。

**下行入队 `queueFrameLocked`**（https 版；lane 版 `queueLaneFrameLocked` 同构）：

- WINDOW 合并：`pendingWindows[id]` 命中→就地加总（≤MaxUint32 才合并，防回卷）→signal；
- DATA 尾部合并：与上一帧同流同 DATA 且总长 ≤ MaxFramePayload→追加并**改写帧头 length 字段**（字节记账纯增量）；
- 新帧：`cost = len(encoded)+256`；DATA 记 downlink 类、其余 control 类；`reservePendingLocked` 失败→false（调用方决定降级路径）；
- **control 类使用 pendingControl 保留区**（见 `reservePendingLocked`），保证 WINDOW/CLOSE 在 DATA 挤满时仍能入队。

**预算三函数**：

- `pendingControlReserve(limits)`：`items = 16 + MaxStreamsPerSession×3`，`cost = items×(256+8+4)`；溢出时直接返回每会话上限（保守）。
- `pendingUplinkReserve(limits)`：为一个最大上行批次留：`MaxBodyBytes + min(MaxBodyBytes/8, 4096)×256` 字节与项。
- `reservePendingLocked`：按类选限额（uplink=会话限−控制保留；downlink=再减上行保留；control=会话限原值）；两段检查+全局 `budget` 回调；`pendingCost/Items` 累加。
- `ValidateBudget`（main 启动时调用）：控制保留×MaxSessionsGlobal 不得吃光全局预算——"静默饿死每个 DATA 帧的哑弹配置"直接拒绝启动。

**批次打包 `takeDownBatchLocked`**：按序取帧直到 `CarrierBatchBytes` 或 4096 帧上限（首帧必取）；被取走的 WINDOW 清理 pendingWindows 索引；**剩余 pendingWindows 索引整体平移**（实现细节：索引 map 减 count）；队尾清 nil 释放引用。

**墓碑环 `rememberClosedLocked`**：`closedStreams` 集合 + `closedOrder` 环形队列（容量 MaxClosedStreamIDs=4096）；满了逐出最老的——若被逐出的 id 还有 carrierLane（websocket-lanes 模式），**连 lane 一起释放记账并删除**，再唤醒该 lane 停泊轮询（让它观察逐出而不是傻等 25 秒）。

**`closeLocked`**：幂等（closed 标记）；`close(done)` 广播；清空全部 lane 队列/记账并唤醒；逐流关 backend；全量退还全局预算；清 pending/unacked/墓碑引用；signal(notify)；`finishOnce` 触发 `onFinished`（goroutine 异步回调 Manager 注销）。

**`backendStream`**（每后端流）：

- `run()`：`net.Dialer{Timeout: BackendDial}` 拨 profile.Backend（**仅此一处、仅配置值，客户端无法影响**）；拨号结果回调 `backendDialFinished(err!=nil && ctx未取消)`；失败 return（defer 里 `backendClosed` 会给客户端排 CLOSE）；成功则启动写泵 goroutine（`writeLoop`+`close`），主协程跑 `readLoop`+`close`，等写泵退出。
- `writeLoop`：`nextWrite` 取一块→`conn.Write` 循环（部分写继续）→每写 N 字节 `backendDrained`（授 WINDOW）→写完一块 `backendWriteFinished`（退项记账）。任何失败 return。
- `readLoop`：`nextReadAllowance` 得额度→`conn.Read(buffer[:allowance])`→`backendData` 入下行队列；EOF/错误 return。
- `close()`：`sync.Once`：cancel ctx + 关 conn。
- 测试钩子 `SetUpActiveForTest`（唯一导出的测试专用方法）。

### 8.8 internal/server/server.go（763 行）

常量：`webSocketProtocolPrefix="tproxy-v1."`、`webSocketLaneProtocolPrefix="tproxy-lane-v1."`；`maxCreateBodyBytes=64`（注释：create 体就是 8+1 字节的 HELLO，小上限让未认证 POST /session 流不了兆级再被拒）；`bodyReadDeadline=30s`（注释：防止请求体在 handler 读或 net/http 弃读时把 goroutine 和 Caddy→relay 连接无限挂住；长轮询不带体；公共请求用普通网关超时）。

**`New`**：`ReadTokenKey`→`ValidateBudget`→二选一公共源：`public_dir`→`loadStaticSite`；`public_upstream`→`httputil.ReverseProxy`（**DisableCompression=true**、Rewrite 保留 Host/RawQuery/Trailer、复制 Forwarded/XFF/XFH/XFP 头、ErrorHandler 返回 502 文本）→`session.NewManager`。

**路由总入口 `serveHTTP`**（防探测二分的核心）：

1. `hasInternalSecret(r)`（secrets.go 的全元数据扫描）为假→**完全走公共处理器**（静态或反代，方法/头/体/cookie 原样）；
2. 为真（请求元数据中出现真实 capability/签名令牌）→`Cache-Control: no-store`；有体则 `setReadDeadline`（ResponseController 30 秒）；
3. Host 必须等于 `public_hostname`（或 `:443` 后缀形式）→否则本地 404；
4. `isTransportPath`（四个 `/api/v1/*`）且 `EscapedPath()==Path`（拒绝 `%2f` 类逃逸）→`serveAPI`；
5. `bridgeProfile(r)`（精确 `GET /?bridge=<43字符>`）→`serveBridge`；
6. 其余→本地 404。

**`serveBridge`**：clientIP→`IssueBootstrap`（限额失败也是 404——**不给探测者区分信号**）→`bridge.Render(hostname, token, carrierMode, carrierBatchBytes)`→设置 `Content-Type: text/html`、CSP、no-store、no-referrer、nosniff、X-DNS-Prefetch-Control: off、Permissions-Policy（23 项拒绝）→写 body。

**`serveAPI`** 前置拒绝（全部本地 404）：任何 query（`RawQuery != "" || ForceQuery`）、重复 `Authorization`（>1 值）、重复 `Sec-WebSocket-Protocol`、带 Cookie 的非 `/api/v1/ws` 请求（**ws 升级豁免 cookie**——浏览器 WebSocket 无法省略 cookie）。→clientIP→`/api/v1/ws` 分流→`bearerToken` 解析→路径分发 session/up/down。

**`serveSession`**：
- DELETE：方法+**空体+无 Content-Type**→`CloseToken`（幂等）→204；任何不符→404；
- POST：必须 octet-stream（mime 解析后无参数）→**`HasBootstrap` 先于读体**（认证在消费体之前——测试 `TestSessionCreateAuthenticatesBeforeReadingBody` 验证）→`readBody(64)`→`Create`：`ErrLimit`→**503+Retry-After: 1**（bootstrap 未消费，字节相同重试安全）；其它错→404；成功→200 + `X-Session-Token`/`X-Carrier-Mode`/`X-Down-Cursor: 0` + WELCOME 体。

**`serveUp`**：POST+octet-stream→`X-Up-Seq` 规范十进制（无前导零、无加号、可往返）非零→`Get` 会话→读体（`MaxBodyBytes` 上限）→按会话载波模式分派：https 拒绝带 `X-Lane-ID` 的请求（反之亦然，lane 必须 `X-Lane-ID` 规范且 ≤0xFFFFFF）→`ProcessUp/ProcessUpLane`；`ErrBackpressure || ErrConcurrent`→503+Retry-After: 1；成功→204+`X-Up-Ack`。

**`serveDown`**：POST+**无 Content-Type**→`X-Down-Cursor` 规范→`Get`→**空体**→分派 `Poll/PollLane`；ctx 取消直接 return（连接已断，不写）；`ErrConcurrent`→503；成功→`X-Down-Cursor`（+lane 模式的 `X-Lane-Closed: 1`）→空体 204 或 200 octet-stream。

**`serveWebSocket`**：必须 GET 且**无 Authorization 头**（凭据在子协议里）→子协议列表恰 1 个→`webSocketCredentials` 解析（前缀切分+Cut+canonicalUint；lane 0/前导零/超 24 位/多段全拒）→token 过 `bearerToken` 语法关→`Get` 会话且载波模式匹配（lanes↔websocket-lanes，否则 404）→空体→`AcquireWebSocket(Lane)`（失败 404）→`websocket.Upgrader{64KiB 读写缓冲, 回显子协议, CheckOrigin 恒真}`（Origin 不是认证——文档明确）→升级失败按模式释放；成功后 defer 释放/关闭；
- `SetReadLimit(MaxBodyBytes)`（超长消息直接断——协议规定拒绝）；
- **空闲超时 2×long_poll**：初始 ReadDeadline；PongHandler 刷新；注释原文解释：不这样做，一个永不发消息的死端只能靠监听器 TCP keep-alive 几分钟后才发现；
- 两 goroutine：`readWebSocket`（每条二进制消息：刷新读期限；非二进制/空→ErrProtocol；**ErrBackpressure 时 50ms 重试循环至 30 秒**——与桥接页 JS 的写缓冲反压呼应；其余错误终止）与 `writeWebSocket`（`Poll/PollLane`：空批次→**发协议 Ping**；有数据→二进制消息，30 秒写期限；游标推进）；
- 任意一侧 finished 即整体退出（lane 模式 defer Release，单模式 defer Close——**WebSocket 断=会话关**）。

**`clientIP`**：RemoteAddr 的 host 必须**数字回环**（注释：请求必须经 loopback 代理到达）→无 XFF→用回环地址本身；有 XFF：**恰一个 IP、无逗号、无空白**（列表或不可解析→错误→404）→返回该 IP。**用于记账而非认证**（bootstrap/会话不绑 IP）。

**`servePublic`**：有 upstream→反代（流式）；否则 GET/HEAD：`/`→index；`resolve(path, legacy)`→entry；否则 404 entry。

**管理端 `AdminHandler`**：`/healthz`（GET→"ok\n"）、`/readyz`（GET→对每个 profile backend `net.DialTimeout` 拨号测试，任一失败 503 "backend unavailable"——**公共站点不受影响**，只有本地 admin 能看到后端故障）、`/metrics`（13 个 `tproxy_*` 指标，Prometheus 文本 0.0.4 格式：live 5 个 + total 8 个）、`enable_pprof` 时挂 5 个 pprof 路由（默认关）。

**工具函数**：`readBody`（MaxBytesReader+io.ReadAll，空体也是错）；`emptyBody`（ContentLength≤0 且试读 1 字节为 0——处理 chunked 无长度的情况）；`binaryContentType`（octet-stream 且**零参数**）；`bearerToken`（`Bearer ` 前缀、恰一个空格、43 字符规范 base64url 往返校验）；`canonicalUint`（非空、非 `0` 开头（除非单字符）、无 `+`、ParseUint+FormatUint 往返）。

### 8.9 internal/server/secrets.go（111 行，防主动探测的扫描器）

**`bridgeProfile`**：方法 GET、`EscapedPath()=="/"`、`len(RawQuery)==len("bridge=")+43`、前缀 `bridge=`→**Strict 解码**（RawURLEncoding.Strict，拒绝非规范）→`MatchCapability`。多一个参数、百分号转义、大小写混写都匹配不上→公网站点。

**`hasInternalSecret`**（入口二分的判据）：

1. `LegacyTokenDrain` 且 `legacyCarrierCredential`（旧随机令牌的形态识别——见 HARDENING 迁移）→true；
2. `containsSecret(r.URL.String())` 或 `containsSecret(r.Host)`；
3. 遍历**全部头名与全部值**（含重复字段）。
注释：只解析规范的 Authorization/bridge 字段会漏掉重复头、畸形 query、cookie、referrer、错路径里的**真**机密；**体与 trailer 有意不读**（公共上传必须保持流式）。

**`containsSecret`** 的算法（精细）：

- 含 `%` 时**逐三字符 `url.PathUnescape` 解码**再拼回（注释：解码单个转义使得别处的畸形转义无法藏住一个 capability；不整体 parse/re-encode 公共 query——畸形 query 无真机密时属于应用）；
- **滑动窗口**：对解码后文本按 base64 字母表（`[A-Za-z0-9_-]`）分段，每段内每 43 字符子串做两件事：`ClassifyToken`（HMAC 双类常数时间比对）非 External→命中；`DecodeString`+`MatchCapability`（常数时间扫描 profile）→命中。
- 任何命中=内部请求→进入本地 404/正常 API 处理，**绝不到公网处理器**——这就是"真机密不出现在网站日志/响应"的保证。

`base64Byte`：手写的字母表判定（与 Go base64 的 URL 字母表一致，含 `-`、`_`）。

### 8.10 internal/server/site.go（124 行）

- `loadStaticSite`：`filepath.WalkDir` 递归；**跳过目录与非普通文件（符号链接不收）**；`Rel`+`ToSlash`；`../` 逃逸防护；读全文入内存（**启动读一次，改动需重启**）；每文件：body、`mime.TypeByExtension`（未知→octet-stream）、modTime、**ETag=sha256(body) 的 hex 加引号**；index 必须 `/index.html`，404 可选（缺省用 index 的 body 配 404 状态）。
- `resolve(path, legacy)`：首字符 `/`、`path.Clean(path)==path`（拒绝 `..`/`//`）、无反斜杠；精确命中；legacy 模式才做 `/favicon.ico→/favicon.svg` 与无扩展名→`.html` 别名；exact 模式只认精确文件名。
- `serveEntry`：200 走 `http.ServeContent`（**标准 Go 条件请求/Range/HEAD 语义+ETag+modTime**）；错误状态直接写 body 不做条件转换（错误文档保持错误状态）。

### 8.11 internal/bridge/page.go（448 行）

结构：`Page{Body, Nonce, CSP}`；`PermissionsPolicy` 常量（**23 项**全部 `=()`：accelerometer、autoplay、camera、clipboard-read/write、display-capture、encrypted-media、fullscreen、geolocation、gyroscope、hid、idle-detection、magnetometer、microphone、midi、payment、picture-in-picture、publickey-credentials-create/get、screen-wake-lock、serial、usb、web-share、xr-spatial-tracking）。

`Render(hostname, bootstrapToken, carrierMode, batchBytes)`：

1. 校验 hostname（复用 config.ValidateHostname）、`0 < batchBytes ≤ 2 MiB`、载波模式合法；
2. **18 字节** crypto/rand nonce（base64url，24 字符）；
3. 五个占位符（`__NONCE__/__ORIGIN__/__BOOTSTRAP__/__CARRIER_MODE__/__BATCH_LIMIT__`）用 `json.Marshal` 生成的安全字面量替换（**JSON 编码防注入**：bootstrap token/hostname 含特殊字符也会被正确转义）；
4. 替换后**再检查五个占位符不存在**（防模板值本身含占位符串的自指攻击）→失败报错；
5. CSP 按第 7.4 节精确拼装（connect-src 带 wss://H，frame-ancestors `http://127.0.0.1:*`，sandbox 两开关）。

`document` 常量：整个 HTML 文档（`<!doctype html>` + `<title>Connection</title>` + 单个 nonce 内联脚本）——**零外部资源、零样式、零图片**。JS 逻辑精读见第 9 节。

---
## 9. Bridge 前端 JavaScript 逐段精读

`internal/bridge/page.go` 中的 `document` 常量是一段约 370 行、IIFE 封装、`'use strict'` 的零依赖 ES2020+ 脚本。这是**载波的客户端实现**（服务端下发、版本与会话绑定）。逐段拆解：

### 9.1 注入的常量与全局状态

```javascript
const relayOrigin=__ORIGIN__, bootstrap=__BOOTSTRAP__, carrierMode=__CARRIER_MODE__;
const fragment=location.hash,
      androidNonce=/^#android=([A-Za-z0-9_-]{43})$/.exec(fragment)?.[1]||'';
history.replaceState(null,'',location.pathname);   // 立即抹掉 query+fragment
```

- 三值由服务端 JSON 注入；`androidNonce` 严格正则 43 字符（32 字节 nonce 的 base64url）；**载波还没开始跑，URL 就已"变干净"**——刷新即公网站点（一次性桥接页生命周期）。
- 状态：`initialized/closed/port/sessionToken/createStarted`；`queuedBytes/queuedItems`（上行排队记账）；`pollController/webSocket/webSocketTimer`；`pending`（建会话前缓存）/`upPending`（https/ws 上行队列）/`lanes:Map`/`closedLanes:Set`/`closedLaneOrder`。
- **客户端限额常量**：`queueLimit=32 MiB`、`queueItemLimit=16384`、`closedLaneLimit=4096`（与协议/服务端一一对应）；lane 级：`laneQueueLimit=8 MiB`、`laneItemLimit=1024`；`batchLimit=__BATCH_LIMIT__`（服务端 profile 值）。

### 9.2 工具函数层

- `status(state)`：`port.postMessage({t:'status',state})`（connecting/connected/reconnecting/failed）。
- `pause(ms)`：Promise 化 setTimeout。
- `options(method, token, body, headers, signal, keepalive)`：**统一 fetch 参数**——`mode:'same-origin', credentials:'omit', cache:'no-store', redirect:'error', referrerPolicy:'no-referrer'`（与 PROTOCOL 执行档案逐字对应）+ 按需 Authorization/Content-Type。
- `bufferedBytes()`：`https` 无、`websocket` 当前 WS 的 bufferedAmount、`websocket-lanes` 遍历全部 lane socket 求和——**浏览器缓冲计入排队预算**。
- `reserve(data, lane)/release(bytes, items, lane)`：客户端侧记账（32 MiB/16384 全局 + lane 8 MiB/1024）；`reserve` 失败→fail()（客户端不无限缓存）。
- `splitFrames(value)`：**客户端帧解析器**（lane 路由需要）：DataView 读 type/u24 id/u32 len；校验：剩余 <8 或帧数超 4096→错；`(type===2&&!size)`（空 DATA）或 `size>1048576` 或越界→错；**首帧且整批恰好一帧时零拷贝引用**，否则 slice。空批次错。
- `frameBound(value, maxFrames, maxBytes)`：只数边界不切（给 joinPending 预算用）。
- `joinPending(values, lane)`：把队列打包成一次请求体——**整项装到下一项会溢出为止；首项自身超界（4096 帧/batchLimit 字节）则在帧边界切开，余量作为新项推回队头**（注释解释为什么：relay 拒绝 >4096 帧或 >batchLimit 的体）。
- `retryAfterMs(response)`：解析 Retry-After（秒数或 HTTP 日期），钳 30 秒。

### 9.3 `request(path, makeOptions)`——统一重试引擎

```
delay=250ms 起，attempt 计数，deadline=now+90s
循环：
  fetch(relayOrigin+path, options)
  ├─ 503 → 读 Retry-After（并 arrayBuffer() 消费体），进入退避
  ├─ 其它状态 → 直接返回 response（调用方检查语义）
  └─ 异常 → closed/外部中止则抛；attempt++；attempt===9 → 抛 'carrier retry limit reached'
  退避 = wait(来自Retry-After) 或 250ms×2^n（顶 5s）+ 0~25% 抖动
  503 且超 90 秒 deadline → 抛
  每次退避前 status('reconnecting')
```

要点：**网络错误上限 9 次；503 不占 attempt 只占 90 秒预算**（与 PROTOCOL "503 不适用固定重试数，适用 90 秒预算" 逐字一致）；外部 signal（页面关闭/被取代的轮询）链入 AbortController；90 秒兜底定时器防 fetch 挂死。

### 9.4 会话建立 `createSession(first)`

- `status('connecting')`；`request('/api/v1/session', POST, bootstrap, first)`——**第一个上游缓冲原样作为创建体**（relay 验证必须是 v1 HELLO）；
- 校验 `status===200` 且 **`X-Carrier-Mode === carrierMode`**（服务端页面与 profile 必须自洽，防降级错配）；
- 取 `X-Session-Token`/`X-Down-Cursor`；`closed` 已发生→立即 `fetch(DELETE, keepalive)` 兜底；否则消费 WELCOME 体；
- `websocket` 模式先 `openWebSocket()`；`https`→`poll()`；`https-lanes`→`pollLane(ensureLane(0))`；
- **把 pending.splice(0) 里建会话前缓存的帧全部转入载波**（同时 release 客户端记账）；
- `port.postMessage(welcome,[welcome])`（**Transferable 零拷贝移交**）+ `status('connected')`。

### 9.5 `https` 载波（基线）

- `queueUp(data)`：reserve→`upPending.push`→`runUp()`；
- `runUp()`：单飞（upRunning 哨兵）循环：`joinPending` 打包→`request('/api/v1/up', {X-Up-Seq})`→校验 204 且 `X-Up-Ack===sequence`→`release`→`traffic` 事件→`upSequence++`；finally 里**若队列又有数据且未关闭则续跑**（处理与并发投递的竞态）；
- `poll()`：无限循环：`request('/api/v1/down', {X-Down-Cursor}, AbortController)`；204→继续（连接活着）；200→读体与 `X-Down-Cursor`→**先 postMessage traffic 再 postMessage(data, transfer)**→**全部成功送达客户端边界后才推进游标**（PROTOCOL：游标只在整批成功转移后推进，丢响应就会被再次请求）→`status('connected')`。

### 9.6 `https-lanes` 载波

- `ensureLane(id)`：lane 对象（sequence=1、cursor='0'、pending、bytes/items、running/polling、socket、timer、opened/localClosed/remoteClosed/finished 标志）。
- `rememberLaneClosed(id)`：`closedLanes` + 4096 环形上限（与 relay 墓碑对应）。
- `queueLane(value)`（入参已 splitFrames 拆单帧）：
  - lane 不存在且帧是 DATA/CLOSE/WINDOW→**静默丢弃**（lane 已被关闭，relay 侧同样忽略）；
  - lane 不存在且 id 在 closedLanes→**抛错**（复用已关闭 lane=协议错）；
  - lane 不存在且首帧非 OPEN→抛错；
  - reserve(data, lane)→`lane.pending.push`→`runLaneUp`。
- `runLaneUp(lane)`：与 runUp 同构，但 `X-Up-Seq`+`X-Lane-ID`；**ack 后顺带启动该 lane 的 pollLane**（每 lane 一对）。
- `pollLane(lane)`：同 poll 但带 `X-Lane-ID`；**收到的批次先 splitFrames 校验每帧 id===lane.id**（跨 lane 帧=致命错）；204 且 `X-Lane-Closed==='1'`→删 lane+rememberClosed+停止轮询。
- **关键防御**（HARDENING 后）：所有 lane 级失败 `fail()` 整个载波（https-lanes 的 lane 故障=父故障，与 relay 侧 laneProtocolFailure 语义一致）。

### 9.7 `websocket` 载波

- `openWebSocket()`：`new WebSocket(relayOrigin.replace(/^https:/,'wss:')+'/api/v1/ws', 'tproxy-v1.'+sessionToken)`；binaryType='arraybuffer'；onopen resolve；onmessage：非 ArrayBuffer 或空→fail；否则 traffic+transfer 给客户端；onerror reject；onclose→fail（**WS 断=会话终**）。
- `queueWebSocket(data)`：reserve→upPending→`runWebSocketUp()`。
- `runWebSocketUp()`：**浏览器缓冲反压**——`webSocket.bufferedAmount+queuedBytes>queueLimit || bufferedAmount≥batchLimit` 时 10ms 定时器重试（不丢数据）；可发时 `joinPending`→`send`→release→traffic；队列未清则 `queueMicrotask` 续跑。**这里就是 relay 侧 readWebSocket 30 秒 ErrBackpressure 重试的对应端**——两端共同实现"临时后端写反压最多等 30 秒"的协议条款。

### 9.8 `websocket-lanes` 载波（含 9 月 3 日 DDoS 修复）

- `openWebSocketLane(lane)`：子协议 `tproxy-lane-v1.<token>.<lane.id>`；onopen：closed/finished 则直接关；`lane.opened=true`+status+`runWebSocketLaneUp`；onmessage：ArrayBuffer 校验→`splitFrames`→**每帧 id===lane.id**→若含 type 3 置 remoteClosed→transfer 交付；onerror 空实现（onclose 统一处理）；onclose：
  - closed/finished→返回；
  - **`!lane.opened`→fail()**（建立失败=父载波故障，PROTOCOL 原文语义）；
  - 否则 `finishWebSocketLane(lane, !lane.localClosed && !lane.remoteClosed)`——**socket 意外断开且既非客户端主动关也非收到过 CLOSE 帧→给 App 补发一个合成 CLOSE 帧**（App 侧该流收到关闭、重建；其余 lane 不受影响）。
- `finishWebSocketLane(lane, notify)`：幂等（finished 哨兵+`lanes.get(lane.id)===lane` 身份双检）；清 timer；**归还 lane 记账**；清队列、删 lane、rememberClosed；socket OPEN/CONNECTING 则 close；notify 时构造 `closeFrame(lane.id)`（手写 8 字节 CLOSE 帧：type=3、u24 id、len=0）交付给 App。
- `queueWebSocketLane(value)`：与 queueLane 同构的丢弃/抛错规则；**CLOSE 且 lane 未开→直接 finishWebSocketLane(false)**（本地刚 OPEN 又立刻 CLOSE，甚至还没建 socket——**这就是 DDoS 修复的主战场**：恶意/突发的大量"开流又立刻关流"不再排队握手 WebSocket）；reserve→pending.push（type 3 置 localClosed）→无 socket 则 `openWebSocketLane`，有则 `runWebSocketLaneUp`。
- `runWebSocketLaneUp`：同 runWebSocketUp 的 lane 版（bufferedAmount≥batchLimit 时 10ms 定时重试；queueMicrotask 续跑）。

### 9.9 边界与生命周期收尾

**loopback 父边界**（`addEventListener('message')`）：

```javascript
if(initialized||event.source!==parent||event.data===null||typeof event.data!=='object')return;
const keys=Object.keys(event.data).sort();
if(keys.length!==2||keys[0]!=='t'||keys[1]!=='v'||event.data.t!=='tproxy-init'||event.data.v!==1||event.ports.length!==1)return;
// origin 必须是 http://127.0.0.1:<显式端口>
```

- 精确形状（恰两键、排序后 t/v、值 1/1）、恰一个 MessagePort、`event.source===parent`、URL 解析后的 origin 必须显式小端口 127.0.0.1——**五重校验**，与 PROTOCOL 逐字对应。

**注入式边界**（`TelegramWebProxy` 适配）：

```javascript
const androidPort={onmessage:null,start(){},close(){androidBridge.onmessage=null},
 postMessage(value){
   if(value instanceof ArrayBuffer){
     let frames;try{frames=splitFrames(value)}catch(error){fail();return}
     for(const frame of frames)androidBridge.postMessage(frame.data);   // 拆成单帧
   }else androidBridge.postMessage(JSON.stringify(value));               // 控制值走 JSON 字符串
 }};
```

- 桥接页把**聚合下行批次按验证过的帧边界拆开**再过 IPC（约 64 KiB/条，避免单条 2 MiB WebMessage——ANDROID.md 的设计动机）；
- 客户端→页方向：`androidBridge.onmessage` 收字符串（JSON.parse）或二进制，转交 `androidPort.onmessage`；
- 激活后立刻回发 `{"t":"tproxy-android-init","v":1,"nonce":androidNonce}`（App 校验 nonce 绑定 WebView 实例）。

**收尾**：`addEventListener('pagehide',()=>close(true),{once:true})`——页面卸载即尽力 DELETE 会话（`fetch(...,{keepalive:true})`）；`close(notifyServer)`：abort 轮询控制器、关全部 lane 定时器/socket、关 WS、可选 DELETE、`port.close()`。`port.onmessage` 处理：ArrayBuffer→首个触发 createSession/建会话前入 pending/正常 queueCarrier；`{t:'close'}`→`close(true)`（客户端替换载波时主动通知，防孤儿会话）。

### 9.10 桥接页设计总评

- **协议纪律极强**：所有数值界（32 MiB/16384/4096/8 MiB/1024/batchLimit/90s/9 次/503 语义）与 PROTOCOL.md、Go 端一一对应，可互查；
- **故障域清晰**：https-lanes 的 lane 故障→父故障（fail()）；websocket-lanes 的已建立 lane 故障→只关该流（合成 CLOSE）；建立失败→父故障——三种情形与 PROTOCOL 语义严格一致；
- **防御性编程**：拆帧验证在交付前（splitFrames/跨 lane 校验）、记账在排队前（reserve）、游标在交付后；
- **可见的取舍**：整页单线程、无 WASM、无 Worker（受限 WebView 兼容）；所有等待用 10ms/退避轮询而非事件回调（简单性优先）。

---

## 10. 部署体系逐文件精读（deploy/ 全部 13 个文件）

### 10.1 deploy/install.sh（262 行，一键安装器）

**前置校验**：root（`EUID`）、`uname -m == x86_64`（否则退出——MTProxy 官方构建限制）；secret 未给参数则**无回显交互输入**（避免进 shell history 与进程列表；`--secret` 供无人值守但有进程参数暴露风险——文档如实警告）；hostname 正则 `^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$` 且含点；secret 正则 `^([0-9a-f]{32}|dd[0-9a-f]{32})$`；email 正则；workers 1-256、max-connections 正整数；`--static-routes ∈ {exact,legacy}`（默认 exact）；`--site-dir`/`--site-upstream` 互斥且必居其一（已有 `/srv/tproxy-site/index.html` 时可都不给——重装场景）。

**安装步骤**（严格顺序）：

1. `apt-get install ca-certificates curl nftables`；
2. **Caddy 2.11.4**：GitHub release 下载 tar.gz→**sha512sum 精确校验**（`8220d1f013b6...f1c9`）→解压装 `/usr/local/bin/caddy`→建 `caddy` 系统用户、`/etc/caddy`(0750 root:caddy)、`/var/lib/caddy`；
3. `install-mtproxy.sh`（见下）；
4. 建 `tproxy` 系统用户；
5. **Go 工具链**：PATH 里 ≥1.20 则复用；否则下载 **go1.26.5**（sha256 `5c2c3b16...93f053` 校验）解压到 `/opt/go1.26.5`（已存在则用临时目录）；
6. **跑全部 Go 测试**再 `go build -trimpath -ldflags='-s -w'` 装 `/usr/local/bin/tproxy-server`（root:root 0755）——**安装即测试**；
7. 站点：新装复制 `--site-dir`→`/srv/tproxy-site`（已存在则保留并提示单独更新）；umask 077 下目录权限 root:root；
8. `ensure-token-key.sh` 制备密钥；生成 `/etc/tproxy-server/config.json`（公共源二选一注入）与 `profiles.json`（default profile、secret、backend 2398），权限 0640 root:tproxy / **0400**；
9. `mtproxy.env`：`MTPROXY_SECRET`（**dd 前缀剥掉**——后端只吃 16 字节基 secret）+workers/connections（0640 root:mtproxy）；
10. Caddyfile：写入 `/etc/caddy/Caddyfile.tproxy`；已有不同 Caddyfile 则**时间戳备份**再覆盖；caddy.service 同样备份覆盖；drop-in `caddy.service.d/tproxy.conf` 注入 `TPROXY_HOSTNAME/TPROXY_SITE_ROOT/ACME_EMAIL`；
11. 安装 6 个 systemd 单元与 firewall.nft、refresh 脚本；
12. **双重验证**：`tproxy-server -check` + `caddy validate`（同环境变量）；
13. `daemon-reload`→按依赖序 enable/restart（firewall→mtproxy→tproxy-server→timer→caddy）；
14. **就绪等待**：最多 20×1s 轮询 `/readyz`，失败则安装失败退出；成功打印三条验证命令。

### 10.2 deploy/install-mtproxy.sh（67 行）

- root+x86_64 校验；装构建依赖（build-essential、libssl-dev、util-linux、zlib1g-dev）；
- 建 `mtproxy` 系统用户；
- **固定 commit `f36d8af769ffaeac36978d38c2c0f6d1104c2137` + tar.gz sha256 `919795c416b870670841a21d1930ad97a24c7b84b9eb8c6f9e3de32f2fdf4655` 校验**；已有构建且 `.tproxy-commit` 匹配则跳过（幂等）；
- **以 mtproxy 用户 `runuser make`**（非 root 构建），产物存在性检查，`.tproxy-commit` 标记，root 接管安装到 `/opt/MTProxy`（旧目录时间戳备份）；
- 下载 `https://core.telegram.org/getProxySecret`（校验**恰 128 字节**）与 `getProxyConfig`（≥100 字节且含 `^default ` 与 `^proxy_for ` 行）→mktemp 原子 `mv` 到 `/etc/mtproxy/`（0640 root:mtproxy）。

### 10.3 deploy/ensure-token-key.sh（32 行）

- 默认 `/etc/tproxy-server/token.key`；root；**拒绝符号链接与非普通文件**；
- 不存在时：mktemp 临时文件→`head -c 32 /dev/urandom`→`chown tproxy:tproxy`+`chmod 0400`→**用 `ln`（硬链接）原子落位**——若期间出现同名文件则拒绝替换（防 TOCTOU/竞换）；
- 已存在：字节数必须恰 32，否则**拒绝替换**（保护既有签名身份）；
- 结尾统一 `chown tproxy:tproxy; chmod 0400`——注释："Preserve the key bytes across reinstalls, updates, and binary rollbacks."

### 10.4 deploy/update-relay.sh（143 行，原子更新器）

- root、三个必需文件（二进制/配置/profiles）、四个命令（curl/flock/install/systemctl）检查；
- **`flock -n /run/lock/tproxy-server-update.lock`**（防并发更新）；
- Go 查找：PATH + `/opt/go*/bin/go`，版本 ≥1.20；
- 流程：记录更新前 ready 状态→**`go test ./...`**→构建候选→**令牌迁移**：`/etc/tproxy-server/token.key` 不存在时，安装 `token-migration.conf` drop-in（`TPROXY_LEGACY_TOKEN_DRAIN=1`；已存在且内容不同则拒绝自动处理，要求人工）→`ensure-token-key.sh`→**候选二进制 `-check` 用真实配置验证**→备份旧二进制到 `.previous`→`install` 候选到 `.next`→`mv` 原子上位→`systemctl restart`；
- **健康门**：20×1s 等 `/healthz`，失败→**rollback()**（恢复 .previous、重启、再等健康；回滚也失败则提示 journalctl）；更新前 ready 的还要等回 `/readyz`，失败同样回滚；
- 结尾提示：会话已作废客户端自动重连；迁移 drop-in 的收尾指引（HARDENING.md 的三行命令）。

### 10.5 deploy/Caddyfile（44 行）

```caddyfile
{
	email {$ACME_EMAIL}
	admin off                       # 关闭 Caddy admin API
	servers {
		protocols h1 h2             # 显式只用 HTTP/1.1+2
		timeouts {
			read_header 10s
			read_body 60s           # 注释：必须远高于 long_poll(25s)，防切断停泊轮询
		}
	}
}
{$TPROXY_HOSTNAME} {
	encode zstd gzip                # 每个响应统一包裹（octet-stream 默认不匹配，载波体不压缩）
	header {
		-Via                         # 外层 header 指令删 Via（HARDENING 修正：header_down 无效）
		Strict-Transport-Security "max-age=31536000; includeSubDomains"
	}
	reverse_proxy 127.0.0.1:8080 {
		transport http { response_header_timeout 40s }
	}
	handle_errors {                 # 后端宕机：所有路径同样错误，无路径特判
		header { Cache-Control "no-store"; Strict-Transport-Security ... }
		respond "{http.error.status_code} {http.error.status_text}" {http.error.status_code}
	}
}
```

三处注释都是设计理由：全路径单后端（无第二个可探测面）；read_body 与 long_poll 的关系；后端宕机行为与普通站点无异。

### 10.6 deploy/firewall.nft（6 行）

```nft
table inet tproxy_backend {
	chain local_backend {
		type filter hook input priority -10; policy accept;
		iifname != "lo" tcp dport { 2398, 8888 } drop
	}
}
```

非 lo 接口丢弃 MTProxy 客户端口与统计端口（`-H` 无 bind 参数的补救）。

### 10.7 systemd 单元（5 个）

**tproxy-server.service**（45 行，最严格）：

- 依赖：`After/Wants network-online + mtproxy + tproxy-firewall`、`Requires tproxy-firewall`；
- `User=tproxy`；**`LoadCredential=profiles.json:/etc/tproxy-server/profiles.json`**（凭据经 systemd 注入 `/run/credentials/...`，isSystemdCredential 对应放行 0400 root 文件）；
- `Restart=on-failure`（3s）、`TimeoutStopSec=20s`、`LimitNOFILE=1048576`；
- **沙箱矩阵（19 项）**：NoNewPrivileges、PrivateDevices、PrivateTmp、ProtectClock/ControlGroups/Home/Hostname/KernelLogs/KernelModules/KernelTunables、**ProtectProc=invisible + ProcSubset=pid**（看不见其它进程元数据——注释回应 -S 秘密在 MTProxy argv 的威胁）、ProtectSystem=strict、ReadOnlyPaths=-/srv/tproxy-site、RestrictAddressFamilies=AF_INET AF_INET6、RestrictNamespaces、RestrictRealtime、RestrictSUIDSGID、LockPersonality、**MemoryDenyWriteExecute**、**CapabilityBoundingSet=（空）**、**IPAddressDeny=any + IPAddressAllow=localhost**（连出仅回环——后端强制本地）、SystemCallArchitectures=native、SystemCallFilter=@system-service、UMask=0077。

**mtproxy.service**（33 行）：`EnvironmentFile=/etc/mtproxy/mtproxy.env` + 两个 Environment 默认；ExecStart 完整命令行（`-S ${MTPROXY_SECRET}`——**秘密在进程参数里，文档承认这是上游接口无法避免的残留暴露面**）；沙箱矩阵同型但无 IPAddressDeny（MTProxy 要出网到 Telegram DC）。

**caddy.service**（35 行）：`AmbientCapabilities=CAP_NET_BIND_SERVICE`（80/443 特权端口的唯一能力）+ CapabilityBoundingSet 同值（**最小能力集**）；XDG 三环境变量；沙箱同型。

**tproxy-firewall.service**（23 行）：**`After=nftables.service + PartOf=nftables.service + Before=mtproxy tproxy-server`**——注释解释：Debian 默认 `/etc/nftables.conf` 开头 `flush ruleset`，任何发行版 nftables 重启会静默丢掉 tproxy_backend 表使 0.0.0.0:2398 暴露；绑定后每次 nftables 重启都重放本表。`ExecStart=-nft delete table`（`-` 容错）+ `nft -f firewall.nft`；RemainAfterExit；ExecReload/ExecStop 对称。

**refresh-mtproxy-config.service/timer/sh**（47 行合计）：oneshot 拉 `getProxyConfig`（同样的字节数与行校验）；**`cmp -s` 无变化则不重启**（有变化才 `systemctl try-restart mtproxy`）；timer：`OnBootSec=10m`、`OnUnitActiveSec=1d`、`RandomizedDelaySec=1h`（错峰）、`Persistent=true`；服务沙箱含 `ReadWritePaths=/etc/mtproxy`。

### 10.8 部署体系总评

- **三道防火墙边界**：云商安全组（文档第一步）→ nftables（本机）→ relay 只听回环；
- **四类供应链校验**：Caddy sha512、Go sha256、MTProxy commit+sha256、Telegram 配置的字节/行校验——**全部 pin 死**；
- **原子性与幂等性**贯穿：mktemp+mv、硬链接、flock、`.tproxy-commit`、cmp-then-restart、时间戳备份、`.previous` 回滚；
- **非 root 构建**（MTProxy）与 root 安装分离；
- 每个脚本 `set -euo pipefail`；install 全程 `umask 077`（社区 issue #9/#10/#12 报告的 umask 相关问题见第 16 节）。

---

## 11. 安全设计专项分析

### 11.1 威胁模型（文档+代码反推的完整版）

| 威胁 | 防线（代码落点） |
|---|---|
| 被动 DPI/流量分析 | 真HTTPS+Caddy（网站形态）；载波体不压缩（encode 不匹配 octet-stream，避免压缩侧信道差异） |
| 主动探测：猜/扫 bridge URL | 43 字符=256 位 HMAC；命中才发桥接页，未命中=公网站点 200（`secrets.go`+`server.go`） |
| 主动探测：用真凭据做畸形请求观察差异 | `hasInternalSecret` 全元数据扫描→内部路径一律**本地 404 no-store**，永不触公网应用；方法/头/query/cookie 的非规范形态在 serveAPI 逐项拒绝 |
| 主动探测：能力/令牌泄露进网站日志 | 内部请求不走公共处理器；文档反复禁止 URI/头/体访问日志；Metrics 只有计数 |
| 令牌伪造 | 16B nonce+16B 截断 HMAC-SHA256（独立持久密钥）；常数时间比较 |
| 令牌重放 | bootstrap 2 分钟一次性；会话令牌随状态作废；closedTokens 墓碑幂等 |
| 时序侧信道 | `MatchCapability` 全表常数时间扫描；`ClassifyToken` 双常数时间比对；`Create` 幂等比对用 `subtle.ConstantTimeCompare` |
| 跨站脚本/数据渗出 | CSP default-src 'none'+nonce 脚本+sandbox；Permissions-Policy 全拒；无存储/无外部资源；credentials omit |
| 回环父边界仿冒 | 精确形状+单端口+source==parent+127.0.0.1 显式端口 origin 五重校验 |
| 注入边界仿冒 | nonce 绑定 WebView 实例+主框架+精确 origin（平台文档规定平台侧校验） |
| SSRF/目的地选择 | OPEN 只拨 profile 的数字回环 backend（config 强校验）；relay 无任何客户端可影响的目的地参数 |
| 资源耗尽（DoS） | 全套限额（第 12 节）+ 令牌桶 + 墓碑 + 队列记账 + 30s 体读期限 + WS 空闲 2×long_poll + 64B create 体上限 |
| websocket-lanes 握手风暴（9-03 修复） | 客户端 CLOSE 未建 lane→直接 finish；取消 CONNECTING socket；Node vm 回归测试 |
| 进程内提权/横向 | systemd 19 项沙箱矩阵 + IPAddressDeny=any/Allow=localhost + 空 CapabilityBoundingSet |
| 凭据文件泄露 | profiles 0400（systemd LoadCredential 白名单）；token.key 0400/32B/原子创建/拒绝替换 |
| -S 秘密在 MTProxy argv | 承认残留风险；ProtectProc=invisible 缓解；文档要求不给不可信用户 shell |

### 11.2 值得记录的安全取舍（文档坦白的）

1. **公共回退的"对等性"不完美**：HARDENING.md 明确——逐字节等价不可达（hop-by-hop 头、分帧、解析器限额、HTTP 版本转换、失败层、时序都可观察；真 Caddy 测试还发现偶发多余空 gzip 块）。本变更只是**消除路径依赖的 relay 决策**，不隐藏 Caddy/Go/IP/站点所有权。
2. **同源信任模型**：PUBLIC_SITE.md 直言公共应用是"**与桥接页共享 origin 的受信代码**"——service worker 可拦截桥接导航；同源/第三方注入脚本具有该 origin 的权威。要求：不装覆盖桥接路径的 service worker、不托管不可信可执行内容。
3. **drain 模式故意保留探测信号**：迁移期 `TPROXY_LEGACY_TOKEN_DRAIN=1` 会按令牌**形态**识别旧随机令牌——HARDENING 承认"故意保留 credential-shape 探测信号"，启动日志会报告该模式启用；迁移完成删 drop-in 才恢复完全公共透传。
4. **WebView 不保证零跨源请求**：三份文档一致声明——CSP/shims 是缩减面，平台原生策略才是边界；WebRTC 明确"不声称在所有引擎上可靠禁用"。
5. **`dd` 前缀只影响客户端**：后端 MTProxy 始终收 16 字节基 secret（install.sh 剥前缀）；`ee` 伪 TLS 模式**明确排除在 v1 客户端契约外**。

### 11.3 密码学清单

- HMAC-SHA256 ×3 用途：capability 派生（域分离标签 `tdesktop-web-proxy-bridge-v1\n`）、令牌 MAC（域分离 `tproxy-server-token-v1\0`+kind 字节）、ETag（sha256 摘要）；
- crypto/rand：nonce（18B/页）、令牌 nonce（16B/个）；
- 常数时间比较：capability 匹配、令牌分类、幂等摘要；
- 令牌存储形态：map 键=sha256(token)（原文不落 map）；secret 字节用后即清零（loadProfiles defer 清零+派生后二次清零）。

---

## 12. 资源限制与配额体系全表

### 12.1 进程级（config.limits，全局权威）

| 字段 | 默认 | 含义 |
|---|---:|---|
| `max_header_bytes` | 16384 | HTTP 头字节上限（HTTP 服务器级） |
| `max_body_bytes` | 2097152 | 二进制请求体上限 |
| `max_frame_payload` | 1048576 | 单帧载荷上限 |
| `carrier_batch_bytes` | 2097152 | 下行批次目标（**硬顶 2 MiB**，桌面回环回退消息上限） |
| `max_streams_per_session` | 128 | 每会话逻辑流数 |
| `max_closed_stream_ids` | 4096 | 每会话墓碑环容量 |
| `max_pending_per_session` | 33554432 | 每会话排队字节（含 256/项成本） |
| `max_pending_global` | 536870912 | 进程排队字节 |
| `max_pending_items_per_session` | 16384 | 每会话排队项 |
| `max_pending_items_global` | 262144 | 进程排队项 |
| `max_sessions_per_ip` | 0（禁用） | 每 IP 会话硬限（CGNAT 考量，仅记账默认） |
| `max_sessions_global` | 128 | 活跃载波会话 |
| `max_streams_global` | 4096 | 活跃后端 TCP 流 |
| `max_backend_dials_in_flight` | 256 | 并发后端拨号 |
| `new_sessions_per_minute`/`burst` | 600/128 | 会话创建令牌桶 |
| `new_streams_per_minute`/`burst` | 6000/512 | 流创建令牌桶 |
| `max_bootstraps_per_ip` | 0（禁用） | 每 IP 未用 bootstrap |
| `max_bootstraps_global` | 512 | 活跃 bootstrap 条目 |
| `new_bootstraps_per_minute`/`burst` | 1200/256 | bootstrap 令牌桶 |
| `max_profiles` | 32 | profile 数上限 |

### 12.2 Profile 级（可省略，继承=钳制，只能更低）

`max_sessions`、`max_streams`、`max_backend_dials_in_flight`、`new_sessions_per_minute/burst`、`new_streams_per_minute/burst`、`max_streams_per_session`、`max_pending_per_session`——9 项；解析后自洽校验：`max_streams_per_session ≤ max_streams`、`max_backend_dials_in_flight ≤ max_streams`。

### 12.3 超时（timeouts）

| 字段 | 默认 | 用途 |
|---|---|---|
| `backend_dial` | 5s | 后端拨号超时 |
| `long_poll` | 25s | 长轮询停泊时长（WS 空闲=2×此值） |
| `reconnect_grace` | 2m | 全静默后会话可恢复窗（文档论证：桌面 MTProto ~30-45s 已重置连接） |
| `bootstrap_lifetime` | 2m | bootstrap 有效期（也是 closedTokens 墓碑期） |
| `read_header` | 10s | 读头期限 |
| `idle` | 75s | HTTP 空闲连接 |
| `shutdown` | 15s | 优雅停机预算 |

### 12.4 内部记账常量（session.go）

`queueItemCost=256`（每项保守成本）；`controlReserveExtraItems=16`；`controlReserveItemsPerStream=3`；控制保留 cost=`items×(256+8+4)`；上行保留=`MaxBodyBytes + min(MaxBodyBytes/8, 4096)×256`。closedTokens 墓碑容量=`MaxSessionsGlobal×16`。

### 12.5 配置校验不变式（启动拒绝）

全局≥每会话/每 IP 系 6 条；`ValidateBudget`（控制保留×会话数不得耗尽全局）；`carrier_batch_bytes ≤ 2 MiB` 且 ≤ max_body_bytes；256 KiB ≤ batch；所有资源限为正；监听/管理/上游/后端全部数字回环；公共源恰一个；profile 名/能力唯一；令牌密钥恰 32B 且 0400。

**语义分层**：全局桶保护进程；profile 桶防单 secret 独占；超限 OPEN→**只 CLOSE 该流**（会话与其它流不动）；认证后的会话创建过载/上行队列满/重试撞解析→**503+Retry-After**（可安全重试）；下行并发→新者胜。

---

## 13. 性能模型与吞吐量上界

### 13.1 `https` 模式的停等天花板（PLAN 7.3 权威表）

| WebView→relay RTT | 纯 RTT 上界 |
|---:|---:|
| 50 ms | 40 MiB/s |
| 100 ms | 20 MiB/s |
| 200 ms | 10 MiB/s |
| 500 ms | 4 MiB/s |

- 4 MiB 流窗口**刻意大于** 2 MiB 批次：单流可在 WINDOW 信用反向途中继续发送；
- 验收目标（受控 ≥100 Mbit/s 链路）：**500ms RTT 下 20 Mbit/s、200ms RTT 下 40 Mbit/s**（经各原生 WebView 及可选系统浏览器载波）；
- Caddy 压缩对 `application/octet-stream` 默认不生效→加密载波体不做 zstd/gzip（每响应统一 encode 只是外形一致）。

### 13.2 各模式消除什么、付出什么

- `https-lanes`：消除全局停等（每流独立序列/重试状态）；依赖 HTTP/2 并发长轮询；HTTP/1.1 每源连接数会成为瓶颈；
- `websocket`：消除 HTTP 确认循环（单有序流控 socket）；代价=所有流共享 TCP 拥塞与故障域；不做会话恢复；
- `websocket-lanes`：再隔离浏览器队列与 relay 写入器；独立 TCP 拥塞域**取决于** WebView 连接分配（不保证）；每 lane 一次 WS/TLS 握手（**macOS WebKit 会串行化同主机 WS 握手**——README 专门给出"握手风暴慢于连接超时"的实操建议：受影响 profile 改 `websocket` 模式即可，客户端零改动）。

### 13.3 优化路线图（PLAN 7.4 的优先级，未实施）

1. 批量 IPC（多帧一次原生 WebView IPC/共享缓冲，base64 仅兼容）；
2. 自适应亚毫秒/毫秒上行合并窗（活跃批量流量下，不延迟孤立控制帧）；
3. 小型有序上行管线或统一交换请求（带上行+游标跟踪下行，保持字节相同重试）；
4. 池化载波/下行缓冲、保留请求体所有权、向量化后端写（先 profile 再池化——复杂度警告）；
5. MTProxy worker/fd/连接限调优；仅当 profile 显示 Go→MTProxy 跳是实质瓶颈才考虑 Go 里终结 TLS 或 Caddy 模块；**合并 Go 进 MTProxy 的 fork 论证被明确否决为 v1 不可接受维护面**；
- 明确的反模式警告：在 `/up` 响应上捎带下行帧不自动更快（`/down` 本就并发在外；双响应路径需要单一有序游标/重放所有者）。

### 13.4 性能测量方法论（PLAN 7.4）

四层同主机同流量对比：裸 MTProxy→直连 Go→Caddy+Go→完整 WebView 路径；记录 CPU-秒/GiB、RPS、p50/p95 载波时延、活连接、分配/GC、RSS、重传、多 RTT×流数的吞吐；worker/内核限/路由/载荷保持一致。**"不要把进程数当性能结果"**。

---

## 14. 测试体系全量盘点（77 个测试函数）

**包分布**：bridge 7+1、config 11+1、frame 4、server 11+5+2+4+1、session 29+1 = **77 个**。全部本地可跑（Caddy 套件除外，opt-in）。本次调研实测全部通过（见第 18 节）。

### 14.1 全量函数清单（按主题分组）

**帧编解码**：`TestRoundTrip`、`TestRejectsPartialAndOversized`、`TestRejectsExcessiveFrameCount`、`TestHelloAndWindow`（frame 包 4 个）。

**配置**：`TestHostnameValidation`、`TestCapabilityVectors`（两个规范向量）、`TestPlainSecretMayBeginWithEE`、`TestProfileCarrierModeDefaultsAndValidation`、`TestProfileLimitsCannotExceedGlobalCeilings`、`TestProfileSessionLimitDoesNotConsumeGlobalCapacity`、`TestProfileStreamDefaultsRespectProfileCeiling`、`TestLoadAppliesDefaultsAndRelativePaths`、`TestLoadAcceptsSystemdCredentialReadPermissions`、`TestPublicSourceValidation`、`TestTokenKeyRequiresPrivatePersistentFile`。

**桥接页渲染**：`TestRenderUsesNonceAndConfiguredBatch`、`TestRenderUsesHardenedExecutionPolicy`（CSP 逐字段断言）、`TestRenderRejectsInvalidHostnameAndOversizedBatch`、`TestRenderRejectsInvalidBatch`、`TestRenderIncludesSelectableCarrierImplementations`（四模式各自代码存在性）、`TestRenderedBridgeSurvivesTheRestrictedProfile`（受限执行档案下可运行——Node vm）、`TestRenderedBridgeAvailabilityFixes`、`TestCarrierBatchMustFitDesktopLoopbackCap`、`TestWebSocketLaneCancellation`（DDoS 修复回归——Node vm 跑 `testdata/lane_cancellation.js`：OPEN→DATA→CLOSE 时**排队中的 WS 握手必须被中止且数据绝不上送**）。

**会话核心（session 包 29 个，race 覆盖）**：序列/重试（`TestSequenceRetryAndMismatch`、`TestConcurrentUplinkIsRejectedAndNewestPollWins`、`TestConcurrentUplinkIsRetryableAndDownlinkSupersedes`）；流生命周期（`TestBackendEOFClosesOnlyItsStream`、`TestStreamLimitRejectsOnlyTheNewStream`、`TestRejectsStreamReuseAndWindowOverrun`、`TestSessionCloseStopsBackendGoroutines`、`TestStreamCancellationWakesZeroCreditWaiter`）；预算/背压（`TestPendingByteLimitIncludesBackendWrites`×3 个变体、`TestPendingItemLimitIncludesTinyWrites`、`TestDownlinkBudgetPausesBackendReads`、`TestDownlinkBudgetPreservesOneUplinkBatch`、`TestDownlinkDataLimitDoesNotCloseSession`、`TestControlFramesUseReservedQueueHeadroom`、`TestQueuedFramesChargeOverheadAndLimitBatchCount`、`TestUplinkBackpressureIsRetryable`）；bootstrap/限额（`TestBootstrapLimitsExpiryAndConsumption`、`TestBootstrapRateIsGlobal`、`TestGlobalBootstrapPoolEvictsOldestUnusedEntry`、`TestBootstrapSurvivesChangingClientAddress`、`TestSessionCapacityOverloadIsRetryable`、`TestSessionCreationRateIsGlobal`、`TestDefaultReconnectGraceIsShort`、`TestDisabledPerIPSessionLimitAllowsSharedAddress`、`TestBackendDialLimitReopensAfterDialCompletes`）；lane（`TestLaneSequencesAndReplayAreIndependent`、`TestHTTPSLanesRoundTripIndependently`、`TestLaneRejectsCrossStreamFrames`、`TestClosedLaneReplaysDownlinkAndFinalUplink`、`TestEvictedLaneReleasesBudgetAndIgnoresLateFrames`、`TestWebSocketLanesRemainIndependent`、`TestWindowFramesCoalesceAcrossOtherControlFrames`）；令牌（`TestTokenProvenanceSurvivesExpiryAndRestart`）。

**服务器行为（server 包 23 个）**：认证与二分（`TestPublicFallbackAndCarrierRoundTrip`、`TestAuthenticSecretsNeverReachPublicHandler`、`TestAPIRejectsUnknownBearerAsPublic404`、`TestSessionAuthenticationDoesNotDependOnOrigin`、`TestSessionCreateAuthenticatesBeforeReadingBody`、`TestMalformedDeleteDoesNotCloseSession`、`TestBridgeLimitFailsLocally`、`TestOnlyInternalBodiesGetCarrierDeadline`）；公共语义（`TestPublicRequestsPreserveApplicationSemantics`、`TestDynamicPublicUpstreamAndTransportCoexist`、`TestStaticSiteLoadsWholeTree`、`TestStaticConditionalRequestsRangesAndExactRoutes`、`TestAdminSurfaceIsSeparate`、`TestProbingParityAcrossAPIAndStaticPaths`、`TestLegacyDrainContainsOldTokensUntilClientsReload`）；WebSocket（`TestWebSocketCredentials`、`TestWebSocketCarrierRoundTrip`）；对等性套件（`TestCaddyPublicParityAndCarriers`——opt-in 真二进制）。

### 14.2 测试基础设施特征

- **确定性假后端**（fake TCP backend）贯穿 session 集成测试：无丢失/无重复/无重排/内存有界的多并发字节流断言；
- **故障注入**：丢失已提交后的响应、重复 POST、丢轮询、延迟轮询、后端拒连/EOF/半关/慢读写/重启、双侧同时关闭+迟到帧；
- **race 构建覆盖**（HARDENING 验证命令 `go test -race ./...`）；
- **Node vm 运行时测试**：把渲染出的 JS 放进沙箱（伪造 location/history/WebSocket/fetch/TelegramWebProxy）断言行为——**服务端仓库测试前端代码**，罕见的严谨度；
- **Caddy 对等套件**（`TPROXY_CADDY_BIN` 指向真 2.11.4 二进制才跑）：适配真实部署 Caddyfile、临时回环监听+测试证书、HTTP/1.1 与 HTTP/2 双栈对比 Caddy→应用 与 Caddy→relay→应用；覆盖：应用可见元数据与体、chunked 上传与 trailer、随机/重复 bearer、畸形 query、cookie、条件/范围头、响应策略/trailer、gzip 解码内容对等与协商（**不要求压缩块逐字节相同**）、HTTP/1.0、异常 Host/SNI、畸形升级、后端宕机、传输路径上的公共 WebSocket、Via 删除、全部四种认证载波往返。
- **测试不承诺的事**（HARDENING 原文）："These tests are not a complete TLS/parser fingerprint audit."

---

## 15. 客户端平台文档要点（ANDROID.md / IOS.md）

（客户端代码不在本仓库；两文档是其与本服务端的契约面。）

### 15.1 ANDROID.md（296 行）

- 架构：tgnet 的 MTProxy 变换原样复用→指向 Java sidecar 的回环监听（每 socket=一流）→AndroidX WebKit `WebViewCompat.addWebMessageListener`（**精确 origin allow 规则**，拒绝 addJavascriptInterface 的全帧注入无 origin 缺陷）+32B nonce 片段；
- 依赖 WebView 特性：`WEB_MESSAGE_LISTENER`+`WEB_MESSAGE_ARRAY_BUFFER`+`DOCUMENT_START_SCRIPT`；AndroidX WebKit 1.14.0（保持 API 21 最低）；**缺特性=关闭失败（指向未用回环端口），绝不回退直连**；
- 前台作用域（无前台服务/无渲染器提权；后台杀渲染器→回前台重建）；
- document-start 第二道 CSP（`unsafe-inline` 是有意的——运营商控制自含载波脚本；逐项禁外部资源）+ 不可替换 shim（localStorage/IndexedDB/CacheStorage/Workers/BroadcastChannel/audio 构造器/clipboard/device/window.open/document.cookie/print/alert/confirm/prompt）；
- WebView 设置矩阵（LOAD_NO_CACHE、禁 DOM/DB 存储、禁文件/内容访问、Safe Browsing 保持、拒全部 ChromeClient 回调、只允许 nonce 桥接 URL 主框架导航）+ `shouldInterceptRequest` 异源请求拦截（纵深防御，非唯一防线）；
- CookieManager 进程全局的坑（不能动 Mini Apps/支付→用可丢弃隔离 profile 或 DOM shim）；
- Android 侧源码改动点清单（WebProxyTransport.java、ProxyInfo v3 schema、ConnectionsManager 回环替换、ProxySettingsActivity 第三选项、t.me/webproxy 链接）；
- 构建要求（JDK17/SDK35/NDK 27.2/CMake 3.10.2，Mac 路径示例）；9 步测试序列+负测试清单；已知 PoC 限制（无独立 ping、WEB 行不参与轮换/通话、tgnet 内部仍记回环端点）。

### 15.2 IOS.md（300 行）

- 架构：MtProtoKit `MTTcpConnection` 的 obfuscated2 变换复用（`MTSocksProxySettings.secret` 存在即启用）→Swift `WebProxyTransport`（Network.framework 回环 NWListener）→WKWebView 主框架；
- **刻意仿真 Android 边界**：同样加载 `#android=<nonce>` URL、注入 page-world `globalThis.TelegramWebProxy`、`postMessage`↔随机名 `WKScriptMessageHandler` 翻译；`android`/`tproxy-android-init` 是**遗留线缆名而非平台断言**；
- WKUserScript(document-start, main-frame-only) 装 meta CSP+shim；原生侧五要素校验（实例/随机 handler 名+nonce/主框架/精确 origin/当前导航 URL）；拒 IP 字面量/用户信息/重定向/TLS 异常/子框架/通配；
- 非持久 WKWebsiteDataStore+每载波新 UserContentController；1 像素透明非交互视图挂主窗口；**appex 拒绝启动**（扩展进程无 WKWebView 语义）；前台 POC（iOS 后台挂起语义）；
- TelegramCore 改动清单（`.web(secret:)` 编码 `_t` 新值、Settings 解析失败关死、UrlHandling/tg://webproxy、`tproxyweb` scheme 隔离本地测试）；身份/诊断节（127.0.0.1 vs 公网主机名的 UI 陷阱）。

---

## 16. 社区状态：全部 14 个 Issue 与 PR

（GitHub API 实测，2026-09-06；编号 1-14，12 个仍 open。）

| # | 类型/状态 | 标题 | 提交者 | 日期 | 调研注记 |
|---:|---|---|---|---|---|
| 1 | ISSUE open | The installation was interrupted | hookzof | 08-22 | 安装中断报告 |
| 2 | PR open | Install MTProxy readable and executable by its service user | tral | 08-22 | 权限修复（与 #9/#10/#12 同族） |
| 3 | PR open | Keep the credential permission test independent of the caller umask | tral | 08-22 | umask 测试独立性 |
| 4 | PR open | Announce the NAT-ed public address to the Telegram middle-ends | tral | 08-22 | NAT 场景功能增强 |
| 5 | ISSUE open | CDN | MrParAziT | 08-23 | v1 明确非目标（PLAN 14） |
| 6 | ISSUE **closed** | WEB proxy stays connected but never passes MTProto DATA (12-byte WINDOW frames only) | Valtarean | 08-23 | 已关闭——疑似后续提交修复 |
| 7 | PR open | chore: escape file paths in config test JSON | Danil42Russia | 08-24 | 测试小修 |
| 8 | PR open | Add Docker Compose deployment with Cloudflare Tunnel | santaklouse | 08-24 | 与"无 CDN/直连 DNS"的 v1 指导相悖（Cloudflare Tunnel 改变流量形态） |
| 9 | PR open | fix: make install robust under umask 077 | bip-bup-bip-bup | 08-27 | umask 077 修复 |
| 10 | ISSUE open | install.sh: umask 077 fails the test gate and installs an unexecutable MTProxy (203/EXEC) | alexelagov | 08-28 | 与 #9 同根：install.sh 全程 umask 077，`runuser make` 产物可能缺 x 位 → 203/EXEC |
| 11 | ISSUE open | WEB Proxy connects but stops working after a few seconds | alexeyhome21 | 08-29 | 运行时断流报告（可能与 #6 相关） |
| 12 | PR open | fix two umask-dependent failures in a fresh installation | naprelsky | 08-31 | umask 族第三发 |
| 13 | PR **closed** | Remove 'Via' header from Caddyfile | hookzof | 09-02 | **已合并**（= commit `c0e9adf` 的 Caddy 修正） |
| 14 | ISSUE open | Proposal: a small local panel for managing client keys (profiles.json + MTProxy secrets) | sandamond | 09-02 | 功能提案 |

**模式观察**：社区贡献集中于**部署健壮性（umask 族占 5 条）**与**部署形态（Docker/面板）**；协议核心零 issue——与"单人官方仓、协议由客户端仓库共同演进"的结构一致。umask 族问题至今全部 open（安装器 `umask 077` 与 `runuser -u mtproxy make` 的产物权限交互是真实缺陷面）。

---

## 17. 代码质量与安全评估

### 17.1 优点（按证据强度）

1. **规格驱动**：PROTOCOL.md 规范性契约 ↔ PLAN.md 实现理由 ↔ 代码常量三方可互查；连"为什么 ReadTimeout 必须 2×long_poll"都有注释；
2. **安全纵深罕见地完整**：探测对等性（测试级验证）、令牌密码学（HMAC+常数时间+密钥持久化）、执行档案（CSP+Permissions-Policy+零资源）、系统沙箱（19 项 systemd 矩阵）、供应链（4 处哈希 pin）、网络边界（3 层）；
3. **记账不变式用 panic 守卫**（负预算/无效拨号计数立即崩溃而非漂移）；
4. **测试密度**：77 函数、race、假后端故障注入、Node vm 测前端、opt-in 真 Caddy 对等套件；
5. **运维工具链成熟**：原子更新+自动回滚、密钥原子制备、配置预检 `-check`、每日路由刷新只在变更时重启；
6. **信息纪律**：日志/metrics 全部脱敏成事件类与计数——文档与代码一致执行。

### 17.2 缺陷与风险（按严重度）

1. **无 LICENSE**（高）：法律上不可安全复用/分发；
2. **umask 安装缺陷族**（高，社区在案）：`install.sh` 的 `umask 077` 会传导到 MTProxy 构建产物与测试环境，造成 203/EXEC 启动失败（#9/#10/#12 复现链完整）；
3. **文档-代码漂移点**（中）：PLAN §14 非目标列表仍写"WebSocket…非目标"，而 v1 已实现两种 WS 载波；PLAN §8 提"ten-minute idle grace"（M2 验收也写十分钟），而实现与 README 为 2 分钟 `reconnect_grace`——早期文案残留；
4. **会话不可恢复**（中，by design）：relay 重启/WS 断开即全量重建；`https` 模式 503 重试窗 90 秒；
5. **吞吐天花板**（中，by design）：`https` 模式 2 MiB/RTT；IPC 与 base64 复制开销在移动端待测（IOS.md 自认）；
6. **单一维护者 + 无 CI**（中）：仓库级质量依赖个人纪律；race 测试与 Caddy 套件不在任何自动管线里；
7. **每 lane 一条 WS 的连接放大**（低-中）：macOS WebKit 握手串行化已有实操规避（文档建议切 `websocket`）；
8. **`-S` argv 暴露**（低，已承认+缓解）：宿主机不可信 shell 用户可读；
9. **帧解析零拷贝引用请求体**（低）：`ParseAll` 的 Payload 直接切片引用 body——生命周期被 `applyBatchLocked` 的 append 拷贝（`append([]byte(nil), payload...)`）正确接管；`queueFrameLocked` 的 DATA 帧在合并路径就地 append 会重新分配，语义安全，但依赖"写路径必拷贝"这一隐式约定，代码未注释（本次调研确认无实际悬挂引用）；
10. **`takeRate` 令牌桶先全局后 profile 的两段判定**（低）：`allowProfileRateLocked` 在任一桶不足时都不扣（正确），但两桶时间基准各自独立推进，长时间只打单 profile 时另一桶满额闲置——行为符合规格，非缺陷；
11. **`emptyBody` 的 1 字节试探读**（低）：对 chunked 空 body 的处理依赖 `r.Body.Read` 返回 0——标准库语义下成立；不带 Content-Length 的非空体会被误判吗？不会：Read 会返回 >0 或错误；极端 Slowlori 场景有 30s 体期限兜底。

### 17.3 与旧版（C 语言 MTProxy 时代）的关系

本仓库 2026-08-10 起为全新 Go 项目（GitHub created_at 与 git 历史双重印证）；旧 C 实现（同名的 telegramdesktop/tproxy-server，MTProxy 衍生）的历史与代码在当前仓库零残留。WEB proxy 是**概念上的接续与换代**：从"实现一个代理协议服务器"变为"把代理藏进普通网站"。

---

## 18. 本地验证记录（本次调研实测）

环境：Linux x86_64；Go **1.26.5**（go.mod 要求 ≥1.20，向前兼容良好）；GOPROXY 镜像拉取 gorilla/websocket v1.5.3 与 go.sum 一致。

```
$ go test -count=1 ./internal/frame/ ./internal/config/ ./internal/bridge/
ok  github.com/telegramdesktop/tproxy-server/internal/frame    0.003s
ok  github.com/telegramdesktop/tproxy-server/internal/config   0.009s
ok  github.com/telegramdesktop/tproxy-server/internal/bridge  0.183s

$ go test -count=1 ./internal/session/
ok  github.com/telegramdesktop/tproxy-server/internal/session  0.048s

$ go test -count=1 ./internal/server/
ok  github.com/telegramdesktop/tproxy-server/internal/server  1.302s   # 下载依赖后通过

$ go vet ./...
（零输出 = 零告警）
```

- 测试**未加 `-race`**（本机时间预算；HARDENING.md 的规范命令含 `-race`，社区与作者均以 race 构建为常态）；
- Caddy 对等套件（`TPROXY_CADDY_BIN`）与真 2.11.4 二进制不在本环境，按设计跳过；
- `go build ./cmd/tproxy-server` 可成功产出二进制（更新器同款 `-trimpath -ldflags='-s -w'` 参数即 install.sh 用法）；
- 复核桥接页渲染路径：`bridge.Render` 的五占位符替换 + JSON 编码注入验证逻辑如第 8.11 节所述，无注入面。

---

## 19. 结论与建议

### 19.1 结论

- **定性**：官方 PoC 后期、面向"在受限网络里把 Telegram 代理伪装成普通网站"的完整服务端半边；代码、协议、部署、测试四件套齐整且互相咬合，工程素养显著高于典型 PoC；
- **安全姿态**：以"探测对等性"为第一目标的多层防御，诚实地标注了不完美处（hop-by-hop 可观察、drain 期信号、同源信任、-S argv）；
- **生产就绪度**：单机小规模（默认 128 会话/4096 流/512 MiB 排队）可自托管试运行；规模化需按 PLAN 7.4 路线图改造 IPC 与管线；多租户运营需补管理面板（issue #14 方向）；
- **主要障碍**：无 LICENSE；umask 安装缺陷待修；t.me/webproxy 路由未注册（分享链接依赖客户端直开）。

### 19.2 给潜在使用者的建议

1. **先解决许可**：在 Telegram 明确授权前，仅作研究/自用评估；
2. 安装时**显式 `--secret` 之外的交互输入**（避免进程列表）；或先打上 #9/#12 补丁再装；
3. 用 `--site-upstream` 接你自己的真实网站（这是作者推荐路径，探测面最小）；
4. 移动端前台限制接受后再选载波：交互优先 `websocket`（握手省）、隔离优先 `websocket-lanes`（注意 macOS 串行化）；
5. 保留 `token.key` 备份（丢了=令牌来源证明丢失）；迁移期 drop-in 用完即删；
6. 监控三件套：`/healthz`、`/readyz`、`/metrics` 的 `tproxy_limit_hits_total`（限额命中是最早的容量告警）；`journalctl -u tproxy-server -u mtproxy -u caddy` 看事件类日志。

### 19.3 给后续开发的建议（若 fork 演进）

- 补 LICENSE 与 CI（race+Caddy 套件进管线）；
- 修 umask 族（PR #9/#12 已有方案）；
- PLAN §14 非目标列表与 §8 超时数字同步修订；
- `https` 模式上行管线化（PLAN 7.4 第 3 项）是吞吐性价比最高的下一步；
- 移动端 IPC 二进制化（ANDRIOD.md/IOS.md 都点名的 base64 复制成本）。

---

## 20. 附录：常量速查表 / 术语表 / 复现命令

### 20.1 线缆与协议常量速查

| 常量 | 值 | 出处 |
|---|---|---|
| 帧头 | 8 字节（u8 type + u24 stream + u32 len） | frame.go |
| 帧类型 | 01 OPEN / 02 DATA / 03 CLOSE / 04 WINDOW / 05 PING / 06 PONG / 10 HELLO / 11 WELCOME / 1F BYE | frame.go |
| HELLO 载荷 | 单字节 `01` | frame.go ParseHello |
| 最大帧载荷 | 1 MiB（1048576） | frame.go |
| 每批最大帧数 | 4096 | frame.go |
| 初始流窗口 | 4 MiB（4194304） | frame.go |
| DATA 块 | 64 KiB | frame.go |
| 流 id 域 | 1..0xFFFFFF（24 位） | frame.go |
| 墓碑容量 | 4096（每会话）/ 客户端 closedLanes 同值 | config/session/JS |
| capability | HMAC-SHA256(secret, "tdesktop-web-proxy-bridge-v1\n"+host) 的 base64url(43 字符) | config.go |
| capability 上下文 | `tdesktop-web-proxy-bridge-v1\n`（冻结 v1） | config.go |
| 令牌 | 16B nonce + 16B 截断 HMAC；域分离 `tproxy-server-token-v1\0`+kind(1=bootstrap,2=session) | token.go |
| 令牌编码 | 43 字符规范无填充 base64url | token.go |
| WS 子协议 | `tproxy-v1.<token>` / `tproxy-lane-v1.<token>.<lane>` | server.go |
| API 路径 | `/api/v1/session` `/api/v1/up` `/api/v1/down` `/api/v1/ws` | server.go |
| 控制头 | `X-Session-Token` `X-Down-Cursor` `X-Carrier-Mode` `X-Up-Seq` `X-Up-Ack` `X-Lane-ID` `X-Lane-Closed` | server.go |
| create 体上限 | 64 字节 | server.go |
| 体读期限 | 30 秒（认证后） | server.go |
| WS 空闲 | 2×long_poll（默认 50 秒） | server.go |
| WS 写期限 | 30 秒 | server.go |
| JS 队列上限 | 32 MiB/16384 项（全局）、8 MiB/1024 项（每 lane） | page.go |
| JS 重试 | 网络错 9 次；503 走 90 秒预算+Retry-After；退避 250ms→5s（+25% 抖动） | page.go |
| 固定 MTProxy | commit `f36d8af769ffaeac36978d38c2c0f6d1104c2137` | install-mtproxy.sh |
| Caddy | 2.11.4（sha512 pin） | install.sh |
| Go | ≥1.20（安装器拉 1.26.5，sha256 pin） | go.mod/install.sh |

### 20.2 术语表

- **carrier（载波）**：承载多路复用会话的传输形态（4 种模式）；
- **lane（车道）**：lane 模式中与一条逻辑流绑定的独立请求对/WS；
- **bootstrap token**：桥接页换会话令牌的一次性（2 分钟）能力凭证；
- **capability**：主机名+secret 派生的桥接页选择凭证（HMAC）；
- **墓碑（tombstone）**：已关闭流 id 的近期记忆，用于幂等与迟到帧容忍；
- **newest-poll-wins**：并发下行轮询时最新者接管、旧者无害返回的语义；
- **探测对等性（probing parity）**：无凭据请求在任何路径都走同一公共处理器的性质；
- **控制保留（control reserve）**：pending 预算中为 WINDOW/CLOSE 等控制帧预留的不可被 DATA 占用的份额。

### 20.3 复现命令

```bash
git clone https://github.com/telegramdesktop/tproxy-server.git
cd tproxy-server

# 测试与静态检查（HARDEN.md 规范命令集）
go test ./...
go test -race ./...                      # race 构建
go vet ./...
TPROXY_CADDY_BIN=/path/to/caddy-2.11.4 go test -race ./...   # 可选真 Caddy 套件
bash -n deploy/install.sh deploy/update-relay.sh deploy/ensure-token-key.sh

# 构建
go build -trimpath -ldflags='-s -w' -o tproxy-server ./cmd/tproxy-server

# 配置预检
./tproxy-server -config config.json -check

# 部署（见 README §4）
sudo ./deploy/install.sh --hostname proxy.example.com --email you@example.com \
  --site-dir ../my-site        # 或 --site-upstream http://127.0.0.1:3000
```

---

*本报告基于 2026-09-06 仓库快照（`f7a6acc`）独立完成；全部文件、提交、issue/PR 均逐一核对，测试实测记录见第 18 节。*
