> 2026-10-18 执行状态更新：本方案已获用户批准并完成三批实现；以下为原计划/研究记录。实际验收、偏差与未验证项以 [completion.md](completion.md)、[acceptance.md](acceptance.md) 为准。部署未获执行授权。

# 短剧工厂全站体验统一 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: 批准后使用 superpowers:executing-plans 逐项实施；如用户选择分工，再使用 superpowers:subagent-driven-development。本轮不执行实现步骤。任务均使用未勾选复选框记录。

**Goal:** 以同一工作台完成从想法到成片的自动创作，并真实呈现积分、订单与原执行的关系。

**Architecture:** 保留单体前后端与现有 worker。复用 AppShell、身份保护、焦点工具和业务服务；界面组件不携带业务状态规则。补两个普通用户只读查询，避免前端猜测入账或错误绑定最新执行。

**Tech Stack:** React 18 / Router 6 / Tailwind 3 / Vite 7；Go 1.23 / Gin / PostgreSQL（兼容当前 PG10）；现有 Vitest、Testing Library、隔离 Chromium。

**Spec:** [spec.md](spec.md)（2026-10-18 用户已确认）及 [clarifications.md](clarifications.md)。

状态：技术方案与任务已起草，待本轮用户确认。价格、积分与分佣规则不变；未编码、未部署。

## Global Constraints

- 保留当前价格、积分、预占/扣减/返还、退款、分佣与归属规则。
- 积分足够直接生成，不足提示充值后按既有规则续跑；无预算确认、无人工阶段门禁。
- run_id/项目/账号必须一致；重复点击、通知、刷新、多标签不得产生重复收费或第二个替代执行。
- 未知提交先查证；终态不因到账复活；不清除在途任务或预占。
- 不使用生产数据，不做真实付款、退款或生成；不修改 .env。实现、上线各自等待批准。
- 保留原路由和 API 兼容；不引入 UI 框架、微服务、Redis、全局查询库或新商业设置。
- 375/768/1440px、44px 主要触控区；性能按 SC-007；所有验收证据对应当前提交。

## Review Focus

1. 原执行已完成且新版本出现：充值上下文仍精确指向旧 run，不显示新 run 已由该笔充值恢复（T05/T08）。
2. 支付已记 paid 但 credited=false 或已退款：余额刷新不能掩盖入账异常，更不能再次生成（T05/T08）。
3. A 账号退出、再登录同一账号或 B 账号：旧请求、草稿、轮询与导航回调全部失效，恢复码保护仍有效（T03/T06/T08）。
4. 隐藏页面时查询尚未结束、随后显示/卸载：不能产生第二个定时器或完成后复活轮询（T08/T12）。
5. 同标题项目、创建响应丢失、启动成功但响应丢失：不按标题找项目、不自动补一次创建；已知项目重试必须用原幂等键（T06）。

## 1. 范围及技术变更

前端分三批迁移；后端仅 T04 的项目列表只读筛选和 T05 的两个只读投影。数据库结构及金融写入不变。技术契约见 [contracts/ui-read-models.md](contracts/ui-read-models.md)，领域模型见 [data-model.md](data-model.md)。

| 模块 / 文件 | 职责与修改边界 |
| --- | --- |
| styles/tokens.css（新增）、index.css、layout/layout.css、features/workspace/workspace.css | 用一个 token 源统一外观；旧类暂映射到 token，最后清理。保留业务布局 |
| components/ui/{PageHeader,Field,StatusBadge,Feedback,ResponsiveList}.tsx（新增） | 无 API/角色依赖的基础组件；以现有 ui.tsx 提取为起点，不抽象所有 HTML |
| components/Modal.tsx、layout/useFocusScope.ts、features/workspace/ui.tsx | Modal 接入现有焦点保护；ui.tsx 过渡期保留兼容出口，领域 Status 最终迁出 |
| layout/{AppShell,ProjectRail,AccountMenu}.tsx、layout/nav-config.ts、features/workspace/WorkspaceNav.tsx | Shell 与账户区只保留一份；最近项目和角色导航作为内容槽；AccountMenu 为新增 |
| lib/api.ts、lib/projects.ts、features/creation/ProjectsProvider.tsx（新增） | 保留 auth epoch，暴露非敏感会话代次；项目列表共享请求、分页与刷新 |
| features/creation/{Composer,CreationProvider,useCreateIntent,useProjectRun,projectPresentation}.tsx/ts（新增） | 草稿与提交意图、执行读取和展示相互分离；页面不内联计价或状态机 |
| features/creation/{RunSummary,OutputPreview,ShotList,RevisionComposer}.tsx（新增） | 从现有 Project 拆出的展示；不独立重复拉取同一执行 |
| features/billing/{types,presentation,useRechargeFlow}.ts（新增）、pages/Credits.tsx、components/PayQRCode.tsx | 一个流程控制器持有查单计时、订单事实、原 run；二维码仅展示和触发“查询” |
| pages/Settings.tsx、pages/Security.tsx、App.tsx | 规范账户路由，移除整页嵌套；不改变登录、密码、TOTP、恢复码安全语义 |
| backend/internal/{handler,service}/ui_read.go（新增）、router/router.go、service/run_api.go | 用户只读投影；LatestRun 中共用的读取计算可提取 helper，写入函数不改 |
| backend/internal/service/project_list_read.go（新增）、handler/handler.go、service/user.go 的 ListProjects | 给现有GET /projects补可选关键词/状态过滤，旧签名作为空筛选兼容入口，金融函数不动 |
| pages/Admin*.tsx、features/partner/PartnerPages.tsx、pages/Login.tsx、Register.tsx、PartnerLanding.tsx | 消费统一组件和领域状态；现有业务动作、权限、分页与推广归属保持 |

