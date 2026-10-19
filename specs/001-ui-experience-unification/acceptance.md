# 验收与追踪矩阵

状态：**三批实现与隔离验证已完成；AC-019 真机项未验证，未部署**。以下保留原验收定义，执行结果见文末与 coverage.json；PASS 仅指所列隔离条件，不含真实渠道或线上业务。

每条验收记录必须包含：代码提交、测试名称/命令、环境与合成夹具、结果、日志/截图路径、限制。状态只用 PASS / FAIL / 未验证；跳过、缺环境不能折算 PASS。

| ID | 关联需求 | 场景与必须满足的结果 | 验证方式 |
| --- | --- | --- | --- |
| AC-001 | FR-001、FR-015 | 访客、普通、推广、管理员及权限撤销；入口无重复实现，角色与当前空间正确 | 路由/组件测试 + 隔离 Chromium |
| AC-002 | FR-002、FR-016 | 全路由 375/768/1440px + 200% 缩放；长标题、邮箱、金额不撑宽文档；一个页面主标题，状态/主动作可读 | DOM 尺寸断言 + 截图逐页检查；U01 先红后绿 |
| AC-003 | FR-003 | 空项目与多项目列表；创建入口复用，有项目时项目优先；返回保留筛选 | 组件/浏览器 |
| AC-004 | FR-004、FR-005 | 想法空值、估算失败、默认范围、展开设置；只有一个开始动作，无新增预算弹窗 | 组件 + 模拟契约 |
| AC-005 | FR-006 | 同会话关闭重开恢复草稿；主动退出、A→B 换号、旧标签晚响应不得泄漏草稿 | 会话与多标签模拟 |
| AC-006 | FR-007 | 足够积分、快速双击、超时/响应丢失、刷新；仅原执行，不能重复收费生成 | 前端拦截请求计数 + 隔离后端幂等回归 |
| AC-007 | FR-008、FR-009 | 全部阶段/运行态组合，尤其分镜与审片；运行时只一个当前阶段，完成时无正在运行误报 | 状态映射单测 + UI；U02 先红后绿 |
| AC-008 | FR-008、FR-010 | 成片可用/不可用、下载过期/失败、版本切换；正确获取结果，不把执行结束冒充交付完成 | 合成媒体与下载响应，不下载客户素材 |
| AC-009 | FR-010 | 缺积分、结算待核对、未知提交、失败、停止、完成、离线；只出现允许动作，无伪百分比 | 状态契约矩阵 + 浏览器 |
| AC-010 | FR-011 | 合法 run_id、无 run_id、恶意/越权/过期上下文、深链；原账号/项目/执行一致 | 前后端授权回归 + 浏览器 |
| AC-011 | FR-012 | 渠道成功但入账未成；入账已成但恢复未成；分别延迟/失败时不误报下一步 | 隔离支付/账本/恢复模拟 + UI |
| AC-012 | FR-012、FR-013 | 余额足够恢复时原 run_id 不变，余额、订单、原执行、项目列表一致 | 隔离端到端 + UI 请求断言 |
| AC-013 | FR-013 | 充值不足、多笔待恢复、重复与乱序通知、多标签；不新增 run、不重复入账/扣费，归属按已有规则 | 隔离数据库事务/并发/恢复队列回归 |
| AC-014 | FR-013 | 已取消/完成、账户停用、权限撤销、未知提交；到账不复活终态、不越权、不盲目重发，预占按原规则处理 | 隔离取消/结算/恢复回归 |
| AC-015 | FR-012、FR-013、FR-017 | 付款后离开、切换项目/账号、关页重开；不错误跳转或展示旧项目；服务端合法恢复不依赖 UI | 多标签浏览器 + 服务端恢复测试 |
| AC-016 | FR-014 | 设置拆分、旧直链/合法参数、刷新/后退/详情返回、外部 return URL；保持上下文，不开放重定向 | 路由测试 + 浏览器 |
| AC-017 | FR-015 | 普通账号直达后台、推广数据越权、旧会话、账号删除/停用；后端拒绝，界面无旧数据 | 隔离鉴权测试 + 前端延迟响应 |
| AC-018 | FR-002、FR-016 | 字段标签/错误、按钮/弹层/列表/详情；Tab/Escape/焦点返回，主要触控区≥44px，减少动效可用 | 浏览器自动化 + 人工键盘走查 |
| AC-019 | FR-016 | 手机安全区与软键盘下输入、提交、关闭可达；没有被固定操作条遮挡 | 375px 模拟 + 真实移动设备；仅模拟不能宣称真机通过 |
| AC-020 | FR-017 | 隐藏/恢复、断线/重连、慢响应；无查询堆积，单资源无重复轮询，恢复即核对 | 假时钟/请求计数 + 浏览器网络记录 |
| AC-021 | FR-017 | 同构建/设备/数据至少5次，保存中位 LCP、固定操作延迟、初始 JS gzip 字节数与媒体请求，按 SC-007 容差判断 | 保存前后原始性能记录；场景和基线在技术方案固定，超过容差需修复或重新提交范围变更确认 |
| AC-022 | FR-007、FR-013 | 价格/积分/退款/归属/分佣基线一致；重复通知、关单竞态、退款冲销及收益回归无改变 | 审查相关 diff + 既有隔离金融用例；禁止真实交易 |
| AC-023 | FR-018 | 前端全量测试、类型检查、生产构建；Go 全量与适用 race、生产数据库版本兼容回归通过 | 实际命令/退出码/测试数量/跳过数；无库不可报全绿 |
| AC-024 | FR-018 | spec→plan→tasks→代码→测试可追踪；独立复审新增缺陷闭环；发布和回滚可审阅 | 一致性审查、独立复审记录、剩余风险和部署提案 |

