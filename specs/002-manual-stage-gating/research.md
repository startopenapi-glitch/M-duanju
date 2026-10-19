# Research：手动逐阶段制作现状（002）

日期：2026-10-19 · 基线：`ec8508c` · 只读源码与生产日志，不连接收费上游、不修改数据。

## 1. 用户目标

用户明确要求：把当前“全自动推进”改为**手动逐环节**：

- 导航可点击进入剧本、定妆照、镜头、成片；
- 上一环节确认后才执行下一环节；
- 先给需求、边界、业务规则和验收，再给技术方案与任务；
- 未经确认不进入大规模编码。

## 2. 现有实现事实

| 事实 | 位置 | 影响 |
| --- | --- | --- |
| 当前默认入口创建 `generation_runs` 并自动推进 | `frontend/src/features/creation/useProjectIntent.ts`、`backend/internal/service/run_orchestrator.go` | 用户无法在阶段之间停顿 |
| worker 对 `SCRIPT_REVIEW` 自动入队定妆照，对 `KEYVISUAL_REVIEW` 自动入队镜头 | `run_orchestrator.go` | 确认门禁不存在 |
| 已有手动生成接口 | `router.go`：`/script/generate`、`/keyvisual/queue`、`/keyvisual/approve`、`/shots/queue`、`/shots/:seq/approve`、`/shots/:seq/unapprove`、`/assemble` | 可复用，但未在当前 UI 暴露 |
| `ApproveKeyVisual` 当前只把状态写回 `KEYVISUAL_REVIEW` | `backend/internal/service/job_status.go` | 不能真正推进到镜头；需要修正语义或新增确认接口 |
| 没有脚本确认接口 | `router.go` | 剧本“确认”没有服务端事件 |
| 没有 `workflow_mode` 字段 | `projects` 表、迁移 025 | 无法区分新手动项目与旧自动项目 |
| 阶段导航是静态 `<ol>`，不可点击 | `frontend/src/pages/Project.tsx` | 不满足“点击每个环节进入” |
| 阶段详情只在折叠区展示 | `Project.tsx` `<details>` | 不能作为独立工作页 |
| 成片只有 `COMPLETED`，无最终确认态 | 项目状态 | 成片可直接展示/下载，是否需要“确认”待定 |
| `RunSummary` 文案/按钮全部面向自动 run | `features/creation/RunSummary.tsx` | 手动项目会被显示为“历史项目/继续自动生成” |
| 现有项目状态已覆盖大部分阶段 | `DRAFT`、`SCRIPT_REVIEW`、`KEYVISUAL_REVIEW`、`SHOTS_REVIEW`、`ASSEMBLING`、`COMPLETED` | 可复用，避免大改状态机 |

## 3. 规范冲突（必须先解决）

`.specify/memory/constitution.md` 1.0.0 明确：

> 不恢复手动生成模式，不增加预算确认或逐阶段确认。
> 积分足够直接生成，积分不足提示充值后按既有规则续跑；无预算确认、无人工阶段门禁。

这与用户本次“切成手动、逐阶段确认”的要求直接冲突。章程要求“用户最新明确要求优先”，但不能用旧章程批准新实现。

**结论：需要先确认章程 2.0.0 修订，再批准 002 规格；本轮只起草，不修改章程、不编码。**

## 4. 可复用能力

- 现有手动 jobs、预占、结算、幂等和 worker 执行；
- 现有 `ApproveShot`、`UnapproveShot`、`Assemble`、`GenerateScript`、`QueueKeyVisual`、`QueueShots`；
- 现有项目所有权中间件、`projects` 状态、审计日志；
- 现有 `ProjectPage` 的阶段映射可拆成纯展示。

## 5. 待用户确认

见 [clarifications.md](clarifications.md) C-01–C-08。
