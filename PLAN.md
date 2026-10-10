### 1. Abstract

本方案专为独立后端服务设计，剥离所有前端展示代码，聚焦于构建高可用、防封控、可恢复的 Pixiv 自动化批量下载服务。后端基于 Go 标准库 HTTP 服务 + 纯 Go SQLite（无 CGO 依赖），提供完备的 RESTful API 与 Server-Sent Events (SSE) 实时事件流，供任意独立前端（Vue/React/Flutter 等）对接。

---

### 2. 总体架构与模块分层 (System Architecture)

```
                       [ 外部 Web / 移动端前端 ]
                                   │ HTTP (RESTful / SSE)
                                   ▼
┌────────────────────────────────────────────────────────────────────────┐
│                          HTTP Transport Layer                          │
│   - Router (Go 1.22+ net/http)                                         │
│   - Middleware (CORS, Recover, Logger)                                 │
│   - Handlers (Settings, Sync, Submit, Task, Query, SSE Hub)            │
└──────────────────────────────────┬─────────────────────────────────────┘
                                   │
                                   ▼
┌────────────────────────────────────────────────────────────────────────┐
│                             Service Layer                              │
│   - SyncService: 增量书签扫描算法与早停策略                            │
│   - SubmissionService: 作品链接/ID 解析与入库                         │
│   - EventHub: 进程内发布订阅，向 SSE 推送下载状态变更                  │
└──────────────────┬───────────────────────────────┬─────────────────────┘
                   │                               │
                   ▼                               ▼
┌─────────────────────────────────────┐ ┌────────────────────────────────┐
│         Download Engine             │ │       Repository Layer         │
│   - Bounded Worker Pool (Channel)   │ │   - SQLite (modernc.org/sqlite)│
│   - Token Bucket / Rate Limiter     │ │   - SettingsRepo               │
│   - Atomic File Writer (.tmp -> dst)│ │   - ArtworkRepo                │
│   - Pixiv Client (Referer/Session)  │ │   - DownloadTaskRepo           │
└─────────────────────────────────────┘ └────────────────────────────────┘
```

---

### 3. 数据模型设计 (SQLite Schema)

采用纯 Go 驱动 `modernc.org/sqlite`，避免 CGO 编译依赖，数据文件命名为 `pixiv.db`。

#### 3.1 表结构与索引

```sql
-- 系统运行时配置
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 画作元数据表（排重与归档基准）
CREATE TABLE IF NOT EXISTS artworks (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    user_id TEXT NOT NULL,
    user_name TEXT NOT NULL DEFAULT '',
    page_count INTEGER NOT NULL DEFAULT 1,
    illust_type INTEGER NOT NULL DEFAULT 0,
    source_type TEXT NOT NULL,                -- 'bookmark' | 'manual'
    status TEXT NOT NULL,                     -- 'pending' | 'processing' | 'completed' | 'partial' | 'failed'
    error_message TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_artworks_status ON artworks(status);
CREATE INDEX IF NOT EXISTS idx_artworks_created_at ON artworks(created_at DESC);

-- 具体单图下载任务表
CREATE TABLE IF NOT EXISTS download_tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    artwork_id TEXT NOT NULL,
    page_index INTEGER NOT NULL,
    image_url TEXT NOT NULL,
    file_path TEXT NOT NULL DEFAULT '',
    file_size INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL,                     -- 'pending' | 'downloading' | 'completed' | 'failed'
    retry_count INTEGER NOT NULL DEFAULT 0,
    error_message TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at DATETIME,
    FOREIGN KEY(artwork_id) REFERENCES artworks(id) ON DELETE CASCADE,
    UNIQUE(artwork_id, page_index)
);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON download_tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_artwork_id ON download_tasks(artwork_id);
```

---

### 4. 核心核心机制设计 (Core Mechanics)

#### 4.1 增量书签同步算法 (Incremental Bookmark Sync)

1. **分页拉取**：从 `offset=0, limit=48` 开始拉取书签列表。
2. **早停机制 (Early Exit)**：
   - 遍历拉取到的每条 `artwork_id`；
   - 检查本地数据库状态：若遇到连续 `N` 个画作（建议配置默认 10 个）状态已是 `completed`，判定后续书签已全部下载过，**立即中断分页扫描**，避免触发 Pixiv 深度翻页频控。
3. **入库与建任**：对未入库画作，异步调用 `FetchPages`，批量向 `download_tasks` 写入 `pending` 任务，并放入内存调度通道。

#### 4.2 独立链接提取 (Share Link Parser)

编写正则提取器，兼容以下任意格式输入：

- `https://www.pixiv.net/artworks/147508896`
- `https://www.pixiv.net/en/artworks/147508896`
- `https://www.pixiv.net/member_illust.php?mode=medium&illust_id=147508896`
- 纯数字 `147508896`

#### 4.3 下载执行器与速率保护 (Worker & Rate Limiting)

