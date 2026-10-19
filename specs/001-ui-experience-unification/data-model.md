> 2026-10-18 执行状态更新：本方案已获用户批准并完成三批实现；以下为原计划/研究记录。实际验收、偏差与未验证项以 [completion.md](completion.md)、[acceptance.md](acceptance.md) 为准。部署未获执行授权。

# 数据与展示模型（不迁移数据库）

本轮只重组读取与展示。projects、generation_runs、jobs、recharge_orders、credit_transactions、resume_wakeups、refund_intents 的结构、写入、价格快照和状态机保持不变。

## 模型归属

| 对象 | 唯一事实来源 | UI 允许派生 / 禁止派生 |
| --- | --- | --- |
| 项目与成片 | 项目读取、既有 outputs | 可选展示版本；不能把旧版 finalUrl 当成本次生成已成功 |
| 原 run | 精确 run_id 或原 requestKey 查询 | 可展示当前状态；不能替换成 latest；queued 只能说已排队，不能证明某笔充值触发 |
| job | 既有 phase/errorCode/manualRequired | 可提示未知提交；不能新增 RunStatus=submission_unknown 或调用生成接口来探测 |
| 积分 | 服务端余额、可用积分与缺口 | credits 与 availableCredits 各自标明；不能用余额差倒推出某笔订单入账 |
| 充值订单 | 新精确 order progress | status 与 credited 分开；不能相信列表中未 SELECT 字段的默认值 |
| 唤醒 | worker/outbox 及 run 当前状态 | 不把 wakeup done 当成制作成功；前端不写 wakeup、不主动恢复 run |
| 退款 | 既有 refund_intents | 与原入账分开显示；credited=true 不是仍可消费该笔全部积分 |

## ProjectPresentation

纯派生结构：`{stage: 'script'|'keyvisual'|'shots'|'assembly'|null, stageState: 'idle'|'active'|'complete'|'unknown', executionLabel: string, tone: BadgeTone, notice?: string, actions: ProjectAction[], output: 'current'|'previous'|'missing'}`。

`ProjectAction` 仅为 UI 动作标识：`refresh | topup | cancel | authorize-new | revise | download`，不作为后端授权。展示适配器不能带 api 参数或执行副作用。

| 项目状态 | stage | 说明 |
| --- | --- | --- |
| DRAFT | null | 尚未开始；有历史 run 时依据 run 说明，不能自动授权 |
| SCRIPT_GENERATING / SCRIPT_REVIEW | script | 后者为内部处理，不显示人工通过按钮 |
| KEYVISUAL_GENERATING / KEYVISUAL_REVIEW | keyvisual | 仅一个当前阶段 |
| SHOTS_GENERATING / SHOTS_REVIEW | shots | 将原双高亮合为镜头阶段 |
| ASSEMBLING | assembly | 合成中 |
| COMPLETED 且本版交付可核实 | assembly，complete | 所有步骤完成，无 active 标记 |
| FAILED / BUDGET_EXCEEDED / 未知 | null，unknown | 不硬猜阶段，展示原因及上次已核实状态 |

执行状态优先级：身份/读取失败 → unresolved job 警告 → cancelled/failed → settlement_pending → waiting_credits → queued/running → completed。优先级决定主说明，不覆盖原始事实；例如 cancelled 且仍有未知 job 必须同时说明停止派发与继续核对。只有 `waiting_credits && autoResume && isLatest && shortfallCredits>0` 才显示“充值并继续”；`settlement_pending` 显示结算核对，缺口未知不显示“还差0积分”。新执行授权仍需保留现有服务端所有拒绝条件。

旧素材可预览，但标“上一版本”，只有 outputs 中对应当前 run/projectVersion 的交付或经既有交付契约核实的当前 finalUrl 才标“本版成片”。历史无 run 成片保留下载，不自动消费。

## RechargePresentation

`{orderState: string, payment: 'checking'|'recorded'|'manual'|'unknown', ledger: 'pending'|'credited'|'unknown', continuation: 'none'|'waiting'|'queued'|'running'|'completed'|'stopped'|'superseded'|'unknown', refund: 'none'|string}`。

- `payment=recorded` 只表示服务端保存的微信付款凭据已存在；渠道实付但本地入账失败时可能仍为 checking/unknown，不显示假确认。
- `ledger=credited` 仅来自精确订单 credited=true；历史异常 paid/false 明确待核对，不自动补记、不放宽退款门禁。
- continuation 用原 run 的实时状态；无法访问仅显示“原任务暂不可用”，不泄漏项目标题。
- 无 run 的普通充值显示 none；人工审核路径显示 manual，不增加伪微信节点。
- 退款中/已退款仍展示真实结果；不得以历史 credited 或当前余额为本次恢复授权。

## 浏览器本地状态

创建草稿只在当前会话内；请求意图可临时记录 accountId、sessionRevision、projectId（已知时）、requestKey、规范化 scope 和提交状态。不得记录 token/密码/服务端密钥。sessionRevision 是非敏感随机会话标识，退出和跨账号变化清除；同账号换 token 也需失效。

列表筛选/页码按账号和页面保存到本次会话；URL含非敏感筛选。项目筛选必须服务端返回过滤后的total与分页，禁止仅筛选当前已载20条冒充全局结果。草稿、订单与run状态不能从本地缓存当成服务端事实。没有新增持久业务实体、后台定时器或消息总线。
