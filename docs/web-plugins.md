# DevOS Web 插件规范 v1

Web 插件是纯前端应用：内核只负责装载、校验与提供静态资源，不执行插件代码。
插件以目录形式存在，打包为 ZIP 后可通过「插件管理」离线安装，安装后无需重启内核。

## 1. 目录结构

```
my-plugin/
├── plugin.json          # 必填，插件清单
├── icon.png             # 可选，图标（也可放在静态目录内）
└── web/                 # 静态资源目录，默认 web（可用 staticRoot 改名）
    ├── index.html       # 入口页，默认 index.html（可用 entry 指定）
    ├── assets/...
    └── ...
```

- 安装后的目录名必须与 `plugin.json` 的 `id` 完全一致。
- `plugin.json` 中存在未知字段时会被忽略，便于向前兼容。
- 静态资源目录、入口文件、图标文件都会在安装时做存在性校验。
- Next.js 静态导出可把 `staticRoot` 指向 `out`（见第 4 节）。

## 2. plugin.json 字段

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `id` | string | 是 | 唯一标识，同时作为目录名与 URL 前缀。仅允许小写字母、数字、`.`、`_`、`-`，长度 1-64，且以字母或数字开头。与内置插件同名会被拒绝。 |
| `name` | string | 是 | 桌面显示名称 |
| `description` | string | 否 | 一句话描述 |
| `version` | string | 建议 | 语义化版本（如 `1.2.0`），用于冲突提示与更新比较 |
| `category` | string | 否 | 分类，默认「Web 插件」 |
| `icon` | string | 否 | 内联 SVG、`data:` URL，或插件内相对图片路径（如 `icon.png`、`web/logo.svg`） |
| `accent` | string | 否 | 主题强调色，如 `#0ea5e9` |
| `entry` | string | 否 | 入口页，相对静态资源目录，默认 `index.html`，允许嵌套如 `zh/index.html` |
| `width` | number | 否 | 默认窗口宽度，默认 960 |
| `height` | number | 否 | 默认窗口高度，默认 640 |
| `multi` | bool | 否 | 是否允许打开多个窗口，默认 false |
| `specVersion` | number | 否 | 插件规范版本，省略视为 1；大于内核支持版本会被拒绝 |
| `staticRoot` | string | 否 | 静态资源目录，相对插件根目录，默认 `web` |
| `spa` | bool | 否 | 无扩展名路径未命中时回退到入口页，默认 false |

内置插件（随内核编译）使用同一份清单结构，但 `specVersion` / `staticRoot` / `spa` 只对 Web 插件生效。

## 3. 图标规则

`icon` 有三种写法：

1. 内联 SVG（直接以 `<` 开头），例如示例插件 `plugins/hello`。
2. `data:` 开头的内联图片。
3. 插件内的相对路径：
   - 位于静态资源目录内（`web/icon.png` 或 `icon.png` 且静态目录覆盖该路径）→ 规范化为 `/apps/<id>/icon.png`；
   - 位于插件根目录但不在静态目录内（如根目录 `icon.png`）→ 规范化为 `/apps/<id>/__icon`，由内核单独读取。
   - 不允许绝对路径、`http(s)://` 外链与 `..` 穿越。

图标文件必须真实存在，否则安装校验失败。推荐 256×256 的 PNG 或 SVG。

## 4. Next.js 静态导出

使用 `output: 'export'` 生成纯静态站点，并把产物目录作为静态根：

```js
// next.config.mjs
export default {
  output: 'export',
  // 必须与插件安装后的 URL 前缀一致，否则资源 404
  basePath: '/apps/my-plugin',
  assetPrefix: '/apps/my-plugin',
  images: { unoptimized: true },
  // 可选：生成 about/index.html 形式的目录索引
  trailingSlash: true,
};
```

打包结构：

```
my-plugin/
├── plugin.json      # staticRoot: "out", entry: "index.html"
└── out/
    ├── index.html
    ├── about.html          # 非 trailingSlash 布局
    ├── _next/static/...    # JS/CSS 资源
    └── favicon.ico
```

要点：

- `basePath` / `assetPrefix` 必须写死为 `/apps/<id>`，因为插件安装在固定路径下。
- 服务端会为无扩展名请求依次尝试 `<path>.html`、`<path>/index.html`，再回退入口页（仅当 `spa: true`）。
  因此 Next.js 的 `/about`、`/about/` 两种导出布局都能正确访问。
