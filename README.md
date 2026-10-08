一个pivix batch downloader的go后端

### 一、 核心流程与 curl 概念实现

Pixiv 的 Web 端有非常标准且清晰的 AJAX JSON 接口，整个过程只需要 3 次 HTTP 交互逻辑：

#### 1. 身份认证与凭证

由于书签（特别是包含非公开书签或 R-18 内容）需要登录态，最简单的方式是获取登录后的 **Cookie**（主要是 `PHPSESSID`）。

#### 2. 获取收藏夹列表（Bookmarks ID）

Pixiv 提供了分页接口来获取用户的收藏列表：

- **接口**：`GET https://www.pixiv.net/ajax/user/{user_id}/illusts/bookmarks?tag=&offset={offset}&limit={limit}&rest=show`
- **请求头**：需要带上 `Cookie: PHPSESSID=你的session;`
- **返回**：JSON 数据，在 `body.works` 列表中可以遍历拿到每个作品的 `id`，以及多图数量 `pageCount`。

#### 3. 获取高清原图链接（Pages）

如果一个作品有多张图（图集或漫画），可以通过 pages 接口获取每一页的原图地址：

- **接口**：`GET https://www.pixiv.net/ajax/illust/{illust_id}/pages`
- **返回**：JSON 数据，结构如下：
  ```json
  {
    "body": [
      {
        "urls": {
          "original": "https://i.pximg.net/img-original/img/..._p0.jpg"
        }
      },
      {
        "urls": {
          "original": "https://i.pximg.net/img-original/img/..._p1.png"
        }
      }
    ]
  }
  ```

#### 4. 使用 curl 下载高清原图（⚠️ 最关键的一步）

Pixiv 的图片 CDN（`i.pximg.net`）有严格的**防盗链机制**。如果你直接用浏览器打开图片 URL 或直接发起 GET 请求，会返回 **`403 Forbidden`**。

**只要带上 `Referer`，curl 就能直接下载**：

```bash
curl -H "Referer: https://www.pixiv.net/" \
     -o output_p0.jpg \
     "https://i.pximg.net/img-original/img/2023/..._p0.jpg"
```

这个 `Referer: https://www.pixiv.net/` 标头是下载成功的唯一硬性门槛。

---

### 三、 开发时需要注意的坑

1. **频率限制（Rate Limiting）与防封**：
   - Pixiv 对频繁请求有速率限制。不要使用几十个并发线程去狂刷接口，容易收到 `429 Too Many Requests` 甚至触发 Cloudflare 验证码屏蔽 IP。
   - **建议**：每次请求作品详情或下载图片之间，设置一个微小的延迟（例如 `300ms ~ 800ms`），并限制并发数（比如同时下载 2~3 个文件）。
2. **动图（Ugoira）的处理**：
   - Pixiv 的动图不是 GIF，也不是常规的视频，而是一组打包的 zip 帧序列帧 + 每帧延迟时间的 JSON。
   - 如果作品的 `illustType == 2`（动图），`pages` 接口可能不适用，需要调用 `/ajax/illust/{id}/ugoira_meta` 拿到 zip 包和播放延时。如果初期想降低复杂度，可以先跳过动图。
3. **断点续传与重试**：
   - 图片体积较大（尤其是原图 PNG 动辄 10MB+），网络不稳定时容易中断，建议在下载器中加上失败重试逻辑（Retry 2~3 次）。
