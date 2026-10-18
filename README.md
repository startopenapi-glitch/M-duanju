# Drama Platform

一个可自托管的 AI 短剧生成平台：从一句话想法生成剧本、定妆照、分镜视频，最后合成成片；包含项目工作台、生成队列、积分/充值、运营后台和代理邀请。

> 当前为开源快照，默认仅提供**本地开发与演示**配置。生成能力依赖兼容的文本/图片/视频 Provider，支付默认使用 mock；生产部署前请完成安全、合规和容量评估。

## 功能

- 项目创建：16:9 / 9:16、5–180 秒、剧本生成
- 生成流程：剧本 → 定妆照 → 分镜视频 → 合成
- 生成队列：租约、预占、结算、失败恢复、充值续跑
- 积分与充值：微信 Native 支付接口、人工审核降级、订单与流水
- 项目历史：软删除，保留财务与审计记录
- 运营后台：用户、项目、订单、任务、退款、代理邀请
- 测试：Go 全量测试、前端 Vitest 测试、类型检查和构建

## 技术栈

- 后端：Go 1.23、Gin、PostgreSQL 15+
- 前端：React 18、TypeScript、Vite、Tailwind CSS
- 媒体：FFmpeg / FFprobe
- 部署：Docker Compose（本地）

## 快速开始（Docker）

需要 Docker Compose v2。

```bash
cp .env.example .env
# 修改 .env 中至少 JWT_SECRET 和数据库密码
docker compose up --build
```

打开 <http://localhost:8088>，注册账号即可。首次启动会自动执行数据库迁移。

默认配置：

- 支付：`WXPAY_MOCK=1`，不会调用真实微信支付；
- 生成：需要配置兼容的 `LIMENG_BASE_URL` / `LIMENG_API_KEY`；
- 文件：成片、定妆照、分镜保存在 `drama-storage` volume，由 web 容器提供 `/files/`。

没有 Provider 时，注册、项目、剧本编辑、后台和支付 mock 仍可体验；真正的图片/视频生成会失败并显示 Provider 错误。

## 本地开发

```bash
# 1. PostgreSQL
createdb drama

# 2. 后端
cd backend
export DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5432/drama?sslmode=disable'
export DATABASE_URL_OWNER="$DATABASE_URL"
export JWT_SECRET="$(openssl rand -hex 32)"
export STORAGE_DIR="$PWD/.storage"
export PUBLIC_BASE_URL='http://localhost:5173'
go run ./cmd/server migrate
go run ./cmd/server api       # 一个终端
go run ./cmd/server worker    # 另一个终端

# 3. 前端
cd ../frontend
npm ci
npm run dev
```

前端开发服务器会把 `/api` 代理到 `http://127.0.0.1:8080`；具体见 `vite.config.ts`。

## 配置

完整示例见 [.env.example](.env.example)。关键变量：

| 变量 | 说明 |
|---|---|
| `DATABASE_URL` | 运行时数据库连接串 |
| `DATABASE_URL_OWNER` | 执行迁移的 owner 连接串；生产建议与运行时角色分离 |
| `JWT_SECRET` | 会话签名密钥，至少 32 字节随机值 |
| `PUBLIC_BASE_URL` | 浏览器访问地址，用于邮件链接和媒体 URL |
| `STORAGE_DIR` | 生成文件存储目录 |
| `WXPAY_MOCK=1` | 仅开发：模拟微信支付，不产生真实扣款 |
| `LIMENG_BASE_URL` | 兼容的文本/图片/视频 Provider 地址 |
| `LIMENG_API_KEY` | Provider 密钥；不要提交到仓库 |
| `VIDEO_RESOLUTION` | `standard` / `720P` / `1080P` |

生产环境不要使用 `WXPAY_MOCK`，并应配置独立的数据库 runtime 角色、HTTPS、反向代理、备份和监控。

## Provider 说明

本仓库只包含 Provider 适配代码，不包含第三方模型权重、账号或 API 密钥。Provider 需要兼容当前的文本 Chat Completions、图片任务和视频任务协议：

- `POST /chat/completions`：剧本/修改计划
- `POST /images`、`GET /images/:id`：定妆照
- `POST /videos`、`GET /videos/:id`：分镜视频

如果使用其他 Provider，建议先在 `backend/internal/service` 增加适配层，而不是把第三方密钥写入代码。Provider 的服务条款、内容合规和计费由部署者自行负责。

## 架构

```text
React SPA (nginx)
      │  /api, /files
      ├──────────────┐
      ▼              ▼
  Go API  ◄──────  Go worker
      │              │
      └──── PostgreSQL
                     │
                Provider APIs
```

- API 负责鉴权、项目、订单、运行编排和同步查询；
- worker 负责队列领取、Provider 提交/轮询、媒体落盘和结算；
- PostgreSQL 保存项目、任务、积分、结算和审计事实；
- 生成文件使用内容寻址方式发布，避免并发覆盖。

## 测试

```bash
# 后端；需要一个隔离 PostgreSQL 测试库
cd backend
TEST_DATABASE_URL='postgres://test:test@127.0.0.1:5432/drama_test?sslmode=disable' \
  go test ./...

# 前端
cd frontend
npm test
npm run typecheck
npm run build
```

测试会使用 mock Provider/本地媒体，不需要真实付费。

## 安全

- 不要提交 `.env`、支付证书、JWT/数据库密码、Resend/Provider Key；
- 生产环境使用 HTTPS、私有数据库网络、最小权限数据库角色；
- 充值、退款和生成队列涉及资金/任务一致性，修改前请先读 `SECURITY.md` 和相关测试；
- 发现安全问题请按 [SECURITY.md](SECURITY.md) 私下报告，不要公开 Issue 披露利用细节。

## 贡献

见 [CONTRIBUTING.md](CONTRIBUTING.md)。提交代码即表示同意以 Apache-2.0 发布，并使用 DCO sign-off。

## 许可证与品牌

- 代码：[Apache License 2.0](LICENSE)
- 第三方依赖：[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)
- “Drama Platform”/示例 UI 中的产品名称仅用于演示；生产品牌、Logo、域名不随代码授权，见 [TRADEMARKS.md](TRADEMARKS.md)。

本项目不包含任何真实客户数据、生产密钥或生产部署配置。
