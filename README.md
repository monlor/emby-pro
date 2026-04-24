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

当前版本不再使用 emby-pro 业务环境变量，统一改为 `/config/app.yml`。容器首次启动时如果文件不存在，会自动生成默认模板；也可以直接参考 [examples/app.yml](/Users/monlor/Workspace/emby-pro/examples/app.yml) 预先写好。

启动后通过同一端口访问 `/admin`：

- 路径：`http://<your-host>:28096/admin`
- 认证：复用 Emby 登录态
- 权限：仅 Emby 管理员可修改配置和手动刷新扫描

管理页支持：

- 单页表单修改 OpenList / Emby / redirect / sync / rules
- 保存后热更新生效
- 手动触发全部或单条规则的 `.strm` 全量重扫

`PLAY_TICKET_SECRET` 仍然可以留空。未配置时，`emby-pro` 会在运行时生成临时密钥。

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
  -v ./config:/config \
  -v ./strm:/strm \
  ghcr.io/monlor/emby-pro:latest
```

然后把 [examples/app.yml](/Users/monlor/Workspace/emby-pro/examples/app.yml) 放到 `./config/app.yml`，或先空跑一次再去 `/admin` 填写。

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
  -v ./config:/config \
  -v ./strm:/strm \
  ghcr.io/monlor/emby-pro:4.8.11.0-ls258
```

然后在 Emby 中把 `/strm/...` 下对应目录添加为媒体库即可。

Compose 示例见 [docker-compose.yml](/Users/monlor/Workspace/emby-pro/docker-compose.yml) 和 [docker-compose.dev.yml](/Users/monlor/Workspace/emby-pro/docker-compose.dev.yml)。

## 常用场景

- 只想快速体验：先启动容器，再到 `/admin` 填 `OpenList`、`rules` 和同步参数
- OpenList 内网访问和外网下载地址不同：在 `app.yml` 里分别设置 `openlist.base_url` 和 `openlist.public_url`
- 希望自定义来源和目标目录：直接编辑 `rules[*].source_path` 和 `rules[*].target_path`
- Web 端默认不走直链；需要开启时再打开 `redirect.direct_play_web`
- 希望部分用户走直链播放：在 `/admin` 勾选 Emby 现有用户，留空则默认所有用户都走直链
- 想要更稳定的生产配置：显式设置 `redirect.play_ticket_secret`
- 想要多实例部署：必须使用固定的 `redirect.play_ticket_secret`

## 使用建议

- 如果 Emby 跑在反代子路径下：把 `emby.base_url` 配成完整前缀

未显式配置 `PLAY_TICKET_SECRET` 的影响：

- 服务重启后，旧播放票据会立即失效
- 多实例之间不能共享已签发票据

## 文档导航

README 前半部分优先帮助你快速体验。实现原理、播放链路和完整配置说明放在后面及 `docs`：

- [docs/emby-302-playback.md](/Users/monlor/Workspace/emby-pro/docs/emby-302-playback.md)：302 直链播放实现原理、`PlaybackInfo` 改写原因、票据模型和路由设计
- [examples/app.yml](/Users/monlor/Workspace/emby-pro/examples/app.yml)：统一配置文件示例

## 技术说明

下面这些内容属于实现层说明，放在项目介绍之后，方便首次接触的用户先理解“它有什么价值”，再决定是否深入细节。

### 总体设计

当前版本把“稳定定位”和“实际播放授权”拆成两层：

- `.strm` 文件里写稳定系统地址，例如 `http://127.0.0.1:28096/strm/openlist/movies/demo.mp4`
- 客户端请求 `PlaybackInfo` 时，`emby-pro` 会把播放入口改写成带短效票据的 `/strm/openlist/...?...t=...`
- 开启 `redirect.fast_playback_info: true` 后，允许直链的客户端会优先走 fast path：直接基于 item 元数据构造最小可用 `PlaybackInfo`，并补齐 `PlaySessionId`
- 真正播放时，再向 OpenList 换取当前可用的下载地址并返回 `302`

这样做的目的，是避免把 OpenList 直链或长期可复用播放链接直接写入 `.strm` 文件。

### 播放链路