- **并发控制**：启动固定容量的 Worker Pool（默认 2 个 Worker）。
- **延时节流**：使用基于时间的令牌控制（每发出一次 Pixiv 请求，强制随机休眠 `300ms ~ 600ms`）。
- **原子落盘**：
  1. 下载流先写入目标路径同级临时文件：`${filename}.tmp`；
  2. 写入校验完整后，调用 `os.Rename` 重命名为目标文件名；
  3. 防止下载中断导致残留损坏半截图片。
- **重试退避**：单图下载失败重试上限为 3 次，采用退避延迟。

---

### 5. API 接口规范 (Contracts for Frontend)

所有响应统一采用 JSON Envelope 封装：

```json
{
  "code": 0,
  "message": "success",
  "data": {}
}
```

#### 5.1 配置管理

- `GET /api/settings`
  - 返回系统配置（`session_id` 脱敏掩码、`user_id`、`download_dir`、`proxy`、`max_workers`）。
- `PUT /api/settings`
  - 请求体：更新上述配置，保存到 SQLite 并动态重载 Pixiv Client 与调度器。

#### 5.2 任务触发

- `POST /api/sync/bookmarks`
  - 请求体：`{"force_full": false}`（可选强制全量而不早停）。
  - 响应：`{"code": 0, "message": "sync triggered in background"}`。
- `POST /api/artworks/submit`
  - 请求体：`{"url_or_id": "https://www.pixiv.net/artworks/147508896"}`。
  - 响应：解析出的元数据与排队状态。
- `POST /api/tasks/{task_id}/retry`
  - 重新将失败的任务投递至队列。

#### 5.3 数据查询

- `GET /api/artworks`
  - 参数：`?status=completed&source=bookmark&page=1&limit=20`。
  - 返回画作列表及其关联的 page tasks 下载状态。
- `GET /api/artworks/{id}`
  - 获取单作品详情及所有分图本地路径。

#### 5.4 实时状态推送 (SSE)

- `GET /api/events`（`Content-Type: text/event-stream`）
  - 前端连接后，后端广播实时事件：
    - `artwork_discovered`: 发现新作品
    - `task_started`: 开始下载某张图（含 artwork_id, page_index）
    - `task_completed`: 下载完成（含文件路径、大小）
    - `task_failed`: 下载失败（含错误信息）
    - `sync_finished`: 本次书签扫描同步完毕

---

### 6. 项目代码目录规划 (Go Project Layout)

```
pixiv-downloader/
├── go.mod
├── go.sum
├── main.go                       # 应用程序启动入口 (组装各层、优雅关机)
├── internal/
│   ├── config/                   # 配置加载与 SQLite 配置联动
│   ├── database/                 # SQLite 连接初始化与表迁移
│   ├── model/                    # Artwork, DownloadTask, Setting 等结构体
│   ├── repository/               # 数据库 CRUD 操作封装
│   ├── pixiv/                    # 纯净的 Pixiv API Client (限速器, HTTP)
│   ├── engine/                   # Worker Pool、任务调度、原子下载逻辑
│   ├── service/                  # 增量扫描、链接解析等核心业务编排
│   └── handler/                  # HTTP 路由、REST API Handler、SSE Hub、CORS
└── downloads/                    # 默认下载存储目录 (可配置)
```

---

### 7. AI Agent 逐步实现执行清单 (Step-by-Step Milestones)

- [ ] **Milestone 1: 基础骨架与存储集成**
  - 安装依赖 `modernc.org/sqlite`。
  - 编写 `internal/database`，实现 SQLite 初始化与自执行 DDL 脚本。
  - 编写 `internal/model` 与 `internal/repository`，实现 Settings、Artworks、Tasks 的数据访问方法。
- [ ] **Milestone 2: Pixiv Client 完善与健壮下载**
  - 将现有 `client.go` 迁移重构至 `internal/pixiv`。
  - 增加作品详情/动图类型识别与单页 URL 解析。
  - 添加带 Referer 的原子下载实现（支持 `.tmp` 写入与原子重命名）。
- [ ] **Milestone 3: 调度引擎与增量同步**
  - 编写 `internal/engine/pool.go`，构建 channel 任务队列与固定并发 Worker。
  - 编写 `internal/service/sync.go`，实现基于本地数据库状态的增量早停书签扫描。
  - 编写 `internal/service/submit.go`，实现 URL 正则解析与单作品任务注入。
- [ ] **Milestone 4: Web 接口与 SSE 推送**
  - 编写 `internal/handler/sse.go`，实现线程安全的 SSE 广播器（Hub）。
  - 实现 RESTful 接口路由及 CORS 中间件，支持跨域让前端独立开发调试。
- [ ] **Milestone 5: 组装与优雅退出**
  - 在 `main.go` 中处理 `os.Interrupt` / `SIGTERM`，确保服务终止时正在写入的文件平稳完成，关闭 SQLite 连接。