路径均相对 frontend/src，backend 前缀除外。新增文件的精确扩展名与接口在任务中给出；未列文件若要改须说明直接依赖原因。

## 2. 界面与导航契约

背景 #F6F7F9、面板 #FFFFFF、正文 #162033、次要文字 #566174、边框 #DCE1E8、主动作 #183153。状态色用成功/警告/错误语义 token 并显示文字；最终颜色需实测对比度。标题 28/24px、区块18px、正文14–16px、手机输入16px、辅助12–13px；主要控件44px，间距4/8/12/16/24/32，控件/卡片/弹层圆角10/14/16px。保留系统字体与现有品牌，不引入外部字体请求。

桌面侧栏240px；表单上限760px，普通内容1200px，视频详情可1400px；320px重排（200%缩放场景）不得横向撑宽。<768px 使用抽屉；图片/视频保留固有比例，页面主动作不能覆盖软键盘输入。每页一个 h1，弹层标题用 h2。动画120–200ms，遵循 reduced-motion。

账户区固定：积分与充值、账户设置、按身份显示的推广合作/运营管理、退出。各空间内导航保持创作主入口和六项管理/四项推广，不把账户入口同时复制到页头。访客页面只复用视觉原语，不显示登录后侧栏。身份查询失败显示可重试状态，不伪装成普通账号。

路由：/settings?tab=account 为账户安全唯一页面；/security replace 到该页；/settings?tab=billing replace 到 /credits 并保留经过验证的 runId/orderId；/settings?tab=admin 仅管理员 replace 到 /admin/stats，其他身份拒绝进入。无参数 /settings 显示账户页。未知 tab 回 account，不从任意 returnTo 直接导航。/credits?runId=…&orderId=… 是本次拟新增的可恢复订单上下文，必须双重验属且核对绑定一致。

## 3. 共享组件与数据接口

以下是计划接口，尚未实现；函数体由任务实现，不提前生成代码。

- `PageHeader({title, description?, actions?})`：title:string；description/actions:ReactNode；输出 h1 与操作区。
- `Field({id,label,error?,hint?,children})`：关联 label/aria-describedby；错误文字就地显示，不隐式读表单值。
- `StatusBadge({label,tone})`：从该组件文件导出`BadgeTone = 'neutral'|'info'|'success'|'warning'|'danger'`；不接收数据库状态。
- `ResponsiveList<T>({items,getKey,columns,renderMobile,empty})`：columns 定义 id/header/cell；只渲染当前断点一份交互树，手机不得隐藏操作。沿用现有 Pager。
- `AccountMenu({onNavigate})`：复用 useAuth/usePartnerIdentity，不另发身份请求；退出复用 clearToken，恢复码临时保护不丢。
- `useSessionScope(): string`（lib/api.ts）：不含 token 的进程内会话代次；token变更/跨标签变化递增，用于 provider key，禁止记录 token 作缓存键。
- `useProjects(account,reloadKey?,filter?)` 向后兼容原签名和返回形状，filter为`{q?:string,status?:string}`，由ProjectsProvider支撑；`projectsChanged()`仍可用，刷新已载页，不把当前浏览位置重置成首页。相同会话+筛选+页码去重；首页和侧栏筛选不同可以分别查询，不能把全量侧栏也改成筛选子集。
- `useCreateIntent(): {phase, submit(draft), check(), retrySameIntent(), error}`：phase为`idle|creating-project|creation-unknown|starting-run|start-unknown|ready|error`；draft为`{idea,aspect,totalSeconds,filmReference,mood}`，字段类型沿用现有创建请求；内部保存原requestKey/已知projectId，不暴露自动“换键重来”。
- `useProjectRun(projectId): {project,runView,readState,error,refresh,act}`：唯一页面级查询所有者；保留当前 projectId/请求代次/动作锁保护。
- `presentProject(project,runView,readState): ProjectPresentation`：纯展示，详见 data-model；不执行请求、不算费用。
- `useRechargeFlow({runId?,orderId?}): {balance,orders,context,progress,phase,error,recharge(amountCents),check(),retry()}`：phase为`loading|ready|creating-order|tracking|credited|continuation-observed|paused|error`，只控制界面流程，不新增金融状态；读请求统一取消，新订单金额为当前套餐整数分，不接受前端优惠/加赠算法。

