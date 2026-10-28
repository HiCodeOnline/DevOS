# DevOS

[English](README.md) | **简体中文**

DevOS 是一个完全运行在浏览器里的本地优先开发者工作台。单个 Go 二进制文件内置了类桌面的外壳（Dock、开始菜单、窗口、锁屏）和一组内置开发应用——SSH 终端、SFTP 客户端、数据库客户端、接口工具、笔记、工具集与密钥库——并且可以在不重启的情况下加载离线 Web 插件。

所有服务只监听 `127.0.0.1`，不需要账号、云服务或外部数据库。

## 特性

- **单二进制、零运行时依赖**：桌面外壳与内置应用通过 `go:embed` 编译进可执行文件；数据库驱动尽量使用纯 Go 实现（含 SQLite）。
- **默认本地化**：只绑定回环地址，自动检测并复用已在运行的实例，数据保存在你自己选择的目录。
- **类桌面外壳**：Dock、带搜索的开始菜单、可拖动/缩放的窗口、预设或上传壁纸、锁屏。
- **9 种界面语言**：简体中文、繁体中文、英语、日语、韩语、法语、德语、俄语、印地语，运行时即可切换。
- **应用密码保护**：可选的解锁密码（scrypt）、基于会话的 API 校验、登录限流，以及 AES-256-GCM 凭据加密。
- **可插拔应用**：内置应用与第三方 Web 插件共用同一套清单格式，插件以 ZIP 离线安装。

## 内置应用

| 应用 | ID | 功能 |
| --- | --- | --- |
| SSH 终端 | `ssh` | 会话库、基于 WebSocket 的多标签终端、跳板机、字符编码支持、会话加密导入导出 |
| SFTP 文件 | `sftp` | 双栏文件管理、传输队列、权限管理、在线编辑 |
| 数据库 | `db` | 多连接管理、结构浏览、SQL 控制台、行编辑与 NoSQL 控制台；支持 MySQL、PostgreSQL、SQLite、SQL Server、Oracle、ClickHouse、Redis、MongoDB、Cassandra、NebulaGraph 等（含国产化、云与大数据引擎，部分连接器标记为规划中） |
| 接口工具 | `api` | HTTP 请求集合、环境变量、调试，以及本地 Mock 服务 |
| 笔记 | `notes` | 类 Notion 的块编辑器，支持页面树、搜索、收藏、Markdown 导入导出 |
| 工具集 | `toolbox` | JSON、编码、时间戳、哈希等日常小工具 |
| 密钥库 | `vault` | 分组的密钥管理，支持标签、检索、解锁密码与全库加密存储 |
| 插件管理 | `plugins` | 离线安装 Web 插件、处理版本冲突、卸载 |

## 环境要求

- 从源码构建需要 **Go 1.27+**。
- 现代浏览器（Chrome / Edge / Safari / Firefox）。
- 主要开发与验证平台为 Windows 10/11；Go 代码本身不依赖特定桌面外壳，但托盘集成仅在 Windows 上日常验证。

## 快速开始

```bash
git clone <repository-url> devos
cd devos
go build -o devos.exe .
./devos.exe
```

首次启动会在浏览器中打开初始化向导，选择数据目录后桌面即运行在 `http://127.0.0.1:7788`。

Windows 发布包使用 `-H windowsgui` 构建，双击可执行文件会直接启动服务并打开浏览器，不会弹出控制台窗口。本地可比照构建：

```bash
go build -trimpath -ldflags "-s -w -H windowsgui" -o devos.exe .
```

开发时也可跳过编译：

```bash
go run .
```

### 命令行参数

| 参数 | 说明 |
| --- | --- |
| `-port <n>` | 监听端口；默认读取 `config.json`，否则为 `7788`（端口占用时自动尝试后续可用端口，最多 `maxPortTries` 次）。 |
| `-no-tray` | 不创建任务栏托盘图标，进程前台运行直到中断。 |
| `-no-browser` | 启动后不自动打开浏览器。 |
| `-setup` | 强制打开数据目录初始化向导（即使已选择过目录）。 |

## 配置

配置位于 `<数据目录>/config.json`，首次运行自动生成默认值：

| 键 | 默认值 | 说明 |
| --- | --- | --- |
| `port` | `7788` | 首选监听端口。 |
| `openBrowser` | `true` | 启动时用默认浏览器打开桌面。 |
| `maxPortTries` | `20` | 首选端口被占用时最多依次探测的端口数量。 |

语言、壁纸与应用密码单独保存在 `security.json` 中，通过桌面内的设置面板修改。

## 数据目录

默认位于系统配置目录下的 `DevOS`：

| 操作系统 | 默认数据目录 |
| --- | --- |
| Windows | `%APPDATA%\DevOS` |
| macOS | `~/Library/Application Support/DevOS` |
| Linux | `~/.config/DevOS` |

所选择的目录记录在系统配置目录的 `location.json` 中，因此向导只会出现一次。切换到新目录**不会**自动迁移已有数据。

