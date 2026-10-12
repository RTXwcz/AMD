package i18n

import "fmt"

// sprintf is a seam so the catalog stays free of formatting concerns.
func sprintf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

// catalog holds every user-facing string. Both languages must define the same
// keys; TestCatalogIsComplete enforces that.
var catalog = map[Lang]map[string]string{
	EN: {
		// doctor
		"doctor.header":                 "ez-am doctor",
		"doctor.no_runner":              "no command runner was provided",
		"doctor.wsl.title":              "WSL",
		"doctor.wsl.unavailable":        "wsl.exe is not available on this machine: %s",
		"doctor.wsl.available":          "wsl.exe is available",
		"doctor.distro.title":           "Distribution",
		"doctor.distro.ok":              "%s %s (%s) · %s",
		"doctor.distro.root_user":       "the default user of this distribution is root; configure a normal user first",
		"doctor.distro.unsupported":     "unsupported distribution family %q; supported: Debian/Ubuntu and Arch",
		"doctor.distro.none":            "no user distribution found (docker-desktop is not a user distribution)",
		"doctor.distro.choose":          "choose one of: %s",
		"doctor.distro.explicit":        "%s (explicit)",
		"doctor.distro.notfound":        "requested distribution %q not found; using %q",
		"doctor.probe.failed":           "the distribution probe failed: %s",
		"doctor.probe.exit":             "the distribution probe exited with code %d: %s",
		"doctor.docker.title":           "Docker",
		"doctor.docker.unavailable":     "the Docker daemon does not answer inside this distribution",
		"doctor.docker.server":          "server %s",
		"doctor.buildx.title":           "BuildKit",
		"doctor.buildx.missing":         "docker buildx is missing; needed once to build the wrapper image",
		"doctor.runtime.title":          "Runtime files",
		"doctor.runtime.missing":        "missing under %s: %s",
		"doctor.runtime.ok":             "installed under %s",
		"doctor.config.title":           "Credentials",
		"doctor.config.missing":         "the Apple account and media token are not configured yet",
		"doctor.config.ok":              "account and media token are stored",
		"doctor.images.title":           "Images",
		"doctor.images.missing":         "not pulled yet: %s (setup.sh fetches them)",
		"doctor.images.ok":              "downloader and wrapper images are present",
		"doctor.wrapper.title":          "Wrapper session",
		"doctor.wrapper.ok":             "listening on 10020/20020/30020",
		"doctor.wrapper.pending":        "an authentication request is already waiting for a code",
		"doctor.wrapper.unhealthy":      "the wrapper is not healthy",
		"doctor.downloads.title":        "Output directory",
		"doctor.downloads.unwritable":   "not writable yet: %s",
		"doctor.result":                 "Result: %s",
		"doctor.result.ready":           " — ready to download",
		"doctor.result.needs_attention": " — the issues above must be fixed first",
		"doctor.result.suggested":       "Suggested actions: %s",

		// control (the app shell)
		"control.distro_label":                 "  distribution: %s",
		"control.checking.title":               "Environment check",
		"control.checking.detail":              "probing WSL, Docker, runtime files, credentials, images and the wrapper session…",
		"control.checking.hint":                "q quits · L switches language",
		"control.report.title":                 "Doctor",
		"control.report.ready":                 "Environment is ready — starting the downloader",
		"control.report.needs":                 "Setup needed: %s",
		"control.report.hint":                  "ENTER fixes · R re-checks · L language · Q quits",
		"control.details.title":                "Details",
		"control.fixes.title":                  "Fix",
		"control.fixes.hint":                   "↑↓ moves · ENTER runs · R re-checks · Q quits",
		"control.action.install_runtime":       "Install the bundled runtime files",
		"control.action.collect_creds":         "Enter Apple ID, media-user-token and deploy",
		"control.action.login":                 "Log in to Apple (2FA)",
		"control.action.choose_distro":         "Choose a WSL distribution",
		"control.action.start_docker":          "Start Docker, then re-check",
		"control.action.start_docker_detail":   "Start Docker Desktop, or run `sudo service docker start` inside the distribution, then press R to re-run the doctor.",
		"control.action.install_buildx":        "Install the docker buildx plugin, then re-check",
		"control.action.install_buildx_detail": "Install the docker-buildx package, or put the docker-buildx binary into ~/.docker/cli-plugins/. Only needed once, to build the wrapper image.",
		"control.action.wsl_missing":           "Install WSL2, then re-check",
		"control.action.wsl_missing_detail":    "Run `wsl --install -d Ubuntu` in an elevated PowerShell, reboot when asked, then press R. Docker must also be reachable inside that distribution.",
		"control.action.rerun":                 "Re-run the doctor",
		"control.action.quit":                  "Quit",
		"control.distro.title":                 "Choose a WSL distribution",
		"control.distro.hint":                  "ENTER selects · ESC goes back",
		"control.creds.title":                  "Apple account",
		"control.creds.email":                  "Apple ID email",
		"control.creds.password":               "Apple ID password",
		"control.creds.storefront":             "Storefront (default us)",
		"control.creds.token":                  "media-user-token",
		"control.creds.hint":                   "TAB/↑↓ moves · ENTER continues · ESC cancels",
		"control.creds.required":               "Apple ID, password and media-user-token are all required.",
		"control.login.title":                  "Additional verification — 2FA",
		"control.login.sent":                   "Apple sent a 6-digit code to your trusted device.",
		"control.login.hint":                   "ENTER submits",
		"control.login.invalid":                "the code must be exactly six digits.",
		"control.log.installing":               "installing the bundled runtime…",
		"control.log.starting_auth":            "starting the Apple authentication request…",
		"control.log.auth_waiting":             "%s\n\nEnter the 6-digit code Apple sent you.",
		"control.log.deploying":                "writing configuration and preparing images (this can take minutes)…",
		"control.log.submitting":               "submitting the code and waiting for the wrapper to become healthy…",
		"control.log.working":                  "working…",
		"control.log.done_hint":                "Press ENTER or R to re-run the doctor · Q quits",
		"control.log.runtime_base":             "runtime base: %s",
		"control.log.runtime_installed":        "runtime files installed and verified.",
		"control.err.install_step":             "install step %q exited with code %d: %s",
		"control.err.login_start":              "login.sh start exited with code %d",
		"control.err.setup":                    "setup.sh exited with code %d",
		"control.err.submit":                   "login.sh submit exited with code %d",
		"control.err.distro_list":              "could not list distributions: %s",
		"control.err.layout_home":              "the distribution home directory is unknown",
		"control.err.runner":                   "a command runner is required",
		"control.err.report":                   "the environment check failed: %s",

		// media (the downloader TUI)
		"media.link_prompt":     "Paste an Apple Music album, song or playlist link:",
		"media.link_hint":       "ENTER continues  ·  CTRL+C quits",
		"media.metadata_title":  "Reading album tracks",
		"media.metadata_detail": "Fetching track names and available audio formats…",
		"media.format_title":    "Choose audio format",
		"media.format_hint":     "↑↓ selects  ·  ENTER downloads",
		"media.quality_current": "Current quality",
		"media.jobs_done":       "Completed %d/%d groups, %s group(s) failed\n\n",
		"media.download_failed": "Download failed",
		"media.exit_hint":       "ENTER or CTRL+C to exit",
		"media.done":            "✓ Download complete",
		"media.files_in":        "Files in: %s",
		"media.tracks_title":    "Choose tracks and quality",
		"media.tracks_selected": "\nSelected %d / %d\n\n",
		"media.tracks_window":   "  showing %d-%d / %d",
		"media.unavailable":     "unavailable",
		"media.available":       "available",
		"media.tracks_hint":     "↑↓ move · SPACE select · ←→ quality · A all/none · G apply to selected · ENTER download",
		"media.stopping":        "Stopping the download",
		"media.stopping_detail": "Stopping the WSL job and cleaning up the download container…",
		"media.group_progress":  "Group %d / %d  ·  %s",
		"media.awaiting_wsl":    "Waiting for WSL output…",
		"media.downloading":     "Downloading",
		"media.stop_hint":       "CTRL+C stops and cleans up the current job",
		"media.stage.validate":  "Validate arguments",
		"media.stage.wrapper":   "Check the wrapper session",
		"media.stage.container": "Start the download container",
		"media.stage.media":     "Download media",
		"media.stage.tag":       "Decrypt, package and write tags",
		"media.stage.done":      "Done",
		"media.stage.stopping":  "Stopping",
		"media.stage.prepare":   "Preparing the download",

		// command line
		"cli.usage": `ez-am — Apple Music lossless downloader for WSL2 + Docker

Usage:
  ez-am                 check the environment, fix what is missing, then download
  ez-am doctor [--json] run the checks and print the report (exit 2 when not ready)
  ez-am download        go straight to the downloader (skips the checks)

Flags:
  --distro <name>       WSL distribution to use (default: auto-detect)
  --base <path>         runtime directory inside the distribution
                        (default: an existing $HOME/amd, otherwise XDG data)
  --lang <en|zh>        interface language (default: system locale)
  -h, --help            show this help
`,
		"cli.err.unexpected": "unexpected argument %q",
		"cli.err.unknown":    "unknown command %q",
		"cli.err.report":     "could not encode the report: %s",
	},

	ZH: {
		// doctor
		"doctor.header":                 "ez-am 环境自检",
		"doctor.no_runner":              "未提供命令执行器",
		"doctor.wsl.title":              "WSL",
		"doctor.wsl.unavailable":        "本机没有可用的 wsl.exe：%s",
		"doctor.wsl.available":          "wsl.exe 可用",
		"doctor.distro.title":           "发行版",
		"doctor.distro.ok":              "%s %s（%s）· %s",
		"doctor.distro.root_user":       "该发行版的默认用户是 root，请先配置普通用户",
		"doctor.distro.unsupported":     "不支持的发行版家族 %q；支持 Debian/Ubuntu 与 Arch",
		"doctor.distro.none":            "没有找到用户发行版（docker-desktop 不是用户发行版）",
		"doctor.distro.choose":          "请选择其一：%s",
		"doctor.distro.explicit":        "%s（显式指定）",
		"doctor.distro.notfound":        "指定的发行版 %q 不存在；改用 %q",
		"doctor.probe.failed":           "发行版探测失败：%s",
		"doctor.probe.exit":             "发行版探测退出码 %d：%s",
		"doctor.docker.title":           "Docker",
		"doctor.docker.unavailable":     "该发行版内 Docker 守护进程无响应",
		"doctor.docker.server":          "服务端 %s",
		"doctor.buildx.title":           "BuildKit",
		"doctor.buildx.missing":         "缺少 docker buildx；构建 wrapper 镜像时需要（仅一次）",
		"doctor.runtime.title":          "运行时文件",
		"doctor.runtime.missing":        "%s 下缺少：%s",
		"doctor.runtime.ok":             "已安装于 %s",
		"doctor.config.title":           "账号凭据",
		"doctor.config.missing":         "尚未配置 Apple 账号与 media-user-token",
		"doctor.config.ok":              "账号与 media token 已保存",
		"doctor.images.title":           "镜像",
		"doctor.images.missing":         "尚未拉取：%s（setup.sh 会拉取）",
		"doctor.images.ok":              "下载器与 wrapper 镜像就绪",
		"doctor.wrapper.title":          "wrapper 登录状态",
		"doctor.wrapper.ok":             "正在监听 10020/20020/30020",
		"doctor.wrapper.pending":        "已有一个验证请求在等待输入验证码",
		"doctor.wrapper.unhealthy":      "wrapper 状态异常",
		"doctor.downloads.title":        "输出目录",
		"doctor.downloads.unwritable":   "暂不可写：%s",
		"doctor.result":                 "结果：%s",
		"doctor.result.ready":           " — 可以开始下载",
		"doctor.result.needs_attention": " — 需先处理上面的问题",
		"doctor.result.suggested":       "建议操作：%s",

		// control
		"control.distro_label":                 "  发行版：%s",
		"control.checking.title":               "环境自检",
		"control.checking.detail":              "正在检查 WSL、Docker、运行时文件、凭据、镜像与 wrapper 登录状态…",
		"control.checking.hint":                "q 退出 · L 切换语言",
		"control.report.title":                 "自检结果",
		"control.report.ready":                 "环境就绪 — 正在进入下载器",
		"control.report.needs":                 "需要先补齐：%s",
		"control.report.hint":                  "ENTER 修复 · R 重新自检 · L 切换语言 · Q 退出",
		"control.details.title":                "详情",
		"control.fixes.title":                  "修复",
		"control.fixes.hint":                   "↑↓ 移动 · ENTER 执行 · R 重新自检 · Q 退出",
		"control.action.install_runtime":       "安装内置运行时文件",
		"control.action.collect_creds":         "填写 Apple ID、media-user-token 并部署",
		"control.action.login":                 "登录 Apple（双重验证）",
		"control.action.choose_distro":         "选择 WSL 发行版",
		"control.action.start_docker":          "启动 Docker 后重新自检",
		"control.action.start_docker_detail":   "启动 Docker Desktop，或在发行版内执行 `sudo service docker start`，然后按 R 重新自检。",
		"control.action.install_buildx":        "安装 docker buildx 插件后重新自检",
		"control.action.install_buildx_detail": "安装 docker-buildx 软件包，或把 docker-buildx 二进制放入 ~/.docker/cli-plugins/。构建 wrapper 镜像时需要（仅一次）。",
		"control.action.wsl_missing":           "安装 WSL2 后重新自检",
		"control.action.wsl_missing_detail":    "以管理员身份打开 PowerShell 执行 `wsl --install -d Ubuntu`，按提示重启后按 R；该发行版内还需要能访问 Docker。",
		"control.action.rerun":                 "重新运行自检",
		"control.action.quit":                  "退出",
		"control.distro.title":                 "选择 WSL 发行版",
		"control.distro.hint":                  "ENTER 选择 · ESC 返回",
		"control.creds.title":                  "Apple 账号",
		"control.creds.email":                  "Apple ID 邮箱",
		"control.creds.password":               "Apple ID 密码",
		"control.creds.storefront":             "区服（默认 us）",
		"control.creds.token":                  "media-user-token",
		"control.creds.hint":                   "TAB/↑↓ 移动 · ENTER 继续 · ESC 取消",
		"control.creds.required":               "Apple ID、密码、media-user-token 都是必填项。",
		"control.login.title":                  "额外验证 — 双重验证",
		"control.login.sent":                   "Apple 已向你的受信设备发送 6 位验证码。",
		"control.login.hint":                   "ENTER 提交",
		"control.login.invalid":                "验证码必须是 6 位数字。",
		"control.log.installing":               "正在安装内置运行时…",
		"control.log.starting_auth":            "正在发起 Apple 验证请求…",
		"control.log.auth_waiting":             "%s\n\n请输入 Apple 发送的 6 位验证码。",
		"control.log.deploying":                "正在写入配置并准备镜像（可能需要几分钟）…",
		"control.log.submitting":               "正在提交验证码并等待 wrapper 就绪…",
		"control.log.working":                  "处理中…",
		"control.log.done_hint":                "ENTER 或 R 重新自检 · Q 退出",
		"control.log.runtime_base":             "运行目录：%s",
		"control.log.runtime_installed":        "运行时文件已安装并校验。",
		"control.err.install_step":             "安装步骤 %q 退出码 %d：%s",
		"control.err.login_start":              "login.sh start 退出码 %d",
		"control.err.setup":                    "setup.sh 退出码 %d",
		"control.err.submit":                   "login.sh submit 退出码 %d",
		"control.err.distro_list":              "无法列出发行版：%s",
		"control.err.layout_home":              "未知发行版家目录",
		"control.err.runner":                   "需要命令执行器",
		"control.err.report":                   "环境自检失败：%s",

		// media
		"media.link_prompt":     "粘贴 Apple Music 专辑、单曲或播放列表链接：",
		"media.link_hint":       "ENTER 继续  ·  CTRL+C 退出",
		"media.metadata_title":  "正在读取专辑曲目",
		"media.metadata_detail": "正在获取曲目名称和可用音频格式...",
		"media.format_title":    "选择音频格式",
		"media.format_hint":     "↑↓ 选择  ·  ENTER 下载",
		"media.quality_current": "当前音质",
		"media.jobs_done":       "已完成 %d/%d 组，%s 分组失败\n\n",
		"media.download_failed": "下载失败",
		"media.exit_hint":       "ENTER 或 CTRL+C 退出",
		"media.done":            "✓ 下载完成",
		"media.files_in":        "文件位置：%s",
		"media.tracks_title":    "选择曲目与音质",
		"media.tracks_selected": "\n已选择 %d / %d\n\n",
		"media.tracks_window":   "  显示 %d-%d / %d",
		"media.unavailable":     "不可用",
		"media.available":       "可用",
		"media.tracks_hint":     "↑↓ 移动  ·  SPACE 选择  ·  ←→ 音质  ·  A 全选/取消  ·  G 应用到已选  ·  ENTER 下载",
		"media.stopping":        "正在停止下载",
		"media.stopping_detail": "正在停止 WSL 任务并清理下载容器...",
		"media.group_progress":  "分组 %d / %d  ·  %s",
		"media.awaiting_wsl":    "等待 WSL 输出...",
		"media.downloading":     "正在下载",
		"media.stop_hint":       "CTRL+C 停止并清理当前任务",
		"media.stage.validate":  "校验参数",
		"media.stage.wrapper":   "检查 Wrapper 登录状态",
		"media.stage.container": "启动下载容器",
		"media.stage.media":     "下载媒体",
		"media.stage.tag":       "解密、封装与写入标签",
		"media.stage.done":      "完成",
		"media.stage.stopping":  "正在停止",
		"media.stage.prepare":   "准备下载",

		// command line
		"cli.usage": `ez-am — WSL2 + Docker 上的 Apple Music 无损下载器

用法：
  ez-am                 环境自检 → 补齐缺失项 → 进入下载器
  ez-am doctor [--json] 只做自检并打印结果（未就绪时退出码 2）
  ez-am download        跳过自检，直接进入下载器

参数：
  --distro <name>       指定 WSL 发行版（默认自动识别）
  --base <path>         发行版内的运行目录
                        （默认沿用已有的 $HOME/amd，否则用 XDG 数据目录）
  --lang <en|zh>        界面语言（默认跟随系统区域设置）
  -h, --help            显示帮助
`,
		"cli.err.unexpected": "多余的参数 %q",
		"cli.err.unknown":    "未知命令 %q",
		"cli.err.report":     "无法序列化自检报告：%s",
	},
}

// Keys returns every known message key, in English catalog order.
func Keys() []string {
	keys := make([]string, 0, len(catalog[EN]))
	for key := range catalog[EN] {
		keys = append(keys, key)
	}
	return keys
}
