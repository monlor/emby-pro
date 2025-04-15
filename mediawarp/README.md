# MediaWarp 配置生成器

> 本项目基于 [MediaWarp](https://github.com/Akimio521/MediaWarp) 开发，一个为 Emby、Jellyfin 服务器提供直链播放的小项目。

此脚本根据环境变量自动生成 MediaWarp 的 `config.yaml` 配置文件。

## 必需的环境变量

| 变量名 | 描述 | 示例 |
|--------|------|------|
| `MEDIA_SERVER_ADDR` | 媒体服务器地址 | `http://localhost:8096` |
| `MEDIA_SERVER_AUTH` | 媒体服务器认证信息 | `2eaxxxxxxxxxa8` |

## 可选的环境变量

### 基础配置
| 变量名 | 描述 | 默认值 |
|--------|------|--------|
| `PORT` | MediaWarp 监听端口 | `9000` |
| `MEDIA_SERVER_TYPE` | 媒体服务器类型（Emby/Jellyfin） | `Emby` |

### 日志配置
| 变量名 | 描述 | 默认值 |
|--------|------|--------|
| `ACCESS_LOGGER_CONSOLE` | 是否将访问日志输出到终端 | `True` |
| `ACCESS_LOGGER_FILE` | 是否将访问日志记录到文件 | `False` |
| `SERVICE_LOGGER_CONSOLE` | 是否将服务日志输出到终端 | `True` |
| `SERVICE_LOGGER_FILE` | 是否将服务日志记录到文件 | `True` |

### Web 界面配置
| 变量名 | 描述 | 默认值 |
|--------|------|--------|
| `WEB_ENABLE` | 是否启用 Web 界面 | `True` |
| `WEB_CUSTOM` | 是否加载自定义静态资源 | `False` |
| `WEB_INDEX` | 是否从 custom 目录读取 index.html | `False` |
| `WEB_CRX` | 是否启用 crx 美化 | `True` |
| `WEB_ACTOR_PLUS` | 是否过滤没有头像的演员 | `True` |
| `WEB_FANART_SHOW` | 是否显示同人图 | `False` |
| `WEB_EXTERNAL_PLAYER_URL` | 是否开启外置播放器 | `True` |
| `WEB_DANMAKU` | 是否启用 Web 弹幕 | `True` |
| `WEB_VIDEO_TOGETHER` | 是否启用共同观影 | `True` |

### 客户端过滤配置
| 变量名 | 描述 | 默认值 |
|--------|------|--------|
| `CLIENT_FILTER_ENABLE` | 是否启用客户端过滤器 | `False` |
| `CLIENT_FILTER_MODE` | 过滤模式（BlackList/WhiteList） | `BlackList` |

### HTTP Strm 配置
| 变量名 | 描述 | 默认值 |
|--------|------|--------|
| `HTTP_STRM_ENABLE` | 是否启用 HTTP Strm 重定向 | `False` |
| `HTTP_STRM_TRANSCODE` | 是否保持原有转码设置 | `False` |

### Alist Strm 配置
| 变量名 | 描述 | 默认值 |
|--------|------|--------|
| `ALIST_STRM_ENABLE` | 是否启用 Alist Strm 重定向 | `True` |
| `ALIST_STRM_TRANSCODE` | 是否保持原有转码设置 | `True` |
| `ALIST_STRM_RAW_URL` | 是否使用 Alist 上游真实链接 | `False` |
| `ALIST_ADDR` | Alist 服务器地址 | `http://192.168.1.100:5244` |
| `ALIST_USERNAME` | Alist 用户名（可选） | `""` |
| `ALIST_PASSWORD` | Alist 密码（可选） | `""` |
| `ALIST_TOKEN` | Alist 永久令牌（可选，优先级高于用户名密码） | `""` |

### 字幕配置
| 变量名 | 描述 | 默认值 |
|--------|------|--------|
| `SUBTITLE_ENABLE` | 是否启用字幕功能 | `True` |
| `SUBTITLE_SRT2ASS` | 是否将 SRT 字幕转换为 ASS 字幕 | `True` |

## 使用示例

### 基本用法
```bash
export MEDIA_SERVER_ADDR="http://localhost:8096"
export MEDIA_SERVER_AUTH="2eaxxxxxxxxxa8"
export ALIST_ADDR="http://192.168.1.100:5244"
./entrypoint.sh
```

### 使用 Alist 令牌认证
```bash
export MEDIA_SERVER_ADDR="http://localhost:8096"
export MEDIA_SERVER_AUTH="2eaxxxxxxxxxa8"
export ALIST_ADDR="http://192.168.1.100:5244"
export ALIST_TOKEN="your-token-here"
./entrypoint.sh
```

### 自定义端口
```bash
export PORT="8080"
```

## 注意事项

1. 脚本将在当前目录创建 `config.yaml` 文件
2. 所有可选参数都有合理的默认值，除非需要更改，否则无需设置
3. 如果缺少任何必需的环境变量，脚本将报错并退出
4. Alist 认证方式优先级：Token > 用户名密码
5. HTTP Strm 功能默认关闭，只使用 Alist Strm 功能
6. Alist Strm 的 PrefixList 固定为 `/media`
7. HTTP Strm 的 PrefixList 固定为 `/media`