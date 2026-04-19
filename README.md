# emby-pro

`emby-pro` 是基于 `linuxserver/emby` 做的增强版 Emby，面向 OpenList 资源场景，内置 `.strm` 自动生成和 302 直链播放能力。它的目标很直接：用一个镜像完成资源接入、媒体入库和播放管理。

Repository: <https://github.com/monlor/emby-pro>  
Image: `ghcr.io/monlor/emby-pro`

镜像会自动跟随 `lscr.io/linuxserver/emby` 的上游稳定 tag 发布：

- 想追踪最新上游版本：使用 `ghcr.io/monlor/emby-pro:latest`
- 想固定到某个 Emby 版本：使用 `ghcr.io/monlor/emby-pro:<linuxserver-emby-tag>`

生产环境更推荐固定具体版本 tag；`latest` 更适合追新。

## 项目特点

- 支持扫描 OpenList 指定目录并自动生成 `.strm`
- 支持全局配置直链播放，并可按 Emby 用户进一步控制
- 支持单独控制 Web 端是否开启直链播放
- 基于单个镜像完成 Emby、`.strm` 同步和播放管理
- 适合希望减少旁路服务、快速跑通 OpenList + Emby 的场景

## 快速开始

推荐先用最少配置跑起来：

- `OPENLIST_BASE_URL`
- `OPENLIST_USERNAME`
- `OPENLIST_PASSWORD`
- `OPENLIST_PATHS`

`PLAY_TICKET_SECRET` 可以不配。未配置时，`emby-pro` 会在启动时自动生成临时密钥。

播放阶段生成的短效地址会优先根据当前请求自动推导公网访问地址与前缀，例如反代后的 `https://media.example.com/emby`。多数场景不需要手动配置 `PUBLIC_URL`。

如果 `emby-pro` 访问 OpenList 走内网地址，但客户端最终 302 下载需要走公网域名，可以额外配置 `OPENLIST_PUBLIC_URL`。未配置时会回退使用 `OPENLIST_BASE_URL`。

如果希望 OpenList 实际源目录和 `.strm` 暴露路径解耦，可以额外配置 `STRM_PATH_MAPPINGS`，例如把真实源 `/115pan_cookie` 暴露成 `/115pan`。

最小示例：

```bash
docker run -d \
  --name emby-pro \
  --restart unless-stopped \
  -p 8096:8096 \
  -p 28096:28096 \
  -e PUID=1000 \
  -e PGID=1000 \
  -e TZ=Asia/Shanghai \
  -e OPENLIST_BASE_URL=https://list.example.com \
  -e OPENLIST_USERNAME=your-openlist-user \
  -e OPENLIST_PASSWORD=your-openlist-password \
  -e OPENLIST_PATHS=/movies,/tv \
  -e PLAY_TICKET_TTL=12h \
  -v ./config:/config \
  -v ./strm:/strm \
  ghcr.io/monlor/emby-pro:latest
```

固定版本示例：

```bash
docker run -d \
  --name emby-pro \
  --restart unless-stopped \
  -p 8096:8096 \
  -p 28096:28096 \
  -e PUID=1000 \
  -e PGID=1000 \
  -e TZ=Asia/Shanghai \
  -e OPENLIST_BASE_URL=https://list.example.com \
  -e OPENLIST_USERNAME=your-openlist-user \
  -e OPENLIST_PASSWORD=your-openlist-password \
  -e OPENLIST_PATHS=/movies,/tv \
  -v ./config:/config \
  -v ./strm:/strm \
  ghcr.io/monlor/emby-pro:4.8.11.0-ls258
```

然后在 Emby 中把 `/strm/...` 下对应目录添加为媒体库即可。

Compose 示例见 [docker-compose.yml](/Users/monlor/Workspace/emby-pro/docker-compose.yml) 和 [docker-compose.dev.yml](/Users/monlor/Workspace/emby-pro/docker-compose.dev.yml)。

## 常用场景

- 只想快速体验：配置 `OPENLIST_BASE_URL`、`OPENLIST_USERNAME`、`OPENLIST_PASSWORD` 和 `OPENLIST_PATHS`
- OpenList 内网访问和外网下载地址不同：额外配置 `OPENLIST_PUBLIC_URL`
- 希望 `.strm` 暴露路径和 OpenList 真实目录不同：配置 `STRM_PATH_MAPPINGS`
- 希望 Web 端关闭直链播放：显式配置 `OPENLIST_DIRECT_PLAY_WEB=false`
- 希望部分用户走直链播放：配置 `OPENLIST_DIRECT_PLAY_USERS`
- 想要更稳定的生产配置：显式设置 `PLAY_TICKET_SECRET`
- 想要多实例部署：必须使用固定的 `PLAY_TICKET_SECRET`

## 使用建议

- 如果 Emby 跑在反代子路径下：把 `EMBY_BASE_URL` 配成完整前缀

未显式配置 `PLAY_TICKET_SECRET` 的影响：

- 服务重启后，旧播放票据会立即失效
- 多实例之间不能共享已签发票据

## 文档导航

