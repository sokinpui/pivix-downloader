# Pixiv Downloader Backend

一个专为独立前端（Vue / React / Flutter 等）设计的、高可用、防封控、可恢复的 Pixiv 自动化批量下载服务后端。采用 Go 标准库 `net/http` + 纯 Go SQLite (`modernc.org/sqlite`)，无 CGO 编译依赖。

## 架构特性

- **独立后端服务**：剥离前端展示代码，通过完备的 RESTful API 与 Server-Sent Events (SSE) 实时事件流与任意前端对接。
- **纯 Go SQLite 存储**：零 CGO 依赖，内置 `pixiv.db` 数据库与表自动迁移，支持任务状态持久化、断点恢复。
- **增量书签同步与早停机制**：支持增量扫描书签，若遇到连续 `N` 个已下载画作，立即触发早停，防止触发 Pixiv 深度翻页频控。
- **独立链接解析**：支持多种 Pixiv 分享链接（`artworks/{id}`、`member_illust.php`、纯数字 ID）智能解析与单作品录入。
- **限速保护与原子落盘**：内置随机速率保护（`300ms ~ 600ms`），下载采用 `.tmp` 临时文件 + `os.Rename` 原子重命名，防止半截破损图片残留。
- **SSE 实时状态推送**：实时广播发现作品、下载开始、下载完成、下载失败等事件。

---

## 快速运行

```bash
# 编译并运行
go build -v .
./pixiv-downloader -port 8080 -session "你的PHPSESSID" -user-id "你的用户ID"
```

## API 接口规范

所有响应统一采用 JSON Envelope 封装：
```json
{
  "code": 0,
  "message": "success",
  "data": {}
}
```

### 1. 配置管理
- `GET /api/settings`: 获取系统配置（Session ID 脱敏、User ID、下载目录、代理等）。
- `PUT /api/settings`: 更新系统配置。

### 2. 任务触发
- `POST /api/sync/bookmarks`: 触发增量书签同步（请求体可传 `{"force_full": false, "user_id": "..."}`）。
- `POST /api/artworks/submit`: 提交单作品链接/ID（请求体 `{"url_or_id": "https://www.pixiv.net/artworks/..."}`）。
- `POST /api/tasks/{task_id}/retry`: 重新投递失败的任务。

### 3. 数据查询
- `GET /api/artworks`: 获取画作列表（支持 `?status=completed&source=bookmark&page=1&limit=20`）。
- `GET /api/artworks/{id}`: 获取单作品详情及所有分图下载状态。

### 4. 实时状态推送
- `GET /api/events`: SSE 实时事件流（`Content-Type: text/event-stream`）。
