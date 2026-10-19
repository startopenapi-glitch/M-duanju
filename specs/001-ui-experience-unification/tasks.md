# 分批实施任务

状态：2026-10-18 用户已确认实施，按三批推进，三批 T01–T14 的代码/隔离验收与交付已完成，AC-019 真机验证保留未做。执行前读 plan.md、spec.md、data-model.md、contracts/ui-read-models.md；实施已获授权，部署仍未获授权。

以下为批准的原始任务清单。步骤保留为原计划，不用勾选掩盖文件名调整或真机未做；实际完成状态和证据见下表及 completion.md。每任务包含失败用例→最小实现→回归→独立可审查提交。提交只包含本任务文件；交叉修改先核对最新 diff，不撤销他人工作。以下路径均为仓库相对路径。

## 第一批：统一基础

### T01 · 固化隔离验证与前后基线（US1/US6）

依赖：用户批准计划。验收：AC-002、AC-021、AC-023。

文件：新增 verification/ui/{run,fixtures,serve,measure}.mjs；按需适配 verification/release_gate.py；证据保存 .test/ui-unification/，不提交密钥、产物或数据。

接口：`run.mjs --suite <baseline|navigation|creation|billing|workspaces|all> --base <loopback-origin>`；measure.mjs接收同样base及`--label before|after --runs 5`。fixtures只允许合成API，未知API/外站请求必须失败。

- 检查现有隔离库、编码器、运行时；复用测试白名单及干净环境，适配工具路径，不读取生产.env。
- 将历史capture夹具中的mode=automatic修正为真实mode=first，使用真实ID格式；不复制虚假API行为。冻结一份基线夹具供前后共用。
- 加入`mobile_home_long_summary_does_not_overflow`及`project_has_one_current_stage`，在6b902ed构建复现失败；其他测试失败如实列出。
- 在独立临时基线目录构建6b902ed，不重置当前工作区；记录三尺寸全部路由基线，以及首屏/固定操作/包体各5次数据；无相应环境时记录未验证，不造数字。
- 验证外网阻断、自造数据清理、没有真实支付/生成请求，单独提交验证基础。预期：两个目标布局断言红，安全隔离断言绿。

### T02 · 公共视觉与交互原语（US1/US5/US6）

依赖T01。验收AC-002、AC-018、AC-019。

文件：新增 frontend/src/styles/tokens.css、components/ui/{PageHeader,Field,StatusBadge,Feedback,ResponsiveList}.tsx、components/ui/primitives.test.tsx；修改 index.css、layout/layout.css、features/workspace/{ui.tsx,workspace.css}、components/Modal.tsx。

接口：严格使用plan中五个原语签名；Modal原props保持兼容，可新增可访问标题props，复用useFocusScope。旧ui.tsx只作过渡适配，业务状态仍放领域层。

- 写`field_error_is_described`、`modal_restores_focus`、`mobile_list_has_one_action_tree`：断言label/error关联、Escape返回触发点、每行只有一个可聚焦业务动作。
- `cd frontend && npx vitest run src/components/ui/primitives.test.tsx`；先确认未实现导致失败。
- 按plan token实现，消除.ws-shell覆盖全局按钮的双规则，保留现有动效减少选项。
- 重跑上述测试、typecheck，以及三尺寸基础控件截图；对比度与44px触控实测。
- 仅提交公共组件及适配；不得顺带改业务状态/请求。

### T03 · 一套导航、账户入口与兼容路由（US1/US5）

依赖T02。验收AC-001、AC-016、AC-017。

文件：新增 frontend/src/layout/AccountMenu.tsx、layout/navigation.test.tsx；修改App.tsx、layout/{AppShell,ProjectRail}.tsx、layout/nav-config.ts、features/workspace/WorkspaceNav.tsx、pages/Settings.tsx、lib/api.ts及lib/api.test.ts。