README 前半部分优先帮助你快速体验。实现原理、播放链路和完整配置说明放在后面及 `docs`：

- [docs/emby-302-playback.md](/Users/monlor/Workspace/emby-pro/docs/emby-302-playback.md)：302 直链播放实现原理、`PlaybackInfo` 改写原因、票据模型和路由设计
- [examples/strm-rules.yml](/Users/monlor/Workspace/emby-pro/examples/strm-rules.yml)：规则文件示例

## 技术说明

下面这些内容属于实现层说明，放在项目介绍之后，方便首次接触的用户先理解“它有什么价值”，再决定是否深入细节。

### 总体设计

当前版本把“稳定定位”和“实际播放授权”拆成两层：

- `.strm` 文件里写稳定系统地址，例如 `http://127.0.0.1:28096/strm/openlist/movies/demo.mp4`
- 客户端请求 `PlaybackInfo` 时，`emby-pro` 会把播放入口改写成带短效票据的 `/strm/openlist/...?...t=...`
- 真正播放时，再向 OpenList 换取当前可用的下载地址并返回 `302`

这样做的目的，是避免把 OpenList 直链或长期可复用播放链接直接写入 `.strm` 文件。

### 播放链路

1. Emby 读取 `.strm`，拿到稳定的 `/strm/openlist/...` 地址。
2. 客户端请求 `PlaybackInfo`。
3. `emby-pro` 改写 `MediaSources.Path`。
4. 如果启用 `OPENLIST_DIRECT_PLAY=true`，且当前客户端允许直链播放，`DirectStreamUrl` 也会改到同一条短效链路。
5. 客户端开始播放，请求带票据的 `/strm/openlist/...?...t=...`。
6. `emby-pro` 校验票据，调用 OpenList `/api/fs/get`。
7. `emby-pro` 返回 OpenList 当前可用的 `/d/...?...sign=...`，客户端完成播放。

完整原理见 [docs/emby-302-playback.md](/Users/monlor/Workspace/emby-pro/docs/emby-302-playback.md)。

## 浏览器播放限制

当前实现的最后一步是返回 `302`，把客户端跳转到 OpenList 当前可用的下载地址。

这意味着浏览器中的 Emby Web 最终访问的可能不是你的 Emby 或 `emby-pro` 域名，而是网盘或其 CDN 域名。此时能否播放，取决于目标站点是否允许浏览器跨域访问。

常见现象：

- Emby Web 播放 `115`、`夸克` 等资源时，可能出现 CORS 报错
- 原生客户端、TV 客户端、外部播放器有时可以正常播放
- 这通常不是 `PlaybackInfo` 改写失败，而是目标直链本身不满足浏览器跨域要求

如果你的主要场景是浏览器播放这类资源，通常只有两种更稳定的方案：

- 改用服务端代理拉流，而不是把浏览器直接 `302` 到网盘直链
- 改用原生 Emby 客户端、TV 客户端或外部播放器

## 配置项

| Variable | Default | Description |
| --- | --- | --- |
| `OPENLIST_BASE_URL` | none | OpenList API 地址，供 `emby-pro` 服务端访问 |
| `OPENLIST_PUBLIC_URL` | none | 客户端可访问的 OpenList 外部地址，仅用于生成 302 下载链接；未设置时回退到 `OPENLIST_BASE_URL` |
| `OPENLIST_TOKEN` | none | OpenList API token；生产环境推荐 |
| `OPENLIST_USERNAME` | none | 未设置 token 时的登录用户名；快速访问可直接使用 |
| `OPENLIST_PASSWORD` | none | 未设置 token 时的登录密码；快速访问可直接使用 |
| `OPENLIST_REQUEST_TIMEOUT` | `15s` | OpenList 请求超时 |
| `OPENLIST_RETRY` | `3` | OpenList 请求重试次数 |
| `OPENLIST_RETRY_BACKOFF` | `2s` | OpenList 重试退避 |
| `OPENLIST_LIST_PER_PAGE` | `200` | OpenList 目录分页大小 |
| `OPENLIST_INSECURE_SKIP_VERIFY` | `false` | 跳过 TLS 校验 |
| `OPENLIST_DISABLE_HTTP2` | `false` | 禁用 HTTP/2 |
| `OPENLIST_DIRECT_PLAY` | `true` | 是否总开关启用直链播放改写 |
| `OPENLIST_DIRECT_PLAY_WEB` | `true` | 是否允许 Emby Web 改写 `DirectStreamUrl`；关闭后可避免浏览器直接跳转网盘/CDN 时的 CORS 问题 |
| `OPENLIST_DIRECT_PLAY_USERS` | none | 仅对指定用户启用 direct play，支持 ID 或用户名，逗号分隔；不影响 Emby 本机回环地址上的内部转码回源 |
| `PUBLIC_URL` | `http://127.0.0.1:28096` | `.strm` 稳定地址和播放阶段回退用的外部地址；播放改写会优先根据当前请求自动推导公网地址 |
| `STRM_BASE_DIR` | `/strm` | `.strm` 输出目录 |
| `STRM_PATH_MAPPINGS` | none | OpenList 源路径到 `.strm` 暴露路径的前缀映射，格式如 `/115pan_cookie:/115pan,/quark_cookie:/quark` |
| `STRM_RULES_FILE` | `/config/strm-rules.yml` | 规则文件路径 |
| `STRM_INDEX_DB` | `/config/strm-index.db` | SQLite 索引路径 |
| `STRM_SCAN_INTERVAL` | `300` | 增量扫描周期，秒 |
| `STRM_FULL_RESCAN_INTERVAL` | `86400` | 全量校准周期，秒 |
| `STRM_MAX_DIRS_PER_CYCLE` | `200` | 每轮最多扫描目录数 |
| `STRM_MAX_REQUESTS_PER_CYCLE` | `1000` | 每轮最多 OpenList API 请求数 |
| `STRM_MIN_FILE_SIZE` | `15M` | 仅为不小于该大小的视频生成 `.strm`，支持纯字节数或 `15M`、`2G` 这类写法 |
| `STRM_VIDEO_EXTS` | common video exts | 允许生成 `.strm` 的视频扩展名 |
| `STRM_CLEAN_REMOVED` | `true` | 清理已从 OpenList 消失的 `.strm` |
| `STRM_OVERWRITE` | `true` | 强制重写本轮扫描命中的 `.strm`；关闭后仅在内容或路径变化时写盘 |
| `STRM_LOG_LEVEL` | `info` | 日志级别，当前支持 `info` / `debug` |
| `PLAY_TICKET_SECRET` | auto-generated | 用于签发 `/strm/openlist/...?...t=...` 播放票据的密钥；未设置时启动自动生成临时密钥 |
| `PLAY_TICKET_TTL` | `12h` | 播放票据有效期 |
| `EMBY_BASE_URL` | `http://127.0.0.1:8096` | Emby 上游地址；如果 Emby 部署在子路径如 `/emby` 下，这里应填写完整前缀 |
| `EMBY_REQUEST_TIMEOUT` | `10s` | Emby 请求超时 |
| `EMBY_TOKEN_CACHE_TTL` | `30s` | Emby token 校验缓存时间 |

