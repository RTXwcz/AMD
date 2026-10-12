# ez-am-downloader Code Wiki

> 本文档是对 `d:\amd` 仓库的结构化代码百科，涵盖整体架构、模块职责、关键类与函数、依赖关系及运行方式。
> 仓库定位：在 Windows + WSL2 + Docker 之上，围绕两个上游项目（[zhaarey/apple-music-downloader](https://github.com/zhaarey/apple-music-downloader) 与 [WorldObservationLog/wrapper](https://github.com/WorldObservationLog/wrapper)）构建的一层"操作层"，用于个人合法备份自己已授权访问的 Apple Music 内容。

---

## 1. 项目整体架构

### 1.1 分层视角

```text
┌──────────────────────────────────────────────────────────────────┐
│  Windows 侧（d:\amd）                                            │
│  ┌──────────────────────┐    ┌────────────────────────────────┐  │
│  │ deploy/ez-am-deploy  │    │ downloader/ez-am-downloader    │  │
│  │ (Go TUI: 首次部署 +  │    │ (Go TUI: 解析链接 → 目录查询 → │  │
│  │  凭据采集 + 2FA)     │    │  曲目选择 → 分组下载)          │  │
│  └──────────┬───────────┘    └──────────────┬─────────────────┘  │
│             │ wsl.exe                       │ wsl.exe             │
└─────────────┼───────────────────────────────┼────────────────────┘
              │                               │
              v                               v
┌──────────────────────────────────────────────────────────────────┐
│  WSL2 侧（~/amd，持久状态真正所在）                              │
│  ┌────────────┐  ┌────────────┐  ┌────────────┐  ┌────────────┐ │
│  │ setup.sh   │  │ login.sh   │  │ dl.sh      │  │ compose.   │ │
│  │ 部署引擎   │  │ wrapper    │  │ 一次性下载 │  │ yaml(参考) │ │
│  │            │  │ 生命周期   │  │ 引擎       │  │            │ │
│  └─────┬──────┘  └─────┬──────┘  └─────┬──────┘  └────────────┘ │
│        │               │               │                          │
│        │       ┌───────┴────────┐      │                          │
│        │       | .env / config/ │      │                          │
│        │       | wrapper-data/  │      │                          │
│        │       | downloads/     │      │                          │
│        │       └────────────────┘      │                          │
└────────┼───────────────────────────────┼──────────────────────────┘
         │                               │
         v                               v
┌──────────────────────────────────────────────────────────────────┐
│  Docker Desktop（WSL Integration）                               │
│  ┌────────────────────────────┐  ┌─────────────────────────────┐ │
│  │ amd-wrapper-login          │  │ amd-downloader-<pid>-<ts>   │ │
│  │ (local/wrapper:latest)     │  │ (ghcr.io/zhaarey/...        │ │
│  │ 长驻：10020/20020/30020    │  │  pinned by digest)          │ │
│  │ loopback only              │  │ --network container:login   │ │
│  │                            │  │ 一次性，--rm                │ │
│  └────────────────────────────┘  └─────────────────────────────┘ │
└──────────────────────────────────────────────────────────────────┘
```

### 1.2 关键设计原则

1. **状态分离**：源码与构建产物在 Windows 检出目录；持久状态（凭据、会话、下载）只存在 WSL 的 `~/amd`。
2. **不绕过账号安全**：项目不绕过 Apple 账号安全机制；需 Apple Music 订阅、合法 `media-user-token`，并遵守 DRM 与当地法律。
3. **登录与下载解耦**：`dl.sh` 永不创建未登录的 wrapper；它先调用 `login.sh status`，再在 wrapper 的网络命名空间内运行一次性下载容器。
4. **端口仅回环**：wrapper 三个端口 `10020/20020/30020` 只绑定 `127.0.0.1`。
5. **凭据零泄露**：密码 / token / 2FA 不进入 shell 源码、不进入日志；通过 `WSLENV` 的 `/u` 标志或 `docker exec` stdin 传递；输出统一过 `redact` 过滤。
6. **镜像按 digest 锁定**：下载器镜像使用 SHA256 固定，wrapper 由本地构建。

### 1.3 顶层目录

| 路径 | 角色 |
| --- | --- |
| `deploy/` | 部署 TUI（Go）：采集凭据 → 运行 setup.sh → 处理 2FA |
| `downloader/` | 下载 TUI（Go）：链接解析、目录查询、曲目选择、分组下载 |
| `tests/` | Shell 回归测试（dl.sh 参数、挂载校验、媒体校验） |
| `docs/superpowers/` | 设计与计划文档（specs / plans） |
| `setup.sh` | WSL 内一次性部署引擎 |
| `login.sh` | wrapper 生命周期管理（start / submit / status / interactive） |
| `dl.sh` | 一次性下载引擎（容器编排 + 输出脱敏） |
| `sync_existing_wsl.sh` | 受保护的运行时文件同步器（root、不动凭据） |
| `compose.yaml` | wrapper 的参考 compose（挂载、回环端口、容器名） |
| `README.md` | 用户文档 |
| `.gitattributes` / `.gitignore` | Git 元数据 |

---

## 2. 主要模块职责

### 2.1 `deploy/` —— 部署 TUI（模块名 `ez-am-deploy`）

- **入口**：`deploy/main.go` → `main()` 启动 bubbletea 程序。
- **职责**：在 Windows 上引导首次部署；以 TUI 采集 Apple ID、密码、storefront、`media-user-token`；通过 `wsl.exe` 调用 `setup.sh` 与 `login.sh start`；若返回 `STATE=NEED_2FA` 则进入 2FA 阶段，调用 `login.sh submit <code>` 完成认证。
- **状态机**：`sWelcome → sCreds → sToken → sDeploy → (s2FA) → sDone`。
- **关键安全点**：
  - 凭据通过 `WSLENV` 的 `EZ_USER/EZ_PASS/EZ_TOKEN/EZ_STOREFRONT` 传递，**不**进入 shell 源字符串；
  - 所有日志经 `redactSecrets` / `redactDiagnosticText` 脱敏；
  - Ctrl+C 取消时调用 `clearSecrets()` 清空输入框，并取消活动子进程。
- **产物**：`dist/ez-am-deploy.exe`（可选便利前端）。

### 2.2 `downloader/` —— 下载 TUI（模块名 `ez-am-downloader`）

入口 `downloader/main.go`，按职责拆分为四个文件：

| 文件 | 职责 |
| --- | --- |
| `main.go` | bubbletea Model/Update/View，状态机驱动下载流程 |
| `catalog.go` | Apple Music 公共目录 API 客户端（无鉴权） |
| `selection.go` | 曲目 / 音质 profile 选择，按 profile 分组生成下载任务 |
| `stream.go` | 子进程（wsl.exe）输出按行流式回传 TUI |

#### 2.2.1 状态机

```text
sLink ──enter──> sMetadata ──(album)──> sTracks ──enter──> sDownload ──> sDone
   │                                  │
   └──enter──> sFormat ──enter─────────┘
```

- `sLink`：粘贴 Apple Music 链接，规范化为 `https://music.apple.com/...`。
- `sMetadata`：异步拉取专辑目录（公共 token + `v1/catalog/{store}/albums/{id}`）。
- `sTracks`：曲目列表，默认全选；支持 ↑↓/Space/←→/A/G 等键位。
- `sFormat`：目录失败时的整链下载格式选择回退路径。
- `sDownload`：按 profile 分组依次启动下载容器，流式输出 + 可取消。
- `sDone`：成功 / 失败 / 取消 终态。

#### 2.2.2 音质 profile

`selection.go` 定义 5 个 profile 常量：`alac-48` / `alac-96` / `alac-192` / `atmos` / `aac`。曲目可用 profile 由其 `AudioTraits`（`lossless` / `hi-res-lossless` / `atmos` / `lossy-stereo`）决定。`buildDownloadJobs` 将选中的曲目按 profile 确定性分组，每组生成一个 `downloadJob`，对应一次容器调用。

### 2.3 Shell 运行时

#### 2.3.1 `setup.sh` —— 部署引擎

- 在 WSL 内运行；要求 Docker、Compose 插件、`dl.sh`/`login.sh`/`compose.yaml` 同目录。
- 创建 `$AMD_BASE`（默认 `~/amd`）下的 `wrapper-bin`、`wrapper-data`、`downloads`、`config` 目录，`config` 目录 `0700`。
- 安装三个运行时文件并设权限（`dl.sh`/`login.sh` `0755`、`compose.yaml` `0644`）。
- 拉取按 digest 锁定的下载器镜像；若 `local/wrapper:latest` 不存在，则下载 wrapper release zip、解压、本地 `docker build`。
- 交互式或通过 `EZ_USER/EZ_PASS/EZ_TOKEN/EZ_STOREFRONT` 环境变量采集凭据；拒绝换行；写 `.env`（`0600`）与 `config/config.yaml`（`0600`），写入前对旧文件做时间戳备份。

#### 2.3.2 `login.sh` —— wrapper 生命周期

子命令：`start | submit <code> | status | interactive`（默认 `interactive`）。

- `status`：校验 `wrapper-data/.../kvs.sqlitedb` 中存在 `StoreFrontIdentifier` 与 `Account.XToken:*`；校验容器存在、运行中、三端口监听。
- `start`：先尝试恢复已有会话；失败则加载 `.env` 启动 `amd-wrapper-auth` 临时容器，等待 "Waiting for input" 输出，输出 `STATE=NEED_2FA` 标记，**保留** auth 容器等 `submit`。
- `submit`：将 6 位码通过 `docker exec -i ... tee 2fa.txt` 写入（**绝不**插值进 shell）；等待端口与会话生效后提升为长驻 `amd-wrapper-login`。
- `interactive`：组合 status → start → 提示输入码 → submit。
- 2FA 容器退出码 / 信号清理通过 `arm_auth_cleanup` / `disarm_auth_cleanup` 受控。

#### 2.3.3 `dl.sh` —— 一次性下载引擎

调用约定：`dl.sh <apple-music-url> [profile] [track-id ...]`。

- 校验 URL、profile、track-id（纯数字）；按 profile 展开为 `--alac-max` / `--atmos` / `--aac` 等参数。
- 若传入 track-id，附加 `--song` 并为每个 ID 拼装 `?i=<id>` 的独立 URL（保留原 query 与 fragment，且**替换而非复制**已有 `i=`）。
- 调 `login.sh status` 作为唯一的 wrapper 就绪契约；不通过则拒绝启动下载容器。
- 启动容器：`docker run --rm --name amd-downloader-... --network container:amd-wrapper-login`，挂载 `downloads` 与 `config.yaml:ro`。
- 输出阶段标记（`[stage] validating|wrapper|container|download|complete`）供 TUI 解析；输出经 `redact_downloader_output` 脱敏 `MediaUserToken`/`AuthorizationToken`/`media-user-token`。
- trap EXIT/INT/TERM/HUP：仅当下载已启动才 `docker rm -f` 命名容器；信号路径返回 130。

#### 2.3.4 `sync_existing_wsl.sh` —— 受保护同步器

- **必须以 WSL root 运行**，且只接受硬编码的 `/home/rtxwcz/amd` 与 `/mnt/d/amd` 路径，否则拒绝执行。
- 在同步前后分别计算 `.env`、`config.yaml`（剔除 `exit-on-error` 行）、`downloads`、`wrapper-data` 的签名；同步后断言签名不变。
- 原子安装四个运行时文件（`install -o OWNER -g OWNER -m MODE ... && mv -fT`），仅将 `exit-on-error` 强制设为 `true`。
- 收紧 `wrapper-data` 权限（目录 `2750`、文件 `0640`，属主 `root:OWNER`）。
- 输出 `SYNC_OK` 及文件数 / 字节数审计。

### 2.4 `compose.yaml`

wrapper 容器的**参考定义**（实际生命周期由 `login.sh` 管理）：服务 `wrapper` 使用 `local/wrapper:latest`，容器名 `amd-wrapper-login`，挂载 `wrapper-data` 与只读 `config.yaml`，三端口仅绑 `127.0.0.1`，`privileged`、`restart: unless-stopped`。

### 2.5 `docs/superpowers/`

设计 / 计划文档目录：
- `specs/2026-07-22-unified-ez-am-design.md`：下一代"统一 ez-am 产品"设计（单一 `ez-am.exe`、嵌入运行时、XDG 布局、Debian/Arch 包、控制域 / 媒体域拆分、WSL 状态机、Docker 后端策略等）。
- `plans/2026-07-22-unified-ez-am-foundation.md`、`2026-07-20-current-stack-repair.md`、`2026-07-20-track-quality-batch-download.md`：实施计划。

### 2.6 `tests/`

Shell 回归测试集：
- `dl_args_test.sh`：在沙箱里用伪 `docker` 校验 `dl.sh` 的参数构造、URL 拼装、脱敏行为。
- `shell_regression_test.sh`、`check_runtime_mounts.sh`、`verify_album_media.sh`、`verify_existing_media.sh`：运行时挂载与媒体校验脚本。

---

## 3. 关键类与函数说明

### 3.1 `downloader/catalog.go`

| 符号 | 说明 |
| --- | --- |
| `AppleMusicURL` 结构体 | 公开链接解析结果：`Raw/Storefront/Kind/ID/SongID/Language/Query`。`AppleMusicLink` 是其别名。 |
| `TrackMetadata` / `AlbumMetadata` | 从 Apple 目录响应中抽取的强类型子集（**不含**任何账号 / 媒体凭据）。 |
| `CatalogClient` | 公共目录客户端；导出 `HTTPClient/MusicBaseURL/APIBaseURL/UserAgent` 便于测试指向 httptest。 |
| `NewCatalogClient(*http.Client)` | 构造默认客户端（`music.apple.com` + `amp-api.music.apple.com`）。 |
| `ParseAppleMusicURL(raw)` | 严格解析：仅 `https`、仅 `music.apple.com`/`www.music.apple.com`、无 userinfo/port；`album`/`song` 类型；`?i=` 至多一个且为数字。 |
| `FetchPublicStorefrontToken(ctx, storefront)` | 从 storefront HTML 与 hashed index JS 资产中正则匹配公开 JWT（非媒体 token）。 |
| `FetchAlbum(ctx, link, token)` | 调 `v1/catalog/{sf}/albums/{id}?include=tracks,artists&extend=...`；**带分页循环检测**与同源校验，逐页 `next` 累积曲目。 |
| `FetchAlbumByURL` / `FetchAlbumCatalog` | 便捷封装：解析 + 取 token + FetchAlbum。 |
| `withDefaults / getText / getJSON` | 内部 HTTP 工具：8 MiB 上限、`Bearer` 头、`Accept` 设置。 |
| `resolveBase / resolveURL / sameOrigin / effectivePort / appendQuery` | URL 解析与同源判定工具。 |
| `catalogAlbumEnvelope` 等内部结构 | Apple 目录 JSON 信封的 Go 映射。 |
| `tracksFrom([]catalogTrackResource)` | 将原始资源切片映射为 `[]TrackMetadata`。 |

### 3.2 `downloader/selection.go`

| 符号 | 说明 |
| --- | --- |
| profile 常量 | `profileALAC48/96/192`、`profileAtmos`、`profileAAC`；`profileOrder` 为确定性顺序。 |
| `trackChoice` | 单曲 UI 状态：`track + selected + profile`。 |
| `downloadJob` | 一组同 profile 的下载任务：`profile + trackIDs`。 |
| progress 常量 | `progressValidating/wrapper/container/download/finalize/complete`。 |
| `hasAudioTrait` | 在 `TrackMetadata.AudioTraits` 中查找指定 trait（不区分大小写）。 |
| `availableProfiles(track)` | 依据 trait 推导该曲可用的 profile 列表（lossless→48/96/192，hi-res→96/192，atmos→atmos，恒有 aac）。 |
| `defaultProfile(track)` | 默认 profile：hi-res→192、lossless→48、其余→aac。 |
| `cycleProfile(choice, dir)` | 在可用 profile 中循环切换（用于 ←→ 键）。 |
| `buildDownloadJobs(choices)` | 按 `profileOrder` 把选中曲目确定性分组为 `[]downloadJob`。 |
| `stageFromLine(line)` | 把 `dl.sh` 的输出行映射为 progress 阶段（显式 `[stage]` 优先，否则关键字回退）。 |
| `appendRecentLog(cur, next, maxLines, maxBytes)` | 有界日志缓冲：限行 + 限字节，截断时从行首对齐。 |

### 3.3 `downloader/stream.go`

| 符号 | 说明 |
| --- | --- |
| `processLineMsg` / `processDoneMsg` | 携带 `runID/jobIndex` 的 TUI 消息，用于过时事件过滤。 |
| `startProcessEvents(cmd, runID, jobIndex)` | 启动子进程并返回事件通道 + 首条消息 Cmd。 |
| `waitProcessEvent(events)` | 阻塞等待下一条事件，构成 bubbletea 事件循环。 |
| `produceProcessEvents` | goroutine：把 stdout/stderr 写入 `eventLineWriter`，结束时发 `processDoneMsg`。 |
| `eventLineWriter` | 线程安全按行缓冲器；`Write` 切行即发，`Flush` 发出剩余尾部。 |
| `splitOutputLines(data, atEOF)` | 兼容 `\n` 与 `\r` 的行扫描函数。 |

### 3.4 `downloader/main.go` 关键点

| 符号 | 说明 |
| --- | --- |
| `model` 结构体 | TUI 全量状态：stage、url 输入、album/tracks、jobs 队列、runID/jobIndex、清理标志位、进度阶段、窗口尺寸等。 |
| `formats` | 整链下载回退用的 3 选项（ALAC / Atmos / AAC）。 |
| `Update(msg)` | 中央事件分发：键位、窗口大小、目录结果、进程行/完成、取消 tick。 |
| `handleEnter()` | 各 stage 的回车动作：解析链接 → 触发目录拉取 → 启动下载。 |
| `fetchAlbumMetadataCmd` | 45 秒超时异步拉取专辑目录。 |
| `canonicalAppleMusicURL` | 严格规范化（https、`music.apple.com`、无端口）。 |
| `startDownloads(jobs)` / `beginCurrentJob()` | 初始化 jobs 队列并按 `jobIndex` 逐组启动容器。 |
| `newDownloadCommandContextWithContainerTracks` | 构造 `wsl.exe` 调用：`exec "$HOME/amd/dl.sh" "$@"`，通过 `EZ_DOWNLOAD_CONTAINER`（WSLENV `/u`）传容器名。 |
| `newCancelDownloadCommandContext` / `newFinalCancelDownloadCommandContext` | 取消用的 `docker rm -f` 命令；final 版本重试 8 次。 |
| `validDownloadContainerName` | 严格白名单：前缀 `amd-downloader-`、长度 ≤ 96、字符集 `[a-z0-9_.-]`、首字符为字母/数字。 |
| `cancelDownloadCommandForPhase` | 取消任务（普通/final），返回 `cancelTick`；带 10 秒超时。 |
| `programExitCode` | 取消 → 130；失败 → 1；否则 0。 |
| `redact*`、`mergedWSLEnv`、`setEnvironmentValue` 等 | 与 `deploy/main.go` 共享思路的安全/环境工具。 |

### 3.5 `deploy/main.go` 关键点

| 符号 | 说明 |
| --- | --- |
| `model` 结构体 | 部署 TUI 状态：stage、5 个 textinput（appleid/password/storefront/2fa/token）、cancel/submitting 等。 |
| `stage` 常量 | `sWelcome/sCreds/sToken/sDeploy/s2FA/sDone`。 |
| `Update` / `handleEnter` | 状态机：欢迎 → 凭据 → token → 部署 →（2FA）→ 完成。 |
| `runDeploymentWithCredentialsContext` | 串行：setup → login start → login status；遇 `STATE=NEED_2FA` 进入 2FA 分支。 |
| `run2FAWithContext` | 调 `login.sh submit <code>` 后再 `login.sh status`。 |
| `newSetupCommandContext` | 通过 `WSLENV` 的 `EZ_USER/EZ_PASS/EZ_TOKEN/EZ_STOREFRONT` 传参，**不**写入 shell 源。 |
| `locateSetupScript` | 在可执行文件同目录或父目录查找 `setup.sh`，拒绝当前工作目录的 `setup.sh`。 |
| `redactSecrets` / `redactDiagnosticText` | 日志脱敏：显式 secrets 字面量替换 + 正则匹配 `password/token=...`。 |
| `clearSecrets` | 取消时清空 password/2fa/token 输入框。 |

### 3.6 Shell 函数要点

| 文件 | 关键函数 |
| --- | --- |
| `login.sh` | `require_docker`、`container_exists/running`、`port_listening`、`wrapper_ports_ready`（解析 `/proc/net/tcp` 的 10020/20020/30020 即 0x2724/0x4E34/0x7544）、`session_db_valid`（python3 只读 sqlite）、`wait_for_ports`、`status_impl`、`load_credentials`、`start_long_lived`、`start_existing_login`、`start_authentication`、`promote_authenticated_session`、`submit_code`、`start_mode`、`interactive_mode`。 |
| `dl.sh` | URL query 拆分与 `i=` 去重、`append_track_id`、`cleanup_download`、`handle_download_signal`、`redact_downloader_output`。 |
| `setup.sh` | `is_wsl`、`docker_help`、`install_runtime_file`、`yaml_quote`、`backup_existing`、`reject_line_breaks`。 |
| `sync_existing_wsl.sh` | `file_set_signature`、`config_without_exit_signature`、原子 `install -o -g -m ... && mv -fT` 安装、签名前后断言。 |

---

## 4. 依赖关系

### 4.1 Go 模块依赖

`deploy/go.mod` 与 `downloader/go.mod` 同构，均 `go 1.22`，直接依赖：

- `github.com/charmbracelet/bubbles v0.18.0` —— textinput 等气泡组件。
- `github.com/charmbracelet/bubbletea v0.27.1` —— Elm 架构 TUI 框架。
- `github.com/charmbracelet/lipgloss v0.13.0` —— 样式与布局。

间接依赖（节选）：`mattn/go-runewidth`、`muesli/termenv`、`muesli/cancelreader`、`charmbracelet/x/{ansi,input,term,windows}`、`erikgeiser/coninput`、`rivo/uniseg`、`golang.org/x/{sync,sys,text}` 等。

### 4.2 外部镜像依赖

| 镜像 | 用途 | 来源 |
| --- | --- | --- |
| `ghcr.io/zhaarey/apple-music-downloader@sha256:16045b4d...a3cb7e4d` | 一次性下载容器（按 digest 锁定） | 上游 zhaarey 项目 |
| `local/wrapper:latest` | wrapper 会话容器（本地构建） | 从 `WorldObservationLog/wrapper` release zip 本地 `docker build` |

### 4.3 与上游项目的关系

- **WorldObservationLog/wrapper**：提供 Apple Music Android 协议的 wrapper 二进制，监听三端口（decrypt-m3u8 / get-m3u8 / get-m3u8-from-device）。本项目只负责其部署、登录、生命周期。
- **zhaarey/apple-music-downloader**：消费 wrapper 端口、下载并解密封装媒体。本项目用其镜像作为一次性下载容器，并通过 `--song`、`--alac-max`、`--atmos`、`--aac` 等参数驱动。

### 4.4 运行时外部依赖

- Windows 10/11 + WSL2 + Ubuntu（或兼容）发行版。
- Docker Desktop 且对所选发行版启用 WSL Integration；或（设计文档规划中）WSL 内 Docker Engine。
- `curl`、`unzip` 或 `python3`（用于 setup.sh 拉 wrapper zip 与解压）。
- `python3`（login.sh 用只读 sqlite 校验会话有效性）。
- 网络：安装期访问 GitHub 与 `ghcr.io`；运行期访问 `music.apple.com` / `amp-api.music.apple.com` / wrapper 端口。

### 4.5 模块间调用关系

```text
ez-am-deploy.exe ──wsl.exe──> setup.sh ──> docker pull / build
                └──wsl.exe──> login.sh start ──> STATE=NEED_2FA
                └──wsl.exe──> login.sh submit <code>

ez-am-downloader.exe ──HTTPS──> Apple Music Catalog (catalog.go)
                    └──wsl.exe──> dl.sh ──> login.sh status
                                          └──> docker run --network container:amd-wrapper-login
                                                    └──> apple-music-downloader 镜像
```

---

## 5. 项目运行方式

### 5.1 构建

Go 构建需关闭 VCS stamping（本部署目录可能在没有有效 Git worktree 的情况下分发）：

```bash
cd deploy
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -buildvcs=false -o ../dist/ez-am-deploy.exe .

cd ../downloader
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -buildvcs=false -o ../dist/ez-am-downloader.exe .
```

验证：

```bash
go test -count=1 ./...
go vet ./...
```

### 5.2 全新安装

从 Windows 检出目录运行 setup 脚本（脚本依赖同目录的 `dl.sh`/`login.sh`/`compose.yaml`）：

```powershell
wsl.exe -d Ubuntu --exec bash /mnt/d/amd/setup.sh
```

`setup.sh` 会：检查 Docker/Compose、拉取下载器镜像、构建 wrapper 镜像、采集凭据、写 `~/amd/.env` 与 `~/amd/config/config.yaml`（旧文件时间戳备份）。**不**安装第二个 Docker daemon、**不**升级 WSL 发行版。

### 5.3 已有部署的同步升级

仅同步四个运行时文件、不动凭据 / 会话 / 媒体，需以 WSL root 运行：

```powershell
wsl.exe -d Ubuntu -u root --exec bash /mnt/d/amd/sync_existing_wsl.sh
```

### 5.4 登录与恢复

在可见 WSL 终端中执行（2FA 码不经过 chat/log）：

```bash
bash ~/amd/login.sh interactive   # 推荐：status→start→提示输入码→submit
bash ~/amd/login.sh status        # 健康检查
bash ~/amd/login.sh start         # 恢复缓存会话或发起 2FA
bash ~/amd/login.sh submit 123456 # 提交 6 位码
```

`start` 会留下活动 auth 容器等待 `submit`；长驻容器名为 `amd-wrapper-login`；旧诊断容器不会被自动清理。

### 5.5 下载

TUI：运行 `dist/ez-am-downloader.exe`，粘贴专辑/单曲/播放列表链接，按提示选择曲目与音质后下载。

Shell：

```bash
bash ~/amd/dl.sh 'https://music.apple.com/us/album/example/123' alac-48

# 选定曲目：URL 后接 profile，再接若干数字 track-id
bash ~/amd/dl.sh 'https://music.apple.com/us/album/example/123?l=en-US' alac-48 456 789
```

支持的 profile：

| 参数 | 含义 |
| --- | --- |
| `alac-48` | ALAC 上限 48 kHz |
| `alac-96` | ALAC 上限 96 kHz |
| `alac-192` | ALAC 上限 192 kHz |
| `atmos` | Dolby Atmos（源提供时） |
| `aac` | AAC 256 |
| `alac` / `hires` | `alac-192` 兼容别名 |

下载产物写入 `~/amd/downloads`（如 `ALAC/`、`Atmos/`、`AAC/`），setup 与 login 永不删除已有媒体。Windows 访问路径：`\\wsl$\<distro>\home\<user>\amd\downloads`。

### 5.6 取消与退出码

- TUI 中 `Ctrl+C` 在下载阶段：取消 WSL 子进程 → `docker rm -f` 命名容器 → 两条清理路径都完成后才以 130 退出。
- 非法 URL、wrapper 会话缺失：在启动下载容器之前即失败。
- 下载容器返回真实退出码；`dl.sh` 透传。TUI 程序：取消 130、失败 1、成功 0。

### 5.7 TUI 键位（下载器曲目选择）

| 键 | 动作 |
| --- | --- |
| ↑/↓ 或 k/j | 移动光标 |
| Space | 选择/取消当前曲 |
| ←/→ 或 h/l | 循环当前曲可用音质 |
| A | 全选/全不选 |
| G | 把当前曲音质应用到所有兼容的已选曲 |
| Enter | 开始下载 |
| Ctrl+C | 退出 / 取消下载 |

---

## 6. 安全与边界

- `~/amd/.env`（Apple ID + 密码）、`~/amd/config/config.yaml`（media token + 下载设置）、`~/amd/wrapper-data`（持久会话 sqlite）为敏感数据；`.env` 与 `config.yaml` 保持 `0600`、config 目录 `0700`。
- 禁止把 Apple 密码、media token、会话数据库、2FA 码贴进 issue/chat/build log。
- 历史部署笔记可能含凭据，分享/提交前需审查并脱敏；如曾明文记录则轮换密码与 media token。
- 仓库 `.gitignore` 忽略本地备份与部署日志，但忽略规则不会删除已提交文件；发布前需检查历史。
- `sync_existing_wsl.sh` 用签名断言保护 `.env`/`config`/`downloads`/`wrapper-data` 不被改动，且只接受硬编码路径。
- 容器名严格白名单（`amd-downloader-*`），避免被恶意构造的名称注入到 `docker rm -f` 命令。
- 目录分页带循环检测与同源校验，防止恶意分页 URL 逃逸。

---

## 7. 测试与质量

- **Go 单元测试**：
  - `downloader/catalog_test.go`：URL 解析边界、目录响应解析、分页等。
  - `downloader/feature_test.go`：profile 推导、曲目选择键位、分组任务、阶段解析、日志有界、进程事件过滤、CJK 小窗口排版、输出行切分等。
  - `downloader/catalog_integration_test.go`、`downloader/main_test.go`：集成与 TUI 主流程。
  - `deploy/main_test.go`：部署 TUI 凭据采集、2FA 输入掩码、WSL 参数构造、setup.sh 定位等。
- **Shell 回归**：`tests/dl_args_test.sh` 用沙箱伪 `docker` 校验 `dl.sh` 参数与脱敏；`tests/shell_regression_test.sh` 等校验运行时挂载与媒体。
- 验证命令：`go test -count=1 ./...` 与 `go vet ./...`。

---

## 8. 演进方向（来自 `docs/superpowers/specs`）

`2026-07-22-unified-ez-am-design.md` 定义了下一代统一产品：
- 单一 `ez-am.exe`（Windows）+ Debian `.deb` + Arch `.pkg.tar.zst`，嵌入运行时脚本、默认配置、schema 版本与签名资产清单。
- 内部拆为 **Control 域**（平台/特权/WSL/Docker/凭据/wrapper 生命周期/诊断）与 **Media 域**（URL/目录/曲目/分组下载/进度取消/校验），媒体域以 `Control.EnsureReady(DownloadRequest)` 起始。
- Windows 启动状态机：探查 → 必要时 UAC 装 WSL/Ubuntu → 选定发行版 → 探测默认用户/home/架构 → Docker Desktop 优先、否则 WSL 内 Docker Engine → 安装/升级运行时 → 健康分类与修复 → 进入媒体流程。
- XDG 兼容布局（`$XDG_DATA_HOME/ez-am` 或 `~/.local/share/ez-am`），识别并保留 legacy `~/amd` 直到用户接受就地迁移。
- 首批架构 amd64/x86_64（arm64 待 wrapper 上游支持）。

相关计划见 `docs/superpowers/plans/` 下三份文档。

---

## 9. 文件索引（速查）

| 路径 | 行数级 | 一句话说明 |
| --- | --- | --- |
| [README.md](file:///d:/amd/README.md) | 中 | 用户文档：架构、前置条件、安装、登录、下载、安全、构建 |
| [compose.yaml](file:///d:/amd/compose.yaml) | 小 | wrapper 容器参考定义 |
| [setup.sh](file:///d:/amd/setup.sh) | 中 | WSL 内一次性部署引擎 |
| [login.sh](file:///d:/amd/login.sh) | 大 | wrapper 生命周期（status/start/submit/interactive） |
| [dl.sh](file:///d:/amd/dl.sh) | 中 | 一次性下载引擎（参数/容器编排/脱敏） |
| [sync_existing_wsl.sh](file:///d:/amd/sync_existing_wsl.sh) | 中 | 受保护运行时文件同步器（root，签名断言） |
| [deploy/main.go](file:///d:/amd/deploy/main.go) | 大 | 部署 TUI：凭据采集 + setup + 2FA |
| [deploy/main_test.go](file:///d:/amd/deploy/main_test.go) | 中 | 部署 TUI 测试 |
| [downloader/main.go](file:///d:/amd/downloader/main.go) | 大 | 下载 TUI：状态机 + 容器命令构造 + 视图 |
| [downloader/catalog.go](file:///d:/amd/downloader/catalog.go) | 大 | Apple Music 公共目录客户端 |
| [downloader/selection.go](file:///d:/amd/downloader/selection.go) | 中 | profile 推导 + 分组任务 + 阶段解析 |
| [downloader/stream.go](file:///d:/amd/downloader/stream.go) | 小 | 子进程输出流式回传 |
| [downloader/feature_test.go](file:///d:/amd/downloader/feature_test.go) | 大 | TUI 行为单元测试 |
| [downloader/catalog_test.go](file:///d:/amd/downloader/catalog_test.go) | 中 | 目录解析与查询测试 |
| [tests/dl_args_test.sh](file:///d:/amd/tests/dl_args_test.sh) | 中 | dl.sh 参数与脱敏沙箱测试 |
| [docs/superpowers/specs/2026-07-22-unified-ez-am-design.md](file:///d:/amd/docs/superpowers/specs/2026-07-22-unified-ez-am-design.md) | 大 | 下一代统一产品设计 |
