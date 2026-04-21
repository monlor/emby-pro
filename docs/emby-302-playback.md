# Emby 302 直链播放实现原理

## 目的

这份文档说明 `emby-pro` 当前是如何实现 Emby 的 302 直链播放链路的，以及为什么实现时不能只改一个点，例如“只改 `DirectStreamUrl`”或者“只在 `/strm` 上做 302”。

文档重点回答这几个问题：

- `.strm` 文件里到底应该写什么
- Emby 播放时真正依赖的是哪些字段
- 为什么 `MediaSources.Path` 也必须参与改写
- 为什么 `DirectStreamUrl` 也要一起改
- 为什么路由要改成 `/strm/openlist/...`
- 为什么要用播放票据，而不是长期可复用的 `/strm` 地址

## 参考思路

当前方案不是凭空设计，核心思路参考了两个方向：

- `go-emby2openlist`
  - 仓库: <https://github.com/AmbitiousJun/go-emby2openlist>
  - 这个项目的 302 / 代理思路说明了一个关键点: 真正的直链播放并不只是“给客户端返回 302”，而是要拦截 Emby 的播放相关 API，尤其是 `PlaybackInfo`，并维护 Emby 本身能理解的媒体路径和挂载关系。
- `SmartStrm`
  - 仓库: <https://github.com/Cp0204/SmartStrm>
  - 这个项目把“生成 `.strm` 文件”和“运行时跳转/播放处理”拆成两层，这和我们当前采用的“稳定系统地址 + 播放时动态换票”思路是一致的。

我们的实现不是逐行复刻这两个项目，而是吸收其中两个核心原则：

1. `.strm` 写盘层和播放时跳转层必须解耦
2. Emby 播放链路里，`Path` 不是纯展示字段，`PlaybackInfo` 也不是可选环节

## 总体链路

当前 `emby-pro` 的链路分三层：

1. 写盘层
   - `.strm` 文件只写稳定的系统地址
   - 当前 OpenList 对接固定写成 `/strm/openlist/<source_path>`
   - 例如:

   ```text
   http://127.0.0.1:28096/strm/openlist/movies/demo.mp4
   ```

2. 授权层
   - 客户端请求 Emby `PlaybackInfo` 时，`emby-pro` 拦截并改写返回内容
   - 如果开启 `OPENLIST_FAST_PLAYBACKINFO=true`，并且当前客户端允许直链播放，`emby-pro` 会直接基于 item 元数据构造最小可用 `PlaybackInfo`
   - fast path 返回的响应仍会补齐 `PlaySessionId`，以保证后续 `/Sessions/Playing`、播放进度和续播链路可用
   - `emby-pro` 生成短效播放票据 `t`
   - 票据 URL 形态是:

   ```text
   /strm/openlist/movies/demo.mp4?t=<short-lived-ticket>
   ```

3. 回源层
   - 客户端真正播放时，请求的是带票据的 `/strm/openlist/...`
   - `emby-pro` 校验票据后，调用 OpenList `/api/fs/get`
   - 再返回当前可用的 OpenList `/d/...?...sign=...`
   - 这个 OpenList 地址的有效期由 OpenList 自己控制

所以当前方案里有两个不同层级的地址：

- 稳定地址: `.strm` 文件里的 `/strm/openlist/...`
- 短效地址: 播放阶段签发的 `/strm/openlist/...?...t=...`

稳定地址只负责“可定位”；短效地址才负责“可播放”。

## 为什么不能只做一个 302

很多人理解 302 直链播放时，会把它简化成：

- `.strm` 指向一个本地地址
- 这个地址收到请求后返回 302
- 完成

这在浏览器里看起来能跑，但对 Emby 不够。

原因是 Emby 播放不是单次 URL 请求，而是一整套播放协商流程：

- 先取 `PlaybackInfo`
- 再根据 `MediaSources` 选择播放方式
- 可能走 `DirectPlay`
- 可能走 `DirectStream`
- 可能退回服务端 ffmpeg / 转码
- 同一个媒体源在不同客户端上的访问入口也可能不同

所以 302 只是最后一步的数据面行为，不是控制面的全部。

如果不处理 `PlaybackInfo`，就会出现这些问题：

- 客户端仍然持有旧的播放入口
- Emby 服务端内部 ffmpeg 请求无法复用你的鉴权链路
- 同一个媒体源在 `Path` 和 `DirectStreamUrl` 上出现两套不一致地址
- 一部分客户端走到了新链路，一部分还停留在旧链路

另外，`PlaybackInfo` 也不能只返回一个“能播起来”的最小壳子。

像 `PlaySessionId` 这样的字段虽然不直接决定播放地址，但会影响 Emby 后续是否能正常接收 `/Sessions/Playing`、写入播放进度和支持续播。也就是说，fast path 可以不完全复制 Emby 原生 `PlaybackInfo`，但不能漏掉播放会话链路依赖的关键字段。

## `MediaSources.Path` 为什么必须修改

这次讨论里最容易被误解的就是这一点。

`MediaSources.Path` 不是单纯给前端展示文件来源的字符串。对 Emby 来说，它至少还有这几个作用：