## 现有测试入口（已读代码，非本轮测试结果）

- `frontend/package.json`：`npm test`（含 Node 测试与 vitest）、`npm run typecheck`、`npm run build`。
- `backend/internal/testutil/database_test.go` 的防护要求特定隔离库 `drama_remediation_test_20261017`；计划阶段还需核对所有测试帮助函数，不能只设置一个变量就假定所有测试安全。
- 后端测试命令在确认隔离连接与模拟上游后执行 `go test ./... -count=1`，并为修改的并发路径补适用 race 检查。
- 现有 run/payment/recovery 测试只是起点；新增验收不能只重复跑历史绿灯而不检查新行为。

## 完工判定

U01、U02 必须保留旧基线失败与修改后成功证据；需求新增用例必须断言正确结果。金融、安全、自动续跑项有 FAIL 或未验证时不进入发布。真机软键盘等暂缺环境需显式列出，不能用桌面截图冒充；是否接受剩余非核心限制由用户决定。不能修改标准来迁就实现。

## 本轮执行结果（2026-10-18）

代码基点 `4783d35`，之后仅极窄屏CSS和验证/文档收敛。

| ID | 状态 | 证据 | 边界 |
|---|---|---|---|
| AC-001 | PASS | navigation.test.tsx、AdminWorkspace.test.tsx；48页面与账户导航场景 | 菜单角色与身份失败不混淆 |
| AC-002 | PASS | after/browser.json；interactions/results.json（72项含48次缩放重排） | 375/768/1440px各16页；每页1个h1；模拟200%重排不是真机缩放 |
| AC-003 | PASS | projects.test.tsx；TestProjectListFilterCountsAndScopesAllPages | 同筛选共享读取、已载分页刷新、服务端筛选与归属 |
| AC-004 | PASS | composer.test.tsx；creation intent；浏览器create-once和draft-focus-and-settings | 一次开始动作，无预算确认；草稿/设置同源 |
| AC-005 | PASS | api.test.ts、intent.test.tsx；浏览器draft-focus-and-settings | 会话revision、迟到响应、草稿重开；跨标签以StorageEvent模拟 |
| AC-006 | PASS | intent.test.tsx、project-intent.test.tsx、TestUIRunNotCreatedCodeOnlyBeforeCreation | 明确拒绝/未知重放/刷新中断区分；服务端幂等全量回归 |
| AC-007 | PASS | presentation.test.ts；48页面project阶段断言；Project/FinalReview既有回归 | 旧基线2个阶段高亮，新版1个；完成态无假运行 |
| AC-008 | PASS | output-preview.test.tsx；Project/FinalReview回归 | 合成404/媒体错误和保留版本；未下载客户素材，外部签名URL使用原生链接 |
| AC-009 | PASS | project-flow.test.tsx、review-run-state.test.tsx、FinalReview.test.tsx | 结算待核对、unknown、失败和旧成片展示不触发新生成 |
| AC-010 | PASS | ui_read_test.go、pay_run_test.go；order-binding-mismatch浏览器 | 精确run/key、越权/非法/旧版、订单归属，不能退回latest |
| AC-011 | PASS | billing presentation/recharge-flow；paid-without-ledger浏览器 | 付款、账本入账、原run继续分别读取；manual保留原单 |
| AC-012 | PASS | run_resume_test.go、review_money_test.go；original-recharge-continuation浏览器 | 到账读取后才导航；不POST新run或resume |
| AC-013 | PASS | 完整Go/race；pay_run、review_money、round2_money_extra、run_resume | 模拟不足额、重复通知、并发与恢复队列；非真实支付 |
| AC-014 | PASS | codex_cancel、run_resume、settlement、remediation_regression；ui_read鉴权 | 终态、停用、未知提交按原规则保留；无真实生成 |
| AC-015 | PASS | recharge-flow.test.tsx、api.test.ts、浏览器manual-order-retained | 页面卸载/身份切换保护与刷新原单；服务端恢复独立测试 |
| AC-016 | PASS | Settings/navigation测试与App路由代码核对；浏览器导航和刷新场景 | 兼容/security、settings旧参数；未引入returnTo外跳 |
| AC-017 | PASS | AdminWorkspace.test.tsx；后端router鉴权及账户全量测试 | 越权后端拒绝；身份晚响应被抛弃 |
| AC-018 | PASS | primitives.test.tsx；浏览器菜单Enter/弹层Escape/焦点返回；截图走查 | 共享主控件44px和token色彩；非屏幕阅读器专项认证 |
| AC-019 | 未验证 | 375px重排和弹层已验；没有真实手机设备会话 | 软键盘、安全区、真实Safari未验证，不能以Chromium冒充 |
| AC-020 | PASS | polling.test.tsx、recharge-flow.test.tsx；请求计数 | 隐藏暂停、可见/online核对、单飞、错误退避；动作强制刷新旧请求取消 |
| AC-021 | PASS | before-final/performance.json、after/performance.json、performance-comparison.json | 6路由各5次；同浏览器合成条件；新增原单查询无旧版对应项，仅报绝对值 |
| AC-022 | PASS | 商业写路径diff核对；全量Go/race/PG10 | 无定价、套餐、扣费、退款、归属/分佣规则改动；不做真实交易 |
| AC-023 | PASS | verification/reports/ui-unification-20261018/summary.json | Go/race/PG10各478；Vitest100+Node4；typecheck/build/cache通过 |
| AC-024 | PASS | 本目录completion.md、release-plan.md、独立复审记录与coverage.json | 实现与验证可追溯；真机限制保留；部署未执行 |
