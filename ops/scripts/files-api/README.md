# Files API — 用法 + 测试脚本

lazu 提供 OpenAI-compatible 的 **Files API**：你可以把文件（图片、PDF、数据集）上传到服务器，拿到 `file-xxx` ID，然后在其他 API 调用（比如 `/v1/responses` 多模态对话）里**用 ID 引用**那个文件——不需要每次把文件 base64 塞进 request body。

这个目录里的 5 个脚本就是用来：

1. **smoke 全链路测试**（CI 用 or 改完代码自测）
2. **ad-hoc 单次操作**（上传/列出/删除/演示）

---

## 0 · 配置对象存储（**必须**——一次性，跑任何脚本前先做）

`/v1/files` 把上传的字节存到对象存储（**不是数据库**——DB 只存元信息）。**没配存储后端的话所有 upload/download/delete 都返回 `503 storage_not_configured`**，脚本会全红。

推荐用 **Cloudflare R2** 或其他已验证的 S3 兼容存储。

### 0.1 · 在 R2 上建一个 bucket

1. 登录 https://dash.cloudflare.com → 左侧 **R2 Object Storage**
2. **Create bucket**
   - Name: `lazu-user-files`（或你想叫的名字）
   - Location: 任选（Auto 默认即可）
   - **保持 private**（不开公开访问）
3. 在 bucket 详情页 → **Settings** → **CORS Policy**：lazu 后端是服务端调用，**不需要 CORS 配置**，跳过
4. 顶部账户菜单 → **R2** → **Manage R2 API Tokens** → **Create API Token**
   - Permissions: **Object Read & Write**
   - Specify bucket: 只勾你刚建的那个（最小权限）
   - 创建后**立刻复制** `Access Key ID` 和 `Secret Access Key`——secret 关掉窗口就再也看不到
5. 在同一页面找到你的 **Account ID**（右侧 sidebar 或 Tokens 页顶部）——endpoint URL 用得到

### 0.2 · 写到 `.env`

```bash
# 在 lazu 仓库根目录创建 .env（如果还没有）
touch .env
```

打开 `.env`，填入：

```bash
R2_ENDPOINT=https://<account_id>.r2.cloudflarestorage.com    # 替换 <account_id>
R2_BUCKET=lazu-user-files                                 # 你刚建的 bucket 名
R2_ACCESS_KEY_ID=<r2-access-key-id>                          # 第 4 步复制的
R2_SECRET_ACCESS_KEY=<r2-secret-access-key>                  # 第 4 步复制的
```

### 0.3 · 重启 backend

```bash
./rebuild.sh --fast
# 或者只重启 Go 后端进程，让它重新读 .env
```

### 0.4 · 验证存储已连通

```bash
export LAZU_API_KEY="sk-your-token"
./ops/scripts/files-api/upload.sh README.md
# 应该输出 file-xxx 的 JSON。如果输出 503 storage_not_configured，回头检查 .env 是否填全 + backend 重启了
```

