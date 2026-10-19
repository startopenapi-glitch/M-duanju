# 短剧生成平台 / Drama Platform

一套可自托管的 AI 短剧制作平台：从项目、剧本、定妆照、分镜视频到成片合成，按阶段人工确认；内置积分预占/结算、微信支付、分成、退款和管理后台。

> 这是面向学习与二次开发的参考实现，不是开箱即用的商业支付系统。上线前请完成安全、支付、合规和容量评审。

## 功能

- **项目工作流**：项目 → 剧本 → 定妆照 → 分镜视频 → 成片，逐阶段人工确认。
- **生成上游**：通过 OpenAI 兼容 HTTP 接口接入文本、图片、视频生成服务；上游地址、模型和密钥均可配置。
- **积分账本**：预占、结算、失败释放、历史记录。
- **支付**：微信支付 Native/H5 的下单、回调和退款结构；本地默认 mock。
- **分成与后台**：合作伙伴、邀请、订单、任务、退款和运营操作。
- **单二进制多角色**：`api`、`worker`、`migrate`、`grant-runtime`、`stop-automatic`。
- **可替换媒体链路**：ffmpeg 作为外部运行时依赖，不随仓库分发。

## 架构

```mermaid
flowchart LR
  U[Browser] --> FE[React + Vite]
  FE -->|/api| API[Gin API]
  API --> PG[(PostgreSQL)]
  API -->|enqueue / status| DB[(jobs & runs)]
  WORKER[Worker] --> DB
  WORKER --> UP[OpenAI-compatible upstream]
  WORKER --> FS[(STORAGE_DIR)]
  API --> FS
  WORKER --> PAY[WeChat Pay]
  API --> PAY
```

- **API**：无状态 HTTP 服务，负责鉴权、项目/订单/积分接口和静态文件。
- **Worker**：认领任务，调用上游生成图片/视频，执行合成与结算；可独立重启和扩容。
- **Migrate**：使用 owner 数据库角色执行迁移；应用运行角色权限更小。
- **PostgreSQL**：业务数据、任务租约、账本和幂等记录的唯一事实来源。

### 四阶段流水线

1. **剧本**：LLM 生成/修改，人工确认。
2. **定妆照**：文生图，人工确认角色形象。
3. **分镜视频**：图生视频，逐镜生成、审核、重试。
4. **成片**：按分镜顺序做转场/拼接、音频归一化和封装，人工验收后交付。

## 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go 1.23、Gin、pgx、PostgreSQL |
| 前端 | React 18、TypeScript、Vite、Tailwind CSS |
| 媒体 | ffmpeg/ffprobe（外部依赖，不随仓库分发） |
| 支付 | 微信支付 API v3（可 mock） |
| 部署 | 单二进制 + Docker Compose |

## 目录

```text
backend/                 Go 服务（api / worker / migrate）
  cmd/server/            入口
  internal/              业务、模型、路由、任务
  migrations/            PostgreSQL 迁移
frontend/                React + Vite 前端
ops/                     示例 nginx 模板
specs/                   产品/验收规范（已脱敏）
verification/ui/         UI 验证工具（可选）
docker-compose.yml       PostgreSQL + migrate + api + worker + web
Dockerfile               后端镜像
.env.example             环境变量模板
```

## 本地快速启动

### 1. 准备

- Go **1.23+**
- Node.js **20+**（推荐 22）
- PostgreSQL **16+**
- ffmpeg/ffprobe（只有合成/媒体探测需要）
- 可选：Docker + Docker Compose

### 2. 配置

```bash
cp .env.example .env
# 编辑 .env，至少设置 DATABASE_URL、JWT_SECRET 和上游配置
```

生成 JWT 密钥：

```bash
openssl rand -hex 32
```

把输出填入 `.env` 的 `JWT_SECRET`。**不要把 `.env` 提交到 Git。**

### 3. 数据库与后端

```bash
# 让当前 shell 读取 .env（Go 程序不会自动加载 .env）
set -a
. ./.env
set +a

cd backend
go run ./cmd/server migrate
go run ./cmd/server api
```

另开终端启动 worker：

```bash
cd backend
set -a
. ../.env
set +a
go run ./cmd/server worker
```

默认 API 监听 `127.0.0.1:8080`。如果从其他机器访问，把它改成：