- `images.unoptimized: true`：静态导出没有图片优化服务。
- 若应用内部使用客户端路由，建议开启 `spa: true` 作为兜底。

## 5. ZIP 打包规范

```powershell
Compress-Archive -Path my-plugin -DestinationPath my-plugin.zip
```

- 允许 ZIP 根目录直接包含 `plugin.json`，或仅含一个顶层目录（该目录下包含 `plugin.json`）。
- 单个 ZIP 上限 64MB，条目数上限 20000，解压后总大小上限 256MB。
- 拒绝符号链接、绝对路径、盘符、`..` 穿越（Zip Slip）、`__MACOSX` 元数据。
- 安装时解压到 `.staging-*` 临时目录校验，通过后再原子移动到最终目录。

## 6. 安装、更新与冲突

安装入口：桌面「插件管理」→ 安装 ZIP，或直接调用接口。

### 目录与数据

| 位置 | 说明 |
| --- | --- |
| `<数据目录>/plugins/<id>` | 用户安装目录，卸载只影响这里 |
| `<数据目录>/plugins/.backup` | 覆盖安装时的临时备份 |
| `<数据目录>/plugins/.trash` | 卸载后的临时回收目录 |
| `<数据目录>/apps/<id>` | 插件数据目录，覆盖与卸载都会保留 |
| `<exe目录>/plugins`、`<工作目录>/plugins` | 外部插件来源，只读装载，不可通过 API 覆盖或卸载 |

### 冲突处理

安装时若 `id` 已存在，接口返回 `409`：

```json
{
  "error": "插件已安装：示例插件",
  "conflict": true,
  "existing": { "id": "demo", "version": "1.0.0", "...": "..." },
  "incoming": { "id": "demo", "version": "1.1.0", "...": "..." },
  "relation": "newer",
  "actions": ["overwrite", "keep", "copy"]
}
```

`relation` 取值：

- `newer`：新包版本更高（升级）
- `older`：新包版本更低（降级）
- `same`：版本相同或无法比较

用户可选择：

1. **覆盖安装**（`action=overwrite`）——先备份现有目录，写入新目录；失败自动回滚。数据目录保持不变。
2. **保留现有**（`action=keep`）——放弃新包，现有插件不变。
3. **另存为新插件**（`action=copy`，同时传 `id`）——把新包改写到新的 id 下并存，两者可共存。

内置插件与外部目录插件不可覆盖：前者返回 409，后者提示改用保留或另存。

### 校验清单

安装前后会校验：

- `plugin.json` 存在且为合法 JSON；
- `id` 符合命名规则，且不与内置插件或已安装插件冲突；
- `specVersion` 不超过内核支持版本；
- `staticRoot` 是合法的相对目录且真实存在；
- `entry` 位于静态目录内且真实存在；
- `icon` 指向的文件真实存在（内联 SVG 除外）。

## 7. HTTP API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/plugins` | 内置 + 已安装插件列表，含 `source`、`dir`、`dataDir`、`installedAt` |
| POST | `/api/plugins/install` | multipart 安装：`file`（必填）、`action`（overwrite/keep/copy）、`id`（copy 必填） |
| DELETE | `/api/plugins/:id` | 卸载已安装插件（内置与外部目录插件不可卸载） |
| GET | `/api/apps` | 桌面应用列表（内置 + Web 插件） |
| GET | `/apps/:id/*` | 插件静态资源 |

安装成功后，插件管理页会向桌面发送 `devos:apps-changed` 消息，桌面自动刷新应用列表，无需重启。

## 8. 最小示例

```
hello/
├── plugin.json
└── web/
    └── index.html
```

```json
{
  "id": "hello",
  "name": "Hello 插件",
  "description": "运行时加载的 Web 插件示例",
  "version": "0.1.0",
  "category": "示例",
  "icon": "icon.png",
  "accent": "#ec4899",
  "entry": "index.html",
  "width": 780,
  "height": 540,
  "multi": true,
  "specVersion": 1
}
```

仓库内 `plugins/hello` 是可运行的示例；把它的 `icon` 换成 `icon.png` 并在 `hello/` 下放置图片即可验证图片图标路径。