1. Emby 读取 `.strm`，拿到稳定的 `/strm/openlist/...` 地址。
2. 客户端请求 `PlaybackInfo`。
3. 如果开启 `redirect.fast_playback_info: true`，并且当前客户端允许直链播放，`emby-pro` 会直接取 item 元数据构造 `PlaybackInfo`；否则回源 Emby 原生 `PlaybackInfo`。
4. 只有当前客户端允许直链播放时，`emby-pro` 才会把 `MediaSources.Path` 和 `DirectStreamUrl` 改写成带短效票据的受控入口。
5. 如果 `redirect.direct_play_web: false`，Emby Web 不会走 fast path，也不会被改写到直链入口。
6. 客户端开始播放，请求带票据的 `/strm/openlist/...?...t=...`。
7. `emby-pro` 校验票据，调用 OpenList `/api/fs/get`。
8. `emby-pro` 返回 OpenList 当前可用的 `/d/...?...sign=...`，客户端完成播放。

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

配置入口已经统一为 `/config/app.yml` 和 `/admin`。下面列的是当前 YAML 字段，不再通过 emby-pro 业务环境变量驱动。

| Field | Default | Description |
| --- | --- | --- |
| `openlist.base_url` | `http://127.0.0.1:5244` | OpenList API 地址，供 `emby-pro` 服务端访问 |
| `openlist.public_url` | empty | 客户端可访问的 OpenList 外部地址，仅用于生成 302 下载链接；未设置时回退到 `openlist.base_url` |
| `openlist.token` | empty | OpenList API token；生产环境推荐 |
| `openlist.username` / `openlist.password` | empty | 未设置 token 时的登录用户名和密码 |
| `openlist.request_timeout` | `15s` | OpenList 请求超时 |
| `openlist.retry` | `3` | OpenList 请求重试次数 |
| `openlist.retry_backoff` | `2s` | OpenList 请求重试退避 |
| `openlist.list_per_page` | `200` | OpenList 目录分页大小 |
| `openlist.rate_limit_qps` | `0` | `.strm` 同步链路额外限速，`0` 表示关闭 |
| `openlist.rate_limit_burst` | `1` | `.strm` 同步链路额外限速的突发值 |
| `openlist.insecure_skip_verify` | `false` | 跳过 TLS 校验 |
| `openlist.disable_http2` | `false` | 禁用 HTTP/2 |
| `emby.base_url` | `http://127.0.0.1:8096` | Emby 上游地址；如果 Emby 部署在子路径如 `/emby` 下，这里应填写完整前缀 |
| `emby.validate_path` | `/System/Info` | token 校验使用的 Emby 路径 |
| `emby.request_timeout` | `15s` | Emby 请求超时 |
| `emby.token_cache_ttl` | `30s` | Emby token 校验缓存时间 |
| `redirect.direct_play` | `true` | 是否总开关启用直链播放改写 |
| `redirect.direct_play_web` | `false` | 是否允许 Emby Web 走直链播放 |
| `redirect.fast_playback_info` | `false` | 是否启用 fast `PlaybackInfo` |
| `redirect.direct_play_users` | empty | 仅对指定 Emby 用户启用 direct play，支持用户 ID 或用户名 |
| `redirect.listen_addr` | `:28096` | redirect/http 服务监听地址 |
| `redirect.public_url` | `http://127.0.0.1:28096` | `.strm` 稳定地址和播放阶段回退用的外部地址 |
| `redirect.play_ticket_secret` | auto-generated | 用于签发短效播放票据的密钥 |
| `redirect.play_ticket_ttl` | `12h` | 播放票据有效期 |
| `redirect.route_prefix` | `/strm` | 受控播放入口前缀 |
| `sync.base_dir` | `/strm` | `.strm` 输出根目录 |
| `sync.index_db` | `/config/strm-index.db` | SQLite 索引路径 |
| `sync.full_rescan_interval` | `24h` | 全量校准周期 |
| `sync.max_dirs_per_cycle` | `200` | 每轮最多扫描目录数 |
| `sync.max_requests_per_cycle` | `1000` | 每轮最多 OpenList API 请求数 |
| `sync.min_file_size` | `15728640` | 最小视频文件大小，单位字节 |
| `sync.video_exts` | common video exts | 允许生成 `.strm` 的视频扩展名 |
| `sync.clean_removed` | `true` | 清理已从 OpenList 消失的 `.strm` |
| `sync.overwrite` | `false` | 强制重写本轮扫描命中的 `.strm`；关闭后仅在内容或路径变化时写盘 |
| `sync.log_level` | `info` | 日志级别，当前支持 `info` / `debug` |
| `sync.hot_interval` | `30m` | 新增或刚变化目录的基础扫描周期 |
| `sync.warm_interval` | `6h` | 稳定目录的中频扫描周期 |
| `sync.cold_interval` | `24h` | 长期不变目录的低频扫描周期 |
| `sync.hot_jitter` | `10m` | `hot` 周期的随机抖动 |
| `sync.warm_jitter` | `1h` | `warm` 周期的随机抖动 |
| `sync.cold_jitter` | `4h` | `cold` 周期的随机抖动 |
| `sync.unchanged_to_warm` | `3` | 连续多少次目录无变化后进入 `warm` |
| `sync.unchanged_to_cold` | `7` | 连续多少次目录无变化后进入 `cold` |
| `sync.failure_backoff_max` | `24h` | 普通失败退避的最大值 |
| `sync.rule_cooldown` | `6h` | 检测到 `429` 或风控信号后的规则级冷却时间 |
| `rules[*].source_path` | required | OpenList 来源目录 |
| `rules[*].target_path` | auto from source | `.strm` 输出目录；可自定义来源和目标路径 |
| `rules[*].flatten` | `false` | 是否把输出扁平化 |
| `rules[*].include_regex` / `rules[*].exclude_regex` | empty | 规则级过滤 |
| `rules[*].clean_removed` | inherit from `sync.clean_removed` | 规则级删除策略 |
| `rules[*].overwrite` | inherit from `sync.overwrite` | 规则级重写策略 |

