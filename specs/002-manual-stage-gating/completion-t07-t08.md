# Completion：T07–T08

日期：2026-10-19 · 状态：Implemented / **已部署**  
范围：全站停止自动执行演练、手动流程全量验收。生产迁移、切换、部署均未执行。

## T07 全站停止

隔离库测试覆盖：

- 两个 active run 被取消；
- 未提交 queued shot/keyvisual job 取消，`reserved_credits` 归零；
- 已提交 polling job 保留 `upstream_id`，继续核对/结算；
- `reconcile`/unknown job 保留，不重发；
- 所有项目 `workflow_mode='manual'`；
- `stop_automatic_mode` 审计存在；
- 第二次停止返回 0，不产生重复审计，不改变 reconcile job。

生产 `stop-automatic` 未执行。

## T08 全量验收

### 通过

- 后端全量 `go test ./...` 通过；
- 前端 34 个测试文件、146 个测试通过；
- TypeScript typecheck、Vite build 通过；
- 手动项目无 run；HTTP 自动 run 被拒绝；
- 剧本/定妆照确认门禁、重复确认、错误阶段拒绝；
- 四阶段路由/导航锁定/未来 URL 重定向（纯 helper 测试）；
- 剧本、角色、场景、镜头字段可编辑；
- 逐镜头通过/取消/重做，全部通过后合成；
- 旧自动执行只读展示，无自动启动入口；
- 价格/积分/退款/分佣结构未改。

### 未执行/未验证

- 浏览器端真实点击、移动端 375/768 视口、键盘和 44px 触摸目标的人工验收；
- 生产迁移 026、生产 `stop-automatic`、生产部署；
- 真实用户项目的手动流程实跑；
- 旧自动 in-flight 项目在生产上的具体状态核对。

## 重要边界

- 当前代码只完成并提交在分支；生产仍为 `20261019-script-default` 旧自动流程。
- 上线顺序必须：
  1. 备份数据库；
  2. 部署含 026 的代码并执行迁移；
  3. 执行 `drama-server stop-automatic`；
  4. 验证已提交任务继续结算、未提交任务取消、无新自动 run；
  5. 发布前端阶段页；
  6. 监控后再开放用户。
- 迁移/停止/部署需单独批准；不能把本轮代码合并当作上线许可。
