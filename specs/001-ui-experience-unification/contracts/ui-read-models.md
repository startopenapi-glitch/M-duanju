# UI 只读接口契约（已实现并隔离验收）

新增以下两个读取端点，并给既有项目列表增加可选筛选。保留既有 `{ok:true,data:...}` 封装及 /api 前缀；无副作用、无渠道调用、不新建任务、不结算。响应 `Cache-Control: private, no-store`。普通用户入口只允许本人，不为 admin 增加跨用户特权；管理员继续使用原管理接口。

## 1. GET /api/runs/context

只接受两种互斥选择器：

- `?runId=<id>`：查询原执行，不能回退到 latest。
- `?projectId=<id>&requestKey=<key>`：查询该账号在该项目下的原幂等提交结果。

未知参数忽略；混合/不完整选择器400。ID格式沿用现有模型：runId为32位十六进制；projectId按现有项目ID解析；requestKey非空且≤64字符，不改变 StartRun 接收规则。不可使用 requestKey 检索其他账号或其他项目。

200数据：`RunContext | null`。null只用于“已经验证本人项目，但该 requestKey 暂无执行”；不是允许换键重发的证明。runId不存在、项目删除或非本人统一404；无登录/旧会话/停用由现有鉴权链拒绝。内部读取错误500，不能退成null。

`RunContext = { run: GenerationRun, project: {id:string,title:string,status:string}, isLatest:boolean, hasUnresolvedSubmission:boolean, availableCredits:number, shortfallCredits:number, observedAt:string }`。

GenerationRun复用现有返回字段，不暴露 user_id/input_hash/scope/定价快照。hasUnresolvedSubmission只根据该run的job提交未知事实；不得把所有reconcile任务都当作未知提交。积分与结算缺口复用 LatestRun 中读取计算；不得从 creditsEstimated 自造另一套补差口径。isLatest按本项目当前版本判断，不修改旧run。

拟新增 Go 接口：`RunReadContext(ctx context.Context, userID string, query RunContextQuery) (*RunContext, error)`；`RunContextQuery{RunID, ProjectID, RequestKey string}`，位于 service/ui_read.go。handler只解析和映射错误；SQL参数化，查询同时限制run归属、项目归属和账号有效。现有LatestRun可复用提取出的读取helper，StartRun/CancelRun/worker不改。

返回内容代表 observedAt 附近读取事实，不承诺多个请求原子快照。UI必须核对响应中的projectId/runId/版本，不把两个时刻的不同执行拼成一个状态；出现新版本后旧上下文仅只读。

## 2. GET /api/orders/:id/progress

200数据：

`OrderProgress = {id:string,status:string,payMode:string,amountCents:number,credits:number,credited:boolean,runId:string|null,projectId:string|null,paymentEvidence:'recorded'|'unknown'|'manual',refund:null|{status:string,amountCents:number},observedAt:string}`。

ID对应订单不存在或非本人统一404，查询异常500，无登录/停用按既有鉴权拒绝。projectId仅在原run和项目仍属该用户时返回，否则null，不返客户邮箱、二维码、凭据、商户交易号、管理员备注或内部错误。

字段必须从精确订单SELECT获得；不能复用 ListOrders 未填充的 credited/runId 默认值。refund从现有refund_intents只投影必要字段；已知历史退款但无可核实意图记录时用`legacy_unverified`，不得伪装成none或新成功。`paymentEvidence=recorded` 要求微信订单存在已有核验入账过程保存的交易凭据；manual表示人工路径，其余unknown。recorded不是发起一次实时查渠道。

拟新增 Go 接口：`OrderProgress(ctx context.Context,userID,orderID string) (*OrderProgress,error)`，位于service/ui_read.go。不直接序列化 RechargeOrder；读取先验属再投影，退款/原run不可见不泄漏其数据。

## 3. 既有 GET /api/projects 的可选筛选

保留page/pageSize和响应items/total/page/pageSize，增加q（trim后最长100字符，按字面匹配title或idea，不把%/_当用户通配符）、status（空为全部，非空仅接受项目既有合法状态；非法400）。先按当前用户过滤，再做关键词/状态、count、排序和分页，不允许传userId扩大归属。旧调用省略新参数时结果保持。

拟新增`ListProjectsFiltered(userID string,page,pageSize int,filter ProjectListFilter) (*model.PageResp,error)`，`ProjectListFilter{Query,Status string}`位于service/project_list_read.go；原ListProjects用空filter委托，避免双份SQL。已有排序规则保持，添加稳定id兜底保证并列时间可重复分页。首页URL保存q/status，侧栏默认仍为全量列表；各查询缓存按规范化筛选隔离。

验证跨页命中、总数、无命中、字面%/_、非法状态、超长输入、账号隔离、筛选切换回第一页和返回后恢复原页；不新增数据库索引/迁移，性能由隔离样本测量。

## 4. 与原端点的衔接

| 端点 | 本轮处理 |
| --- | --- |
| GET /api/projects/:id/runs/latest | 项目工作台继续用；充值必须用精确run，不能代替原run读取 |
| POST /api/projects/:id/runs | 保留请求体、幂等和计费语义；界面显式开始/重放同一提交才调用 |
| POST /api/pay/create | 保留 amountCents/channel/runId；manual降级返回id也要跟踪，不能丢弃 |
| GET /api/pay/status/:id | 既有主动对账入口，有补入账/关单副作用；仅明确追踪的支付流程调用，不做全站预取 |
| POST /api/pay/orders/:id/confirm | 保留“查询支付结果”；不改为新建订单或再次付款 |
| GET /api/credits、GET /api/orders | 继续余额/流水/分页读取；列表最多展示当前接口能证实的信息，详情用progress |

## 5. 契约验收

本人、他人、失效会话、删除项目、过期run、新旧版本、错requestKey、双选择器、非法ID、数据库异常全部覆盖。两端点在隔离测试中重复读取，前后业务表行数/状态/余额/预占/wakeup不变，模拟上游调用次数为0。只读方法不调用命令方法由静态复审补证；通过测试不替代代码检查。

旧前端在新后端通过原契约测试；新前端遇到旧后端404时显示功能暂不可用，不能伪装普通充值或静默丢runId。部署顺序为后端→前端，回滚为前端→后端。

## 复审追加：明确拒绝的兼容错误标识

POST `/api/projects/:id/runs` 原409状态及error文本保留；仅在 `planRevision` 失败、尚未进入 `CreateWithSetup` 的分支添加 `code: "run_not_created"`。提交后读取失败、未知事务结果、普通409不可携带该code。此code仅说明本次请求尝试，不证明相同幂等键此前在途尝试也未提交：客户端只允许本次新意图第一次请求明确拒绝时清键；恢复/重放/曾未知时继续查询或重放原键。原状态机、价格、预占和扣费不变。