典型场景：

- 内网 API 访问 OpenList：`openlist.base_url: http://openlist:5244`
- 对外 302 下载地址：`openlist.public_url: https://list.example.com`
- 自定义来源和输出目录：把 `rules[*].source_path` 设为 OpenList 源路径，把 `rules[*].target_path` 设为 Emby 挂载目录

OpenList 保守同步推荐：

- `openlist.rate_limit_qps: 0.2`
- `openlist.rate_limit_burst: 1`
- `sync.full_rescan_interval: 24h`
- `sync.max_dirs_per_cycle: 20`
- `sync.max_requests_per_cycle: 60`
- `sync.hot_interval: 30m`
- `sync.warm_interval: 6h`
- `sync.cold_interval: 24h`

说明：

- emby-pro 现在默认使用自适应目录调度，`hot / warm / cold`、失败退避和规则级冷却共同决定每个目录的下次扫描时间。
- `openlist.rate_limit_*` 只作用于 `.strm` 同步扫描链路；播放阶段为获取直链调用 OpenList `/api/fs/get` 时不再额外套一层 emby-pro 限流。
- 当前版本不再提供 `sync profile` 预设，直接按 YAML 字段调整同步策略。

## 规则文件

规则直接写在 `/config/app.yml` 的 `rules` 里：

```yaml
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

`overwrite` 表示是否强制重写本规则命中的 `.strm`。工具始终会接管并覆盖未纳入索引的同名目标文件；只有已追踪且内容未变化的文件，才会在 `overwrite: false` 时跳过重复写盘。

最小文件大小通过 `sync.min_file_size` 全局控制，默认 `15728640`。

示例文件见 [examples/app.yml](/Users/monlor/Workspace/emby-pro/examples/app.yml)。

## 项目结构

- [cmd/strm-sync](/Users/monlor/Workspace/emby-pro/cmd/strm-sync)：入口程序，启动同步器和 redirect 服务
- [internal/config](/Users/monlor/Workspace/emby-pro/internal/config)：YAML 配置、规则和校验
- [internal/syncer](/Users/monlor/Workspace/emby-pro/internal/syncer)：扫描 OpenList、生成 `.strm`、维护索引
- [internal/redirect](/Users/monlor/Workspace/emby-pro/internal/redirect)：`/strm/openlist` 路由、播放票据、`PlaybackInfo` 改写、302 回源
- [internal/openlist](/Users/monlor/Workspace/emby-pro/internal/openlist)：OpenList API 客户端
- [internal/emby](/Users/monlor/Workspace/emby-pro/internal/emby)：Emby API 客户端
- [internal/index](/Users/monlor/Workspace/emby-pro/internal/index)：本地 SQLite 索引
- [examples](/Users/monlor/Workspace/emby-pro/examples)：配置示例
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