- 它是媒体源的基础定位信息
- 它会影响 Emby 服务端内部如何理解这个媒体源
- 在非纯客户端直连的场景里，Emby 服务端仍可能使用它参与后续播放流程
- 一些依赖服务端文件语义的逻辑，例如字幕、转码、内部 ffmpeg 回源，都会受它影响

这也是为什么参考的 302 类项目里，都会强调“Emby 媒体库路径”和“外部挂载路径 / 对接路径”的对应关系，而不是只谈一个最终跳转 URL。

在 `emby-pro` 当前实现里，`Path` 必须改成带票据的 `/strm/openlist/...?...t=...`，原因有三点：

1. 非 direct-play 场景仍然需要一个受控的服务端入口
2. Emby 内部 ffmpeg / 转码请求通常拿不到用户 token，所以要靠播放前签发的短效票据放行
3. 如果 `Path` 还保留旧的稳定地址，那么就等于保留了一条长期可复用的后门入口

所以结论是：

- `Path` 不是“可改可不改”
- 在我们这套短效票据模型里，`Path` 必须改

## `DirectStreamUrl` 为什么也要修改

仅修改 `Path` 也不够。

当 `OPENLIST_DIRECT_PLAY=true` 时，客户端在很多场景下优先看的是 `DirectStreamUrl`。

如果 `DirectStreamUrl` 不改，会出现两类问题：

- 客户端 direct-play 仍然走旧地址，绕开短效票据
- `Path` 和 `DirectStreamUrl` 指向两套不同播放入口，行为不一致

因此当前实现是：

- 仅当当前客户端允许 direct play 时，改写 `MediaSources.Path`
- 同时把 `DirectStreamUrl` 也改成同一个短效 `/strm/openlist/...?...t=...`
- 如果客户端不允许 direct play，例如 `OPENLIST_DIRECT_PLAY_WEB=false` 下的 Emby Web，请求会回退到 Emby 原生 `PlaybackInfo`，不会走 fast path，也不会被改写成直链入口

这样 `Path` 和 `DirectStreamUrl` 最终都汇聚到同一个受控入口。

## 为什么 `/strm/openlist` 不是直接 `/strm`

把 OpenList 路由从 `/strm/<path>` 改成 `/strm/openlist/<path>`，不是为了好看，而是为了给“对接类型”留出稳定命名空间。

当前我们已经有两个层面的对象：

- 稳定系统入口
- 实际后端 provider

把 provider 放进路径后，路由语义会更清晰：

- `/strm/openlist/...` 表示后端是 OpenList
- 未来如果接入本地文件、WebDAV、115、Alist 兼容层，理论上都可以挂成
  - `/strm/local/...`
  - `/strm/webdav/...`
  - `/strm/<provider>/...`

这就是你前面提到“包括本地目录也做同样修改，为以后添加新的对接类型做预留”的意义。

也就是说，`/strm/openlist` 不是一个临时补丁路径，而是我们现在的 provider namespace。

## 为什么不能再用长期静态签名

旧模型的核心问题是：

- `.strm` 文件里如果直接写长期可复用的 `/strm?...s=...`
- 或者写长期有效的 OpenList 直链
- 任何拿到这个地址的人，都可以长期访问

这会直接破坏“播放授权”和“用户会话”之间的绑定。

当前改造后的原则是：

- `.strm` 文件中只允许稳定系统地址
- 真正的播放入口必须是短效票据 URL
- 票据必须在 PlaybackInfo 阶段签发
- `/strm/openlist/...` 在没有票据时不可播放

这样就把长期稳定定位地址和短期播放授权彻底拆开了。

## 当前实现的字段分工

可以把当前实现理解成下面这张表：

| 字段/入口 | 当前职责 |
| --- | --- |
| `.strm` 文件内容 | 稳定定位地址，不直接承担播放授权 |
| `/strm/openlist/...` | provider 级系统入口 |
| `/strm/openlist/...?...t=...` | 真正可播放的短效入口 |
| `MediaSources.Path` | 服务端可理解、可回源的受控媒体路径 |
| `DirectStreamUrl` | 客户端 direct-play 时使用的受控播放入口 |
| OpenList `/d/...?...sign=...` | 最终数据面跳转地址，由 OpenList 管理有效期 |

## 当前实现的取舍

相对于一些开源 302 实现，我们当前版本做了两个明确取舍：

1. 不再把长期签名写进 `.strm`
   - `.strm` 文件长期稳定
   - 播放权限只在运行时发放

2. 不再保留多套写盘模式
   - 不再让 `.strm` 直接写 OpenList `raw_url`
   - 不再保留 `url_mode`
   - 所有对接统一先收敛到系统地址，再在播放时处理

这个取舍的结果是：

- 配置面更简单
- 安全边界更清晰
- 以后新增 provider 更容易复用整套票据链路

## 结论

要正确实现 Emby 的 302 直链播放，不能只盯着一个点。

完整方案至少要同时处理这几件事：

1. `.strm` 只写稳定系统地址
2. `PlaybackInfo` 阶段动态签发短效播放票据
3. `MediaSources.Path` 改写到短效受控入口
4. `DirectStreamUrl` 在 direct-play 场景下也改到同一入口
5. `/strm/<provider>/...` 作为统一 provider namespace
6. 最终播放阶段再向 provider 换取当前可用直链并 302

少掉任何一层，都容易变成“看起来像 302，实际上链路不完整”的实现。