详细架构和备选方案见 [docs/DEPLOYMENT.md](../../../docs/DEPLOYMENT.md#object-storage-for-files-api)。

---

## 1 · 跑脚本的准备

```bash
# 1) 启动本地服务（如果还没起）
./rebuild.sh --fast
# 等到 backend ready（`./run.sh` 已正常启动）

# 2) 拿一个 API token（控制台 /console/tokens 页生成，sk- 开头）
export LAZU_API_KEY="sk-your-token-here"

# 3) 可选：换 backend 地址（默认 127.0.0.1:3000）
export BACKEND_URL="http://127.0.0.1:3000"
```

依赖工具：`curl`、`jq`、`python3`（仅本文件的全链路测试生成测试用 PNG 时需要）。Mac 上 `brew install jq` 即可。

---

## 2 · 文件 API 全链路测试

```bash
./ops/scripts/files-api/smoke.sh
```

跑完输出形如：

```
── 1 · POST /v1/files (purpose=user_data)
  PASS status code  (=201)
  PASS file id      (=file-abc123…)
  ...
── 8a · verify deletion
  PASS deleted file → 404  (=404)

✔ all 22 assertions passed
```

测试覆盖：

- ✅ Upload（`user_data` 文本 + `vision` PNG）
- ✅ List（无过滤 + `?purpose=vision`）
- ✅ Retrieve metadata
- ✅ Content 下载 + SHA256 校验
- ✅ 错误路径：401 无 token / 400 不支持的 purpose / 404 不存在
- ✅ Delete + 验证删除后变 404
- ⚪️ `/v1/responses` dereference（可选，需设 `DEREF_MODEL=<model-name>`）

测试结束自动清理它创建的所有文件。

---

## 3 · 单次操作脚本

所有脚本都用 `LAZU_API_KEY` + `BACKEND_URL` 两个环境变量，没传就报错。

### Upload 一个文件

```bash
# 文本/PDF 等 → purpose=user_data（默认）
./ops/scripts/files-api/upload.sh ~/Downloads/contract.pdf

# 图片 → purpose=vision
./ops/scripts/files-api/upload.sh ~/Desktop/screenshot.png vision
```

输出 JSON：

```json
{
  "id": "file-aBc123…",
  "object": "file",
  "bytes": 18234,
  "created_at": 1779525709,
  "filename": "contract.pdf",
  "purpose": "user_data",
  "status": "processed"
}
```

**purpose 接受的值**：

- `user_data` — 任意文档（max 512 MiB）
- `vision` — 图片（PNG/JPEG/GIF/WebP，max 20 MiB）
- 其他（`assistants` / `batch` / `fine-tune`）目前不支持，会返回 400

### 列出文件

```bash
./ops/scripts/files-api/list.sh              # 全部
./ops/scripts/files-api/list.sh vision       # 只看 vision
./ops/scripts/files-api/list.sh user_data    # 只看 user_data
```

### 删除文件

```bash
./ops/scripts/files-api/delete.sh file-aBc123…           # 删单个
./ops/scripts/files-api/delete.sh --all vision           # 删某 purpose 全部
./ops/scripts/files-api/delete.sh --all                  # 删全部（会问 y/n）
```

### 演示：用 file_id 调多模态 `/v1/responses`

```bash
export DEREF_MODEL="gpt-4o-mini"   # 任何支持 vision 的模型
./ops/scripts/files-api/dereference-demo.sh ~/Desktop/cat.png
```

脚本会自动：

1. 上传图片，拿到 `file-xxx`
2. 用 `{type: "input_image", file_id: "file-xxx"}` 引用方式调 `/v1/responses`
3. 后端中间件把 `file_id` 改写成 inline base64 发给上游模型
4. 打印响应
5. 清理上传的文件

---

## 4 · 直接用 curl（不通过脚本）

### 上传

```bash
curl -X POST "${BACKEND_URL}/v1/files" \
  -H "Authorization: Bearer ${LAZU_API_KEY}" \
  -F "file=@./path/to/file.pdf" \
  -F "purpose=user_data"
```

### 列出 + 过滤

```bash
curl "${BACKEND_URL}/v1/files?purpose=vision&limit=50" \
  -H "Authorization: Bearer ${LAZU_API_KEY}"
```

### 拿元信息

```bash
curl "${BACKEND_URL}/v1/files/file-xxx" \
  -H "Authorization: Bearer ${LAZU_API_KEY}"
```

### 下载内容

```bash
curl -o downloaded.pdf "${BACKEND_URL}/v1/files/file-xxx/content" \
  -H "Authorization: Bearer ${LAZU_API_KEY}"
```

### 删除

```bash
curl -X DELETE "${BACKEND_URL}/v1/files/file-xxx" \
  -H "Authorization: Bearer ${LAZU_API_KEY}"
```

### 在 `/v1/responses` 里引用

```bash
curl -X POST "${BACKEND_URL}/v1/responses" \
  -H "Authorization: Bearer ${LAZU_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o-mini",
    "input": [{
      "role": "user",
      "content": [
        { "type": "input_text",  "text": "总结这张发票" },
        { "type": "input_image", "file_id": "file-xxx" }
      ]
    }]
  }'
```

`input_image` / `input_file` 都支持。后端会在转发给上游之前把 `file_id` 换成 `data:<mime>;base64,<bytes>`（≤ 5 MiB 的文件有 10 min 内存缓存，避免重复读盘）。

---

## 5 · 计费说明

每次 upload 会按 **存储量 × 时长** 扣费：

- 默认 TTL：**30 天**（到期自动清理）
- 单价：约 **\$0.030/GiB/月**（实际见后端 `MicroUSDPerGiBMonth`）
- 删除会**按剩余 TTL 比例退款**

如果 token 没余额，upload 会返回 `402 Payment Required`。

---

## 6 · 常见错误

| 状态码 | error.code                 | 含义                                                                                     |
| ------ | -------------------------- | ---------------------------------------------------------------------------------------- |
| 401    | invalid_api_key            | token 错或过期                                                                           |
| 400    | missing_required_parameter | 没传 `file` 或 `purpose`                                                                 |
| 400    | invalid_file               | 不允许的 MIME 类型（vision 模式不能传 .pdf；user_data 模式不能传 .exe/.sh 等可执行文件） |
| 400    | purpose_not_supported      | purpose 值不是 `user_data` 或 `vision`                                                   |
| 402    | quota_exceeded             | token 余额不够                                                                           |
| 404    | file_not_found             | 文件不存在 **或** 不是你的（admin 也只能看自己的，console 单独有 admin 接口）            |
| 413    | file_too_large             | user_data > 512 MiB / vision > 20 MiB                                                    |
| 502    | storage_read_failed        | S3 兼容存储读取失败                                                                      |
| 503    | (storage not configured)   | 后端 ArtifactStorage 没配                                                                |

---

## 7 · 一些有用的 jq pattern

```bash
# 拿到自己所有文件占了多少 MB
./ops/scripts/files-api/list.sh | jq '[.[].bytes] | add / 1024 / 1024 | "\(.) MB"'

# 找超过 7 天没用的 file_id
./ops/scripts/files-api/list.sh | jq --arg cutoff "$(date -d '7 days ago' +%s)" \
  '.[] | select(.created_at < ($cutoff | tonumber)) | .id'

# 把上传后的 file_id 直接喂给环境变量
FID=$(./ops/scripts/files-api/upload.sh ~/img.png vision | jq -r .id)
echo "$FID"
```
