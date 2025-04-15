# Emby Pro

> 基于 [AutoFilm](https://github.com/Akimio521/AutoFilm) 和 [MediaWarp](https://github.com/Akimio521/MediaWarp) 的一站式流媒体解决方案，实现 Emby + Alist 的完美结合。

## 项目特点

- 自动扫描 Alist 存储中的媒体文件并生成 .strm 文件
- 通过 MediaWarp 代理 Emby 服务，实现直链播放
- 支持 Alist 多种存储后端（115网盘、阿里云盘等）
- 支持 Emby 媒体库管理、刮削和元数据管理
- 提供 Web 界面美化功能
- 支持字幕转换和优化
- 支持多设备同步播放进度

## 快速开始

### 1. 下载配置文件

```bash
mkdir emby-pro && cd emby-pro
curl -O https://raw.githubusercontent.com/your-username/emby-pro/main/docker-compose.yml
```

### 2. 配置环境变量

编辑 `docker-compose.yml` 文件，设置以下必需的环境变量：

```yaml
services:
  autofilm:
    environment:
      - ALIST_URL=http://alist:5244
      - ALIST_USERNAME=your_alist_username
      - ALIST_PASSWORD=your_alist_password
      - SOURCE_PATHS=电影:/115网盘/视频影音/电影
      - CRON="0 2 * * *"

  mediawarp:
    environment:
      - MEDIA_SERVER_ADDR=http://emby:8096
      - MEDIA_SERVER_AUTH=your_emby_auth_token
```

### 3. 启动服务

```bash
docker-compose up -d
```

### 4. 配置 Alist

1. 访问 Alist 管理界面：`http://your-server-ip:5244`
2. 登录并添加存储
3. 获取 Alist 永久令牌（可选）

### 5. 配置 Emby

1. 访问 Emby 管理界面：`http://your-server-ip:8096`
2. 完成初始设置
3. 获取 Emby API 密钥：
   - 进入管理后台
   - 点击 "高级" -> "API 密钥"
   - 创建新的 API 密钥

### 6. 更新配置

使用获取到的 Alist 令牌和 Emby API 密钥更新 `docker-compose.yml`：

```yaml
services:
  autofilm:
    environment:
      - ALIST_TOKEN=your_alist_token  # 如果使用令牌认证

  mediawarp:
    environment:
      - MEDIA_SERVER_AUTH=your_emby_api_key
```

### 7. 重启服务

```bash
docker-compose down
docker-compose up -d
```

### 8. 访问服务

所有服务启动完成后，可以通过以下地址访问：

- Emby 媒体库：`http://localhost:9000`（通过 MediaWarp 代理访问）
- Alist 管理界面：`http://localhost:5244`

> 注意：请使用 MediaWarp 代理地址（http://localhost:9000）访问 Emby，这样可以获得直链播放和额外的功能支持。

## 服务说明

### AutoFilm

[点击查看全部环境变量文档](/autofilm/README.md)

- 自动扫描 Alist 存储中的媒体文件
- 生成 Emby 可识别的 .strm 文件
- 支持定时更新
- 支持多个源路径配置

### MediaWarp

[点击查看全部环境变量文档](/mediawarp/README.md)

- 代理 Emby 服务，实现直链播放
- 优化播放体验
- 支持 Web 界面美化
- 支持字幕转换
- 支持客户端过滤

### Emby

- 提供媒体库管理
- 支持多设备同步
- 支持刮削和元数据管理
- 支持播放进度同步

### Alist

- 提供统一的存储管理
- 支持多种存储后端
- 支持 WebDAV 访问
- 支持永久令牌认证

## 高级配置

### AutoFilm 配置

默认识别所有alist文件，通过环境变量配置 AutoFilm 识别的文件路径

```yaml
services:
  autofilm:
    environment:
      - SOURCE_PATHS=电影:/115网盘/视频影音/电影,电视剧:/115网盘/视频影音/电视剧
      - CRON="0 2 * * *"  # 每天凌晨 2 点更新
```

### MediaWarp 配置

通过环境变量配置 MediaWarp：

```yaml
services:
  mediawarp:
    environment:
      - PORT=9000  # 修改代理端口
      - WEB_CRX=True
      - WEB_DANMAKU=True
      - WEB_VIDEO_TOGETHER=True
```

## 注意事项

1. 确保服务器有足够的存储空间
2. 建议使用永久令牌进行 Alist 认证
3. 定期备份 Emby 配置
4. 监控服务运行状态
5. 注意网络带宽使用情况
6. 访问 Emby 时请使用 MediaWarp 代理地址（http://localhost:9000）

## 常见问题

1. **如何查看服务日志？**
   ```bash
   docker-compose logs -f [service_name]
   ```

2. **如何更新服务？**
   ```bash
   docker-compose pull
   docker-compose up -d
   ```

## 许可证

本项目基于 [AGPL-3.0](LICENSE) 许可证开源。

## 致谢

- [AutoFilm](https://github.com/Akimio521/AutoFilm)
- [MediaWarp](https://github.com/Akimio521/MediaWarp)
- [Emby](https://emby.media/)
- [Alist](https://alist.nn.ci/) 