不建立全站万能事件总线。到账后的显式回调只通知账户余额刷新、projectsChanged 和当前 flow 重新读取；账户 Provider 与项目 Provider 按会话代次销毁。查询结果至少绑定会话+资源身份；账户层不依赖充值页面组件。

会话代次用非敏感随机值保存到localStorage独立键`drama_session_revision`，setToken/clearToken时同步轮换，其他标签监听token/revision变化并清理旧状态；不存token副本或把token打印为key。sessionStorage内意图绑定该revision，刷新同一会话可恢复，换token即使同账号也失效。首次升级没有revision时创建；生成revision不提交任何业务请求。保留api.ts原有token比较与epoch保护，两者都不能删。

## 4. 创作与充值流程

新建面板的默认值保留16:9、15秒、现有视觉参考与情绪；默认折叠次要字段。费用显示现有估算的 estimatedCredits，人民币说明只有可核实口径时显示。参数变化使旧估算失效；估算失败有重试，不默认为零。

Composer 与 Shell 中 CreationProvider 共用草稿。普通草稿仅内存保存；已提交但响应未知的 requestKey/已知项目/非敏感输入快照可存 sessionStorage，按账号+会话代次隔离并在退出清理。退出后重新登录不自动提交。创建项目响应丢失：停在“项目创建结果待核对”，只刷新项目列表，不自动重发 POST /projects。启动 run 响应丢失：查原 requestKey；确认查询无记录仍不能证明首个请求终止，用户点恢复时只重放相同键、相同输入，绝不自动换键。

项目首屏为 RunSummary + OutputPreview（有结果）或紧凑进度；四阶段映射明确，审片是内部处理状态而非用户确认关卡。未知 job 状态优先显示核对警告。重新授权/修改版是新的明确动作，查询和到账刷新绝不触发它们。

支付控制器保留原两条路径：微信扫码、人工审核。微信查单仍调用原对账接口；只有该页面明确在追踪订单时执行，不能在全局资源预取中调用。新只读 progress 决定“是否入账”，原 run context 决定“现在是否排队/运行/完成”。三者可同时完成，不人为加等待动画，也不虚构先后事件时间。人工审核没有渠道付款节点。

同一订单微信查询2秒起、失败指数退避上限30秒、等待上限10分钟；同一流程最多一个在途网络周期。隐藏时清定时器，恢复一次核对；超时只表示暂停自动查询，不代表订单关闭。订单仍可手动查原订单，不强制新建。pending run 查询5秒、错误退避至30秒；等待续跑超过60秒显示仍在核对与返回项目，不标失败、不创建新执行。run终态/旧版本停轮询。

URL orderId 支持重开查询原订单，不重新下单；金额、runId 以服务端绑定为准。URL与订单绑定冲突时阻断该上下文，提示使用已核验的订单记录，不静默改绑定。入账后的导航必须同时满足当前会话、当前订单、用户仍在充值流程、原项目可访问；一次有效导航消费一次，不重复跳转。

## 5. 三批、验证与发布准备

| 批次 | 任务 | 独立交付边界 |
| --- | --- | --- |
| 1 一致性基础 | T01–T04 | 合成基线与统一组件、导航/设置、共享项目与双高亮修复 |
| 2 创作与充值 | T05–T08 | 两个只读投影、稳定创作意图、结果优先项目页、真实订单/续跑反馈 |
| 3 全站收敛 | T09–T14 | 登录/账户/推广/管理迁移、交付反馈、性能与全量验收、复审和发布材料 |

依赖与步骤在 [tasks.md](tasks.md)。验证命令及环境安全在 [quickstart.md](quickstart.md)。需求覆盖和本次修正记录在 [analysis.md](analysis.md)。每批可单独审查，最终不能以一批通过代替全站通过。

无数据库迁移，旧 API 保留；发布时先发布支持新读取的后端，再发布前端，旧前端可继续运行。回滚先前端再后端，不回滚业务账本、不清理 run/wakeup。必须在真正部署前核对 systemd、当前 release 与备份；本轮不修改服务器。

建议由当前执行者顺序实现共享基础和闭环，结束后独立 reviewer 复审；共享 API/状态边界较多，不建议多人同时改 AppShell、api.ts 或充值控制器。执行方式由用户在本方案确认时确定。
