# AutoFilm 配置生成器

> 本项目基于 [AutoFilm](https://github.com/Akimio521/AutoFilm) 开发，一个为 Emby、Jellyfin 服务器提供直链播放的小项目。

此脚本根据环境变量自动生成 AutoFilm 的 `config.yaml` 配置文件。

## 必需的环境变量

| 变量名 | 描述 | 示例 |
|--------|------|------|
| `ALIST_URL` | Alist 服务器地址 | `https://alist.example.com` |
| `ALIST_USERNAME` | Alist 用户名 | `admin` |
| `ALIST_PASSWORD` | Alist 密码 | `password123` |

## 可选的环境变量

| 变量名 | 描述 | 默认值 |
|--------|------|--------|
| `ALIST_TOKEN` | Alist 永久令牌（如果提供，将忽略用户名/密码） | `""` |
| `CRON` | 自动更新的定时任务设置 | `0 2 * * *` (每天凌晨2点) |
| `SOURCE_PATHS` | 源路径列表，可包含目标子目录 | `""` |
| `FLATTEN_MODE` | 是否启用扁平模式 | `False` |
| `SUBTITLE` | 是否下载字幕文件 | `False` |
| `IMAGE` | 是否下载图片文件 | `False` |
| `NFO` | 是否下载 .nfo 文件 | `False` |
| `MODE` | Strm 文件内容模式 (AlistURL/RawURL/AlistPath) | `AlistURL` |
| `OVERWRITE` | 是否覆盖已存在的文件 | `False` |
| `SYNC_SERVER` | 是否与服务器同步 | `True` |
| `SYNC_IGNORE` | 同步时忽略的文件正则表达式 | `\.(nfo\|jpg)$` |
| `MAX_WORKERS` | 最大并发工作线程数 | `50` |
| `MAX_DOWNLOADERS` | 最大同时下载数 | `5` |
| `WAIT_TIME` | 请求之间的等待时间（秒） | `0` |

## 使用示例

### 基本用法（扫描整个 Alist）
```bash
export ALIST_URL="https://alist.example.com"
export ALIST_USERNAME="admin"
export ALIST_PASSWORD="password"
./entrypoint.sh
```

### 指定源路径
```bash
export ALIST_URL="https://alist.example.com"
export ALIST_USERNAME="admin"
export ALIST_PASSWORD="password"
export SOURCE_PATHS="电影:/网盘/电影,电视剧:/网盘/电视剧"
./entrypoint.sh
```

### 使用 Alist 令牌替代用户名/密码
```bash
export ALIST_URL="https://alist.example.com"
export ALIST_TOKEN="your-token-here"
./entrypoint.sh
```

### 自定义定时任务
```bash
export CRON="0 3 * * *"  # 改为凌晨3点运行
```

## 源路径格式说明

`SOURCE_PATHS` 环境变量接受以下格式的路径：
```
源路径1:目标目录1,源路径2:目标目录2,源路径3
```

其中：
- `源路径1`, `源路径2`, `源路径3` 是 Alist 服务器中的路径
- `目标目录1`, `目标目录2` 是 `/media` 下的可选子目录
- 如果未指定目标目录，文件将直接放置在 `/media` 目录下

示例：
- `电影:/网盘/电影` - `/网盘/电影` 中的文件将被放置在 `/media/电影` 目录下
- `电视剧:/网盘/电视剧` - `/网盘/电视剧` 中的文件将被放置在 `/media/电视剧` 目录下
- `动漫` - `/动漫` 中的文件将被直接放置在 `/media` 目录下

## 注意事项

1. 脚本将在 `autofilm` 目录下创建 `config.yaml` 文件
2. 如果未提供 `SOURCE_PATHS`，将创建一个扫描整个 Alist 根目录的条目
3. 所有可选参数都有合理的默认值，除非需要更改，否则无需设置
4. 如果缺少任何必需的环境变量，脚本将报错并退出 