接口：AccountMenu({onNavigate})；角色项目沿用useAuth/usePartnerIdentity，统一nav-config，不另建角色系统。本任务提供plan中的useSessionScope及revision轮换，供T04/T06/T08使用；保留原API晚响应与恢复码保护。

- `navigation_is_unique_per_role`覆盖普通/推广/管理员/暂停/身份失败；`legacy_settings_keeps_verified_context`覆盖/security、billing/admin参数、外部returnTo；`session_scope_survives_reload_but_rotates_on_token_change`断言刷新保持代次、同账号换token/跨标签变更失效。
- `cd frontend && npx vitest run src/layout/navigation.test.tsx src/lib/api.test.ts src/pages/AdminWorkspace.test.tsx`；新增断言先失败。
- 统一账户区与手机抽屉；保留六项管理/四项推广；移除Settings嵌套Credits，复用Security内容，旧路径replace跳转。
- 验证刷新/后退、详情返回、匿名直达、角色撤销，浏览器navigation套件通过。
- 提交导航与路由；任何隐藏菜单不得替代后端鉴权。

### T04 · 共享项目列表与阶段单高亮（US1/US3）

依赖T02/T03。验收AC-002、AC-003、AC-007。

文件：新增 frontend/src/features/creation/{ProjectsProvider.tsx,projectPresentation.ts,projects.test.tsx,presentation.test.ts}；修改lib/projects.ts、layout/AppShell.tsx、layout/ProjectRail.tsx、pages/{Home,Project}.tsx；新增backend/internal/service/project_list_read.go、project_list_read_test.go，修改handler/handler.go的ListProjects、service/user.go的ListProjects兼容委托。

接口：useProjects(account,reloadKey?,filter?)兼容原调用，保留projectsChanged；presentProject按data-model；ListProjectsFiltered及可选q/status按接口契约。provider按会话+筛选共享已载页，请求记录不含token。

- 写`home_and_rail_share_project_fetch`、`refresh_preserves_loaded_pages_and_scroll`、`shots_review_has_single_stage`与`TestProjectListFilterCountsAndScopesAllPages`，断言同查询一份请求、返回位置保留、阶段唯一、筛选覆盖全量而非仅已载页。
- `cd frontend && npx vitest run src/features/creation/projects.test.tsx src/features/creation/presentation.test.ts`；基线先红。
- 修复列表外层min-width/长文本布局，不使用body overflow-x:hidden；项目优先，空态复用创建入口；接入四阶段显示。
- 重跑T01两条红用例为绿、共享请求/分页边界和账号变更；后端隔离`go test ./internal/service -run TestProjectListFilter -count=1`通过。
- 提交第一批闭环；记录尚未迁移页面，不宣称全站改造完成。

## 第二批：创作与充值闭环

### T05 · 精确原执行与订单只读投影（US2/US4）

依赖T01，契约以contracts/ui-read-models.md为准。验收AC-010–AC-014、AC-017、AC-022。

文件：新增 backend/internal/{service,handler}/ui_read.go、各自ui_read_test.go、router/ui_read_test.go；修改 router/router.go、service/run_api.go（仅共用读取helper）；新增 frontend/src/features/billing/types.ts和features/creation/types.ts。

接口：RunReadContext(ctx,userID,query)、OrderProgress(ctx,userID,orderID)，字段、null/404/500及隐私规则严格按契约。

- 写`TestUIRunContextUsesExactRun`、`TestUIRunContextLookupScopesRequestKey`、`TestUIOrderProgressReadsCreditedFact`、`TestUIReadsDoNotMutateOrCallProviders`，旧路由应404/字段缺失而失败。
- 隔离环境中 `cd backend && go test ./internal/service ./internal/handler ./internal/router -run TestUI -count=1`，确认红因目标行为缺失。
- 新增两个GET，只读取现有记录；复用现有资金/结算缺口读取，不修改settleOrder、StartRun、ResumeRunChecked或任何迁移。
- 重跑新测试及既有pay_run、refund、production_compat回归；断言读取前后订单/积分/run/jobs/wakeup无变，越权和失败无敏感字段。
- 提交只读能力及前端类型。旧端点响应兼容；禁止修改测试中的价格/积分期望。

### T06 · 一个创作入口与稳定提交意图（US2）

依赖T02/T04/T05。验收AC-004、AC-005、AC-006。

文件：新增 frontend/src/features/creation/{Composer.tsx,CreationProvider.tsx,useCreateIntent.ts,composer.test.tsx,intent.test.tsx}；修改layout/{NewProjectDialog,AppShell}.tsx、pages/Home.tsx；使用T03提供的useSessionScope，不重复实现会话代次。

接口：useCreateIntent和useSessionScope按plan；Composer接收draft/onChange/onSubmit/estimate/busy/error，不自己创建项目。

- 写`starts_once_without_budget_modal`、`scope_change_invalidates_estimate`、`unknown_project_create_is_not_reposted`、`unknown_run_start_keeps_request_key`、`draft_is_erased_on_session_change`。
- `cd frontend && npx vitest run src/features/creation/composer.test.tsx src/features/creation/intent.test.tsx src/lib/api.test.ts`；确认缺失行为失败。
- 保留既有默认值，折叠风格/情绪，费用以服务端积分估算为准；保存稳定意图，原键重试只由显式动作触发。
- 验证双击、未知响应、相同标题、慢估算覆盖、切账号/同账号重登；浏览器creation套件拦截真实生成。
- 提交入口闭环；POST /projects响应未知的安全停顿作为明确例外，不声称可无感自动恢复。

### T07 · 项目结果优先与真实状态（US3）

依赖T04/T06。验收AC-007、AC-008、AC-009。

文件：新增 frontend/src/features/creation/{useProjectRun.ts,RunSummary.tsx,OutputPreview.tsx,ShotList.tsx,RevisionComposer.tsx,project-flow.test.tsx}；修改pages/Project.tsx、projectPresentation.ts。

接口：useProjectRun集中读project/latest与动作保护；子组件只收props。显示最新项目时可用latest，收到指定历史run上下文时不得冒充当前版本。

- 写`unresolved_job_blocks_blind_retry`、`previous_output_is_not_current_success`、`settlement_pending_is_not_zero_credit_shortfall`、`run_read_error_is_not_legacy`。
- `cd frontend && npx vitest run src/features/creation/project-flow.test.tsx src/pages/Project.test.tsx src/pages/review-run-state.test.tsx src/pages/FinalReview.test.tsx`，新增行为先红。
- 拆页面为编排和展示；保留取消锁、epoch与canRevise门禁。只读详情折叠，不提供手动阶段生成按钮。
- 验证完成/上一版本/历史无run/失败/取消/unknown组合，首屏作品与小屏镜头列表可用。
- 提交项目页；已有服务端状态值一个也不重命名。

### T08 · 支付追踪、积分与原run同步（US4）

依赖T05/T06/T07。验收AC-010–AC-015、AC-020、AC-022。

文件：新增 frontend/src/features/billing/{presentation.ts,useRechargeFlow.ts,recharge-flow.test.tsx,presentation.test.ts}；修改pages/Credits.tsx、components/PayQRCode.tsx、features/creation/RunSummary.tsx。

接口：useRechargeFlow按plan；PayQRCode只接订单展示/阶段/查询回调，无内部重复定时器。新orderId深链与原runId都按服务端核对。

- 写`paid_false_credit_is_not_success`、`manual_approval_has_no_wechat_stage`、`old_run_is_not_latest_run`、`hidden_query_cannot_spawn_second_timer`、`late_paid_event_cannot_redirect_new_account`、`order_binding_mismatch_blocks_context`。
- `cd frontend && npx vitest run src/features/billing/recharge-flow.test.tsx src/features/billing/presentation.test.ts src/pages/ProductionCommercial.test.tsx`；记录红结果。
- 实现单查询周期、隐藏暂停/退避/超时查询原单；manual降级保留id；到账显式刷新余额、订单、原run和项目，不POST新run、不写wakeup。
- 跑隔离billing浏览器与后端重复通知/不足额/取消/结算/退款用例；保留套餐金额、amountCents、可用积分口径完全一致。
- 提交充值闭环；支付未知/历史paid未入账只提示核对，不自动修金融数据。

## 第三批：全站统一与收敛

### T09 · 登录、账户与会话边界（US5/US6）

依赖T02/T03/T06/T08。验收AC-001、AC-005、AC-017–AC-019。

文件：修改frontend/src/pages/{Login,Register,Security,PartnerLanding}.tsx、App.tsx；新增features/account/account-ui.test.tsx；保留lib/recovery与原会话保护。

- 写`account_relogin_invalidates_previous_scope`、`recovery_codes_remain_visible_until_ack`、`auth_field_errors_are_accessible`；复用原auth-ui测试。
- `cd frontend && npm test`，新增用例应先红，现有鉴权失败先排查而非删除。
- 统一字段/错误/标题和移动布局；只调整显示，保留邀请码、验证码、TOTP和恢复码操作语义。
- 验证键盘、自动填充、跨标签退出、迟到响应、旧会话，截图不含真实身份/密钥。
- 提交账户迁移；不趁机更改密码或权限政策。

### T10 · 管理与推广逐页统一（US5）

依赖T02/T03/T09。验收AC-001、AC-017、AC-018、AC-022。

文件：修改pages/Admin{Stats,Jobs,Orders,Users,Partners,System}.tsx、features/partner/PartnerPages.tsx、features/workspace/{ui.tsx,workspace.css}；新增features/admin/presentation.ts、features/partner/presentation.ts与pages/WorkspaceVisual.test.tsx。

- 写`workspace_mobile_preserves_all_actions`、`pending_payment_differs_from_pending_receipt`、`unknown_refund_stays_query_only`；覆盖六管理/四推广页面。
- `cd frontend && npx vitest run src/pages/WorkspaceVisual.test.tsx src/pages/AdminWorkspace.test.tsx`，先红后改。
- 统一列表/详情/过滤器，不改原命令、分页、版本锁、审计或管理员危险操作确认；各领域自己传Badge文案/色调。
- 验证暂停代理、脱敏客户、查询失败、旧记录、部分退款、未知退款、复制失败兜底；没有开启分佣就不显示可提现收益。
- 按页完成后移除旧适配；无引用的旧Layout只有搜索与测试证明后才删，不整库搬目录。

### T11 · 交付物、移动端与异常交互验收（US3/US6）

依赖T07/T09/T10。验收AC-002、AC-008、AC-018、AC-019。

文件：扩展verification/ui/{run,fixtures}.mjs；问题修复只在所属任务模块；新增features/creation/output-preview.test.tsx。

- 写`download_failure_keeps_version_context`、`expired_output_is_not_success`，使用本地合成视频/下载响应，绝不取生产素材。
- 复跑三尺寸、200%缩放、长内容、所有目标路由；每个缺陷保留失败截图与断言。
- 修复溢出、焦点、下载反馈和小屏遮挡；媒体下载URL不通过追加token绕过鉴权，不改变服务端存储权限。
- 真机检查软键盘、安全区、横竖屏；缺设备明确“未验证”，不把Chromium模拟当真机。
- 提交回归测试和对应小修；证据保存到验收目录并逐条关联AC。

### T12 · 测量驱动的性能收敛（US6）

依赖T04/T08/T10/T11。验收AC-020、AC-021。

文件：verification/ui/measure.mjs；必要时App.tsx、useProjectRun.ts、useRechargeFlow.ts、OutputPreview.tsx、ShotList.tsx及frontend/src/features/creation/polling.test.tsx。

