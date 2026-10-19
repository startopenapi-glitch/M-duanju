# Implementation Plan：全站手动逐阶段制作

Feature：002-manual-stage-gating  
状态：T01–T08 implemented；2026-10-19 已部署；旧 unknown 待决策  
Spec：[spec.md](spec.md) · 已确认选择：[clarifications.md](clarifications.md)  
基线：`ec8508c`（生产 release `20261019-script-default`）

## 1. 方案摘要

**全站立即切手动：所有项目 `workflow_mode=manual`；新项目不创建 `generation_runs`；已有自动 run 停止派发新阶段，已提交任务继续核对/结算，未提交 queued job 取消并释放预占。**

保留 `generation_runs`/orchestrator 代码作为**历史与对账兼容**，不再作为新项目推进路径。

## 2. 数据与切换

### 2.1 迁移（预计 026）

```sql
ALTER TABLE projects ADD COLUMN workflow_mode VARCHAR(16) NOT NULL DEFAULT 'manual';
UPDATE projects SET workflow_mode='manual';  -- 全站统一
```

- 不改变价格、积分、结算、退款、分佣表；
- 不新增 `stage_confirmations` 表；确认写 `audit_logs`；
- 不新增最终状态（用户已选择合成完成即 COMPLETED）。

### 2.2 全站停止自动推进（一次性操作）

必须用**现有 `CancelRun` 语义**，不能直接改 run 状态：

1. 找到所有 `generation_runs.status IN ('queued','running','waiting_credits','settlement_pending')`；
2. 按项目/用户调用 `CancelRun`：
   - run 置 `cancelled`；
   - 仅取消 `status='queued' AND phase='ready' AND upstream_id=''` 的未提交 job，释放预占；
   - 已提交/轮询/`reconcile` 任务保留，继续核对/结算；
   - 不退款、不删除素材；
3. 将项目 `workflow_mode='manual'`；
4. 已提交任务完成后，项目停在当前阶段，等待手动确认；
5. 对 `KEYVISUAL_GENERATING`/`SHOTS_GENERATING` 但没有活动 job 的旧项目，使用现有恢复/对账路径回到可操作阶段，不重新提交未知任务。

**风险：** “立即”不等于硬杀上游；硬杀会丢失已付费结果和产生重复扣费。必须在实施前确认此安全语义。

## 3. 手动阶段状态机

| 当前状态 | 用户动作 | 服务端结果 | 费用 |
| --- | --- | --- | --- |
| `DRAFT` | 生成剧本 | `SCRIPT_GENERATING` → `SCRIPT_REVIEW` | 0 |
| `SCRIPT_REVIEW` | 确认剧本 | 校验角色/场景/镜头存在，创建 keyvisual job，状态 `KEYVISUAL_GENERATING` | 定妆照 30（现有） |
| `KEYVISUAL_REVIEW` | 确认定妆照 | 校验图片存在，创建 shots jobs，状态 `SHOTS_GENERATING` | 每镜头现有价 |
| `SHOTS_REVIEW` | 逐镜头通过/取消/重做 | `shots.status` 更新；全部通过后合成可用 | 重做才付费 |
| 全部镜头通过 | 合成 | `ASSEMBLING` → `COMPLETED` | 现有合成规则 |
| `COMPLETED` | 查看/下载 | 无副作用 | 0 |

- 手动项目没有 run，worker 不会自动跨阶段；
- 确认在项目锁内完成，重复请求幂等；
- 旧 `/keyvisual/approve` 目前是 no-op，必须修正为确认并创建 shots，或新增 `/keyvisual/confirm` 后旧接口拒绝；二选一在实施前定。
- 每个确认写审计：`script_confirmed`、`keyvisual_confirmed`、`shots_confirmed`、`assemble_confirmed`。

## 4. API 草案

| 方法/路径 | 语义 | 校验 |
| --- | --- | --- |
| `POST /script/generate` | 生成/重生成剧本 | 已有 |
| `POST /script/confirm` | 确认剧本并派发定妆照 | `SCRIPT_REVIEW`、内容完整、无活动 keyvisual job |
| `POST /keyvisual/queue` | 生成定妆照 | 已确认剧本、无活动任务 |
| `POST /keyvisual/confirm` | 确认定妆照并派发镜头 | `KEYVISUAL_REVIEW`、URL 非空、无 shots job |
| `POST /shots/:seq/approve` / `unapprove` | 单镜头通过/取消 | 已有 |
| `POST /shots/:seq/regenerate` | 重做 | 二次确认、既有计费 |
| `POST /assemble` | 合成 | 所有镜头 APPROVED + 视频存在 |

错误：`stage_locked` 409、`stage_already_confirmed` 幂等/409、`balance_insufficient` 429、`submission_unknown` 409、越权 403/404。

## 5. 前端路由与页面

- `/project/:id` → 当前合法阶段
- `/project/:id/script`
- `/project/:id/keyvisual`
- `/project/:id/shots`
- `/project/:id/final`

顶部 `ProjectStageNav`：完成/当前/锁定；未来阶段不可导航；`aria-disabled`；刷新/后退保留。每个阶段独立页面：

- Script：生成、编辑、角色/分镜查看、确认（提示定妆照 30 积分）
- KeyVisual：生成、预览、重做、确认（提示下一步逐镜头计费）
- Shots：逐镜头视频、通过/取消/重做，全部通过后合成
- Final：成片预览/下载，不自动触发

旧自动执行：显示“历史/已停止自动执行”，已提交任务继续核对；不显示自动“继续生成”。手动项目移除 `RunSummary` 的自动入口。

## 6. 兼容与回滚

- 旧 `generation_runs` 保留；全部停止后仅历史/对账，不再用于新项目。
- 旧 API `StartRun` 保留但前端不调用；可后续标记 deprecated。
- `workflow_mode` 新列可回滚兼容；不改变金融表。
- 停止自动推进的一次性操作必须可审计、可重放；不删除 run/job/receipt。
- 回滚代码不能恢复已取消的 run；只能恢复旧展示，不重新扣费。

## 7. 测试策略

### 后端

- 新项目无 run、无下一阶段 job。
- 剧本/定妆照未确认时，下一阶段接口全部拒绝。
- 确认接口原子性、幂等、并发、重复点击。
- 逐镜头通过/取消/重做；全部通过前合成拒绝。
- 手动项目 worker 不推进。
- 全站停止：未提交 queued job 取消并释放预占；已提交/unknown 继续；无重复提交。
- 旧 run 记录保留、不自动下一阶段；余额/失败/权限/删除项目。
- 价格、积分、退款、分佣回归与基线一致。

### 前端

- 四阶段导航点击/锁定/URL/刷新/后退。
- 确认前按钮禁用、接口拒绝；重复点击不重复。
- 逐镜头/合成门槛；余额、失败、unknown。
- 旧自动执行只读展示。
- 375/768/1440px、键盘、44px、无横向溢出。

## 8. 实施边界

- 先批准 constitution 2.0.0、spec、本 plan、tasks；
- 未批准前不改代码、不迁移数据、不停生产 run；
- 分批：后端停止/门禁 → 前端路由/页面 → 兼容/回归；
- 发现冲突先登记，不擅自改商业规则；
- 部署和全站停止操作另行批准。