```dotenv
BIND_ADDR=0.0.0.0
```

### 4. 前端

```bash
cd frontend
npm ci
npm run dev
```

打开 `http://localhost:5173`。Vite 会把 `/api` 代理到 `http://127.0.0.1:8080`。

### 5. 第一个管理员

先注册一个账号，然后在数据库中提升为管理员（把邮箱换成你的）：

```bash
psql "$DATABASE_URL" -c "UPDATE users SET role='admin' WHERE email='you@example.com';"
```

## Docker Compose

```bash
cp .env.example .env
# 编辑 .env：设置 JWT_SECRET、上游密钥；本地可保留 WXPAY_MOCK=1
docker compose up --build
```

- Web：`http://localhost:3000`
- API：`http://localhost:8080`
- 数据库迁移会在 `migrate` 容器中自动执行一次

### ffmpeg 与许可证

仓库**不包含** ffmpeg/ffprobe 二进制。默认 Docker 镜像也不安装它们。

如果需要在容器内合成：

```bash
# 只建议在了解并接受 ffmpeg 许可证义务时使用
INSTALL_FFMPEG=true docker compose build worker
```

或者在宿主机安装 ffmpeg，并以你自己的方式把可执行文件提供给 worker。不要把 GPL 二进制直接提交到本仓库。

## 环境变量

完整清单见 [`.env.example`](./.env.example)。关键项：

| 变量 | 说明 |
|---|---|
| `DATABASE_URL` | 应用运行角色连接串 |
| `DATABASE_URL_OWNER` | 迁移/授权使用的 owner 连接串（可选） |
| `JWT_SECRET` | 登录令牌签名密钥，必须随机且足够长 |
| `PUBLIC_BASE_URL` | 对外访问根地址，用于回调/媒体 URL |
| `STORAGE_DIR` | 生成文件和成片的本地存储目录 |
| `LIMENG_BASE_URL` | OpenAI 兼容上游的 `/v1` 根地址 |
| `LIMENG_API_KEY` | 上游密钥；只放环境变量/密钥文件 |
| `LIMENG_TEXT_MODEL` | 文本模型名 |
| `VIDEO_RESOLUTION` | 生成分辨率档位 |
| `FFMPEG_PATH` / `FFPROBE_PATH` | 媒体工具路径 |
| `WXPAY_*` | 微信支付配置；本地可全部留空并使用 mock |
| `RESEND_API_KEY` / `SMTP_*` | 邮件发送，二选一 |

支持 `_FILE` 后缀的密钥（如 `JWT_SECRET_FILE`、`WXPAY_APIV3_KEY_FILE`）可避免把密钥直接写进环境变量。

## 测试

```bash
cd backend
go test ./...

cd ../frontend
npm ci
npm run typecheck
npm test
npm run build
```

部分后端测试需要真实 PostgreSQL：

```bash
export TEST_DATABASE_URL='postgres://drama:change-me@127.0.0.1:5432/drama_test?sslmode=disable'
go test ./...
```

需要 ffmpeg 的测试默认会跳过；要跑完整媒体验收，设置 `DRAMA_TEST_FFMPEG=/path/to/ffmpeg`（同目录需有 `ffprobe`）。

## 安全与生产注意事项

- 不要提交 `.env`、数据库导出、支付证书、用户数据或生成媒体。
- 生产环境使用密钥管理服务，并轮换 JWT/支付/上游密钥。
- 微信支付必须使用真实商户配置、HTTPS 回调、证书校验和回调幂等；本地 mock 不适用于生产。
- 对 ffmpeg 输入做资源与超时限制，避免不可信媒体导致 CPU/内存耗尽。
- 默认配置面向本地开发；公网部署前请做 CSRF/CORS、限流、审计、备份和恢复演练。
- 发现安全问题请按 [SECURITY.md](./SECURITY.md) 私下报告，不要公开 Issue。

## 许可证

源代码与本文档使用 **Apache License 2.0**，见 [LICENSE](./LICENSE) 和 [NOTICE](./NOTICE)。

第三方依赖保留各自许可证；ffmpeg/ffprobe 不随仓库分发，相关说明见 [LICENSE-NOTES.md](./LICENSE-NOTES.md)。

## 贡献

见 [CONTRIBUTING.md](./CONTRIBUTING.md)。