- 固定5次冷启动和固定操作脚本，与T01同夹具、同浏览器配置比较；保存原始样本而非只写平均值。
- `polling_stays_single_flight_after_visibility_burst`断言一次在途、隐藏后不再发起、恢复仅一次；明确生产首屏LCP与交互脚本延迟口径。
- 仅按实测优化路由分包、重复列表请求、定时器、离屏媒体preload；不新增骨架计时伪装快、不砍业务步骤满足指标。
- SC-007超容差不得通过；重新跑受影响功能与整个性能样本，列明收益/无收益。
- 提交有证据的优化；未达到目标的项目不能写“性能已优化完成”。

### T13 · 全量回归与独立复审（全部）

依赖T01–T12。验收AC-001–AC-024。

文件：验证产物.test/ui-unification/；复审report.md、coverage.json；任何修复回所属模块与测试。

- 运行quickstart的全量Go、race、前端测试、typecheck、build及PG10回归；统计实际执行/通过/失败/跳过，缺库不能冒充绿灯。
- 运行隔离Chromium all套件，逐条更新acceptance结果；旧/新API兼容及价格积分基线必须通过。
- 独立reviewer对代码、失败→通过证据、状态与隐私边界复审，不采信完成声明；本任务授权不意味着允许真实交易或收费生成。
- 每条新回归先复现、再修复、再跑影响范围；高风险或未验证核心项不放行。
- 提交最终代码和证据索引，分析spec/plan/tasks实现偏差；必须把未做项列出。

### T14 · 收敛报告与发布/回滚提案（全部）

依赖T13。验收AC-023、AC-024。文件：本目录completion.md、release-plan.md（实施完成后创建）。

- 汇总需求→任务→提交→测试→证据，所有勾选都可追溯；任何关键FAIL/未验证明确阻断。
- 明确无迁移；写出后端→前端发布、前端→后端回滚与不改账本的操作清单，不执行命令。
- 上线前要求重新核对systemd/current release、备份校验和、制品版本和只读冒烟；不使用本地旧路径直接覆盖线上。
- 给出真实移动设备/真实渠道未验证项和替代证据边界，请用户确认发布；未批准停在待上线。
- 提交交付文档。Converged仅代表已约定验收收敛，不代表已部署或真实交易验证。

## 依赖图与执行方式

T01→T02→T03→T04；T01→T05；T04+T05→T06→T07；T05+T06+T07→T08；T02+T03+T06+T08→T09→T10；T07+T09+T10→T11→T12→T13→T14。

T05可在基础UI之后独立开发，但同一时段只由一人修改router/run_api/api.ts等共享文件。本方案推荐顺序实施，最后独立复审；没有自动启动其他agent的授权。

## 实际收敛记录

| 任务 | 实际状态与证据 |
|---|---|
| T01 | 完成；470Go/53Vitest+4Node基线、48截图、原始性能、两条红断言 |
| T02–T04 | 完成；共享原语/导航/项目缓存与筛选；bb24d12，原67前端+项目筛选回归 |
| T05 | 完成；197da80，HTTP验属/无写快照/故障与精确身份测试；后续窄拒绝code见偏差说明 |
| T06–T08 | 完成；8f5c381及两轮复审修复；稳定意图、原run/原订单、到账刷新回归 |
| T09–T10 | 完成；复用现有鉴权与管理测试组织，公共UI与领域状态分离；旧业务动作保留 |
| T11 | 隔离部分完成；48截图+72交互；真机软键盘、安全区未验证（AC-019） |
| T12 | 完成；6路由×5样本前后比较通过SC-007；无生产速度结论 |
| T13 | 完成隔离门禁与独立代码复审；Go/race/PG10各478、前端100+4，无测试跳过 |
| T14 | 完成；completion/coverage/release-plan与薄证据索引；不部署 |

不为达成形式勾选新建重复测试文件，行为覆盖映射见coverage.json。未知创建的安全停顿、真实设备未验证和真实交易禁止均保留，不把模拟结果包装为线上验收。
