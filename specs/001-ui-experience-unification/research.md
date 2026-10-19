# 技术核对与决策记录

日期：2026-10-18。源代码基线：`6b902ed`；规范基线：`9081cb7`。本轮只读源码、编写文档，没有运行生产请求或业务测试。

| 编号 | 代码证据（仓库相对路径） | 发现与本方案决策 |
| --- | --- | --- |
| R01 | frontend/package.json；backend/go.mod | React 18、Router 6、Tailwind 3、Vite 7、Go 1.23、Gin。保留锁文件与框架，不套用 Skill Store 的 Vite 8/shadcn 迁移 |
| R02 | layout/AppShell.tsx；layout/ProjectRail.tsx；features/workspace/WorkspaceNav.tsx | 两套侧栏各自维护账户操作，后台重复顶栏。保留 AppShell，用一个 AccountMenu 和同一导航配置；账户区切空间 |
| R03 | features/workspace/ui.tsx；components/Modal.tsx；layout/useFocusScope.ts | 已有页头、状态、分页、详情和焦点工具。通用外观抽出，业务状态字典留各领域；Modal 补现有焦点工具，不再造另一套弹层 |
| R04 | lib/api.ts；features/workspace/resource.tsx；App.tsx | 有 token/epoch 晚响应拒绝、账户+URL 资源隔离与身份切换重挂载。复用；只补可供共享数据订阅的非敏感会话修订号，不能移除恢复码显示的会话过期延迟保护 |
| R05 | lib/projects.ts；pages/Home.tsx；layout/ProjectRail.tsx；service/user.go:ListProjects | 两处useProjects独立请求；刷新总是page=1；现有列表只有分页、没有筛选。共享provider，并给既有GET补可选只读筛选，避免US1筛选返回场景遗漏；同一事件重查已载页，不重置浏览位置 |
| R06 | pages/Settings.tsx；App.tsx；rg 搜索所有 settings/security/runId 调用 | 设置 tab 仅是组件状态，并不存在已实现的 ?tab= 参数协议。正式落地 /settings?tab=account；/security 兼容跳转。对拟兼容的 ?tab=billing/admin 明确称兼容建议，不伪称旧接口 |
| R07 | pages/Project.tsx 的 FLOW/ORDER、runState 与动作门禁 | 双高亮来自共用序号；展示阶段可合并为剧本/定妆/镜头/合成四阶段，保留后端枚举。run 查询失败不能进入历史项目分支；unknown 来自 job，不是 RunStatus 枚举 |
| R08 | pages/Credits.tsx；router/router.go；service/run_api.go | 充值入口仅有 runId；只有按项目查最新 run 的 GET，没有按原 run 查上下文。新增只读 /runs/context，不能拿最新执行代替原执行 |
| R09 | handler/pay.go:GetPayStatus；service/pay.go:ConfirmOrderByQuery、settleOrder | GET /pay/status/:id 会查渠道并可能入账/关单，非纯读取。保留其现有对账用途；新加 /orders/:id/progress 只读持久事实。已付款但未入账的历史数据不自动修复 |
| R10 | service/pay.go:settleOrder；service/user.go:ListOrders；model/model.go:RechargeOrder | 正常 paid、credited、余额、流水、wakeup 同事务。订单列表未 SELECT credited/run_id，结构体默认 false 不能当事实。新增精确只读投影；不人为制造“付款成功→延迟入账”的业务中间态 |
| R11 | components/PayQRCode.tsx；pages/Credits.tsx | 注释称隐藏暂停，实际 check 未检查 document.hidden；无在途互斥，onPaid 引用变化会重订阅。统一串行调度；确认支付不等于确认入账，按订单投影核对 |
| R12 | run/wakeup.go:ResumeRunChecked；service/pay.go:resolveRechargeRun | 绑定充值只允许 waiting_credits，实际续跑还检查账号、项目、版本、auto_resume、缺口。settlement_pending 不能直接带原 runId 下充值单；只展示核对状态，waiting_credits/settlement 按原口径展示缺口 |
| R13 | layout/NewProjectDialog.tsx；service/user.go:CreateProject；service/run_api.go:StartRun | 创建项目无幂等键，启动 run 有严格幂等键与输入指纹。创建响应未知时不自动重发、不按标题猜认；已知项目但启动未知可查同账号同 requestKey，再由明确重试重放同键，不换键 |
| R14 | internal/testutil/database.go；service/migrate_test.go；verification/release_gate.py | 隔离库仅允许 drama_remediation_test_20261017；无变量会 Skip；现有门禁为 Linux 路径、编码器和 nginx 前置。计划适配验证工具路径，不复制生产 .env，也不放宽数据库白名单 |

## 范围取舍

- 采用“前端职责整理 + 两个普通用户只读投影 + 现有项目GET的只读筛选”。仅CSS修改无法满足精确run_id、入账真实性与页面职责；重写服务端或新建钱包/缓存系统没有必要。
- 两个投影不会调用支付、生成、恢复、退款命令。原对账接口保留其既有副作用，不能因新 GET 看似相似而混用。
- 新版无需数据库迁移、价格迁移或生产数据补录。不会给 CreateProject 增加幂等表；未知创建的完整无感恢复需要另一个后端改造，当前用安全异常态处理。
- 没有新增持久渠道付款证据；当本地只能证实“待核对”，界面如实显示。不得把 mock 能造出的任意中间态当成生产承诺。
- 人工审核充值备用路径保留；它与禁止手动生成模式是两件不同的事。

## 仍需执行时验证的事项

真实设备软键盘、当前机器的隔离 PostgreSQL 10/编码器/Chromium 可用性、性能基线数值均尚未验证。T01 固化环境和基线；缺环境不得用生产站替代。现有测试名称已查到，但本轮没有复跑，不能声称它们仍全绿。