```text
<数据目录>/
├── config.json          # 端口与浏览器行为
├── security.json        # 应用密码、语言、壁纸偏好
├── wallpapers/          # 上传的壁纸图片
├── apps/<应用ID>/        # 各应用数据，按领域拆分为独立 SQLite 文件
│   ├── profiles.db      # SSH / SFTP 会话配置
│   ├── connections.db   # 数据库连接配置
│   ├── vault.db         # 密钥库元信息、分组与条目
│   └── notes.db         # 笔记页面与内容块
└── plugins/<插件ID>/     # 已安装的 Web 插件
```

各领域使用独立数据库文件，开启 WAL 模式且只保留一个写连接，不同应用并发写入时不会争抢同一把数据库锁；表结构升级通过 `PRAGMA user_version` 顺序执行。从旧版本升级时，原有的 `profiles.json`、`connections.json`、`notes.json`、`meta.json` 与 `vault.json` 会在首次启动时自动导入，并改名为 `*.bak` 保留备份。

## 安全

- **应用密码**：可选。启用后每次启动都需输入密码，未解锁前应用与 API 均被拦截。密码使用 scrypt 哈希（`N=32768, r=8, p=1`），校验串行执行以限制内存占用。
- **会话与守卫**：内核为所有 API 路由增加会话校验与来源校验，仅健康检查、系统信息、解锁与壁纸文件等少量接口保持公开。
- **登录限流**：连续解锁失败会进入冷却期，并返回剩余等待时间。
- **凭据加密**：应用保存的 SSH 密码/密钥、数据库密码、密钥库条目均使用 AES-256-GCM 加密落盘，文件权限仅限当前用户。
- **加密导入导出**：支持该功能的应用（SSH、数据库）会用 scrypt 派生的随机新密钥重新加密导出文件；密钥只展示一次，导入时必填且无法找回。

## 国际化

界面内置以下语言：

| 代码 | 语言 |
| --- | --- |
| `zh-CN` | 简体中文 |
| `zh-TW` | 繁體中文 |
| `en` | English |
| `ja` | 日本語 |
| `ko` | 한국어 |
| `fr` | Français |
| `de` | Deutsch |
| `ru` | Русский |
| `hi` | हिन्दी |

词典位于 [`web/assets/locales`](web/assets/locales)，每个文件通过 `window.DevOSI18n.register` 注册一份扁平键值表。桌面外壳、锁屏、设置与初始化向导均已完整翻译；内置应用另有自己的词典目录 `web/assets/locales/apps`（目前为简中、繁中、英文），其他界面语言在应用内部回退为英文。

## 插件开发

Web 插件是纯前端应用：内核只负责装载、校验与提供静态资源，不执行插件代码。插件是以 `plugin.json` 清单加静态根目录（默认 `web/`）组成的目录，打包为 ZIP 后可通过**插件管理**离线安装，无需重启 DevOS。

```text
my-plugin/
├── plugin.json      # id、name、version、entry、staticRoot、spa 等
└── web/
    └── index.html
```

完整规范（清单字段、图标规则、路径安全、Next.js 静态导出方法）见 [`docs/web-plugins.md`](docs/web-plugins.md)，最小示例见 [`plugins/hello`](plugins/hello)。

## 目录结构

```text
.
├── main.go                 # 入口：参数解析、单实例检测、托盘
├── setup.go                # 首次运行的数据目录向导
├── web/                    # 桌面外壳（编译时内嵌）
│   ├── index.html          # 桌面、Dock、开始菜单、锁屏、设置
│   ├── setup.html          # 初始化向导
│   └── assets/             # desktop.js、i18n.js、locales/、样式
├── internal/
│   ├── kernel/             # HTTP 内核、应用窗口、安全守卫、Web 插件加载
│   ├── plugin/             # 插件接口与清单类型
│   ├── plugins/            # 内置应用（ssh、sftp、db、apitool、notes、toolbox、vault、pluginmgr）
│   ├── config/             # config.json 读写
│   ├── security/           # 应用密码、会话、语言存储
│   ├── secure/             # AES-256-GCM 凭据存储、加密导入导出
│   ├── profile/            # 连接类应用共用的配置存储
│   └── sshtools/           # SSH/SFTP 辅助工具
├── plugins/hello/          # 外部 Web 插件示例
└── docs/web-plugins.md     # Web 插件规范（v1）
```

## 开发与测试

```bash
gofmt -l .
go test ./...
go build ./...
```

`web/`（以及各插件的 `web/`）会在编译时通过 `go:embed` 内嵌进二进制，因此**修改 HTML/CSS/JS 后必须重新编译并重启**。修改静态资源时请同步递增脚本/样式 URL 上的 `?v=` 版本号，避免浏览器复用旧缓存，然后强制刷新页面。

内核提供给外壳使用的内部接口包括：`/api/health`、`/api/system`、`/api/apps`、`/api/plugins` 与 `/api/security/*`（状态、解锁、锁定、密码、设置、壁纸）。内置应用在 `/api/apps/<id>/*` 下挂载各自的接口；接口工具的 Mock 服务则公开挂在 `/mock/*`。

## 许可证

[Apache License 2.0](LICENSE) © 2026 HiCodeOnline