典型场景：

- 内网 API 访问 OpenList：`OPENLIST_BASE_URL=http://openlist:5244`
- 对外 302 下载地址：`OPENLIST_PUBLIC_URL=https://list.example.com`
- 路径映射：`OPENLIST_PATHS=/115pan_cookie` 且 `STRM_PATH_MAPPINGS=/115pan_cookie:/115pan`

## 规则文件

除了 `OPENLIST_PATHS`，也支持本地规则文件：

```yaml
defaults:
  clean_removed: true
  overwrite: true
  flatten: false

rules:
  - name: movies
    source_path: /movies
    target_path: /strm/movies
    exclude_regex: "/sample|trailer/i"

  - name: tv
    source_path: /tv
    target_path: /strm/tv

  - name: anime-flat
    source_path: /anime
    target_path: /strm/anime-flat
    flatten: true
```

同一路径如果同时出现在 `OPENLIST_PATHS` 和规则文件中，规则文件优先。

`overwrite` 表示是否强制重写本规则命中的 `.strm`。工具始终会接管并覆盖未纳入索引的同名目标文件；只有已追踪且内容未变化的文件，才会在 `overwrite: false` 时跳过重复写盘。

最小文件大小通过环境变量 `STRM_MIN_FILE_SIZE` 全局控制，默认 `15M`。

示例文件见 [examples/strm-rules.yml](/Users/monlor/Workspace/emby-pro/examples/strm-rules.yml)。

## 项目结构

- [cmd/strm-sync](/Users/monlor/Workspace/emby-pro/cmd/strm-sync)：入口程序，启动同步器和 redirect 服务
- [internal/config](/Users/monlor/Workspace/emby-pro/internal/config)：环境变量、规则文件、配置校验
- [internal/syncer](/Users/monlor/Workspace/emby-pro/internal/syncer)：扫描 OpenList、生成 `.strm`、维护索引
- [internal/redirect](/Users/monlor/Workspace/emby-pro/internal/redirect)：`/strm/openlist` 路由、播放票据、`PlaybackInfo` 改写、302 回源
- [internal/openlist](/Users/monlor/Workspace/emby-pro/internal/openlist)：OpenList API 客户端
- [internal/emby](/Users/monlor/Workspace/emby-pro/internal/emby)：Emby API 客户端
- [internal/index](/Users/monlor/Workspace/emby-pro/internal/index)：本地 SQLite 索引
- [examples](/Users/monlor/Workspace/emby-pro/examples)：规则文件示例
- [docs](/Users/monlor/Workspace/emby-pro/docs)：设计和实现说明

## 兼容性变更

以下旧配置已移除，启动时会直接报错：

- `OPENLIST_DIRECT_LINK_PERMANENT`
- `REDIRECT_TARGET_MODE`
- 规则文件里的 `url_mode`
- `REDIRECT_SECRET`

## 本地开发

只跑一次同步：

```bash
go run ./cmd/strm-sync --once
```

测试：

```bash
go test ./...
```

竞态检测：

```bash
go test -race ./...
```
