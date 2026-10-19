# Completion：T01–T02

日期：2026-10-19 · 范围：仅章程/规格、workflow_mode、安全停止自动执行。未做前端阶段页、未部署、未执行生产迁移或停止。

## T01

- `.specify/memory/constitution.md` 升级为 2.0.0：全站手动、逐阶段确认；旧自动执行停止新阶段派发；
- 002 spec/plan 状态：Approved for T01–T02 implementation；T03+ 未批准；
- 安全停止口径：已提交/轮询/reconcile 继续核对结算；未提交 queued 取消并释放预占；不硬杀/不退款。

## T02

- 迁移 `026_project_workflow_mode.sql`：新增 `workflow_mode`（manual/auto），全站回填 manual；
- `model.Project` 增加 `workflowMode`；创建项目显式 manual；项目读取/列表返回该字段；
- `Service.StopAllAutomaticRuns`：
  - 枚举所有 active runs；
  - 复用 `CancelRun`（不直接写 run/job）；
  - 未提交 queued job 取消并释放预占；已提交/unknown 保留；
  - 项目统一 manual；写 `stop_automatic_mode` 审计；
- `drama-server stop-automatic` 一次性命令；生产尚未执行；
- 新增 `manual_mode_test.go`：
  - 停止两个 active run；
  - 未提交 queued job 取消/预占归零；
  - 已提交 polling job 与 upstream_id 保留；
  - workflow_mode 全部 manual；
  - 审计存在；
  - 新项目创建 manual 且无 run。

## 验证

- 目标测试通过；
- 后端全量 `go test ./...` 通过（含迁移 026）；
- 未运行生产 `migrate`/`stop-automatic`，未改生产数据；
- 构建/部署未执行。

## 未完成

- T03 阶段确认接口、T04 路由/导航、T05/T06 页面、T07 全站切换演练、T08 全量验收；
- 旧 `/keyvisual/approve` no-op 修正策略；
- 生产迁移/停止/部署需单独批准。
