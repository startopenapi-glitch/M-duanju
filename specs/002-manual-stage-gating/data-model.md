# Data Model：手动逐阶段

本文件只描述计划，不生成迁移。

## Project

新增：

| 字段 | 类型 | 规则 |
| --- | --- | --- |
| `workflow_mode` | `VARCHAR(16) NOT NULL DEFAULT 'manual'` | `manual` / `auto`；存量回填 `auto`；新项目显式 `manual` |

保持：

- `status`：`DRAFT`、`SCRIPT_GENERATING`、`SCRIPT_REVIEW`、`KEYVISUAL_GENERATING`、`KEYVISUAL_REVIEW`、`SHOTS_GENERATING`、`SHOTS_REVIEW`、`ASSEMBLING`、`COMPLETED`、`FAILED`、`BUDGET_EXCEEDED`；
- `keyvisual_url`、`final_url`、`spent_credits`、`keyvisual_task_id`；
- jobs、shots、settlements、credit transactions、audit logs：不改结构。

## Stage readiness

| 阶段 | ready 条件 | 下一动作 |
| --- | --- | --- |
| script | status DRAFT/SCRIPT_REVIEW；角色、场景、shots 存在 | 生成或确认 |
| keyvisual | status KEYVISUAL_REVIEW；keyvisual_url 非空；无活动 keyvisual job | 确认定妆照 |
| shots | status SHOTS_REVIEW；每个 shot 有 video_url | 逐条通过；全部通过后合成 |
| final | status COMPLETED；final_url 非空 | 查看/下载 |

## Confirmation facts

- 阶段确认写入 `audit_logs`，不新建表；
- 同一阶段重复确认以项目锁 + 状态条件幂等；
- 任务创建与状态迁移在同一事务；
- 确认本身不扣费；下一阶段 job 的既有预占/结算逻辑负责费用。

## Manual vs auto

| 模式 | 创建 run | 推进者 | 旧兼容 |
| --- | --- | --- | --- |
| manual | 否 | 用户确认接口 | 新项目默认 |
| auto | 是 | worker `run_orchestrator` | 存量/进行中项目 |
