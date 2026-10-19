# Tasks：全站手动逐阶段（草案，未批准，禁止编码）

状态：T01–T08 已完成；2026-10-19 已部署；unknown 已释放。

## T01 · 章程与规格确认 ✅

依赖：用户确认 C-01–C-08 与安全停止语义。  
验收：AC-011、AC-014。  
产出：更新 constitution 2.0.0、spec/clarifications 状态；不改业务代码。  
证据：constitution 2.0.0；spec/plan 状态 Approved for T01–T02；全站手动和安全停止边界已记录。

## T02 · workflow_mode 与安全切换设计 ✅

依赖：T01。  
验收：AC-008、AC-009、AC-011。  
范围：
- 迁移 `workflow_mode`，全站 manual；
- 实现一次性停止自动推进的服务/CLI，复用 CancelRun 语义；
- 不直接 SQL 改 run；未提交 queued job 取消并释放预占；
- 已提交/unknown 继续；审计与重放测试；
- 不改变金融表。  
证据：`026_project_workflow_mode.sql`、`manual_mode.go`、`stop-automatic` 命令、`manual_mode_test.go`；全量后端测试通过。**尚未部署、尚未执行生产迁移/停止命令。**

## T03 · 手动阶段服务端门禁 ✅

依赖：T02。  
验收：AC-001–AC-005、AC-008、AC-010。  
范围：
- `script/confirm`、`keyvisual/confirm`；
- 明确旧 `/keyvisual/approve` 兼容策略；
- 项目锁、状态校验、job 创建、audit、并发/重放；
- 手动任务无 run；不自动跨阶段。  
证据：`manual_stage.go`、handler/routes、`manual_stage_test.go`、`manual_stage_http_test.go`；确认前/重复/跨阶段/HTTP 测试通过。

## T04 · 阶段路由与导航 ✅

依赖：T02/T03。  
验收：AC-002、AC-006、AC-012、AC-013。  
范围：
- 四阶段子路由 + 当前阶段重定向；
- `ProjectStageNav` 点击/锁定/键盘；
- 直接 URL/刷新/后退守卫；
- 旧自动执行只读展示。  
证据：`stages.ts`、`ProjectStageNav.tsx`、`ProjectStagePage.tsx`、App 路由、`stages.test.ts`；前端 34 files/146 tests、typecheck/build 通过。T05/T06 的真实操作页未实现。

## T05 · 剧本/定妆照页面 ✅

依赖：T03/T04。  
验收：AC-003–AC-005、AC-007。  
范围：
- 生成/编辑/确认；
- 预览/重做/确认；
- 下一阶段费用提示；
- 余额/失败/unknown 状态；
- 测试确认前无下一阶段任务。  
证据：剧本生成/确认、镜头字段编辑、定妆照确认/重做、费用提示；前端 typecheck/build 通过。

## T06 · 镜头/成片页面 ✅

依赖：T03/T04。  
验收：AC-005、AC-007、AC-010。  
范围：
- 逐镜头通过/取消/重做；
- 全部通过后合成；
- 合成中/失败/完成；
- 成片预览/下载，不自动触发；
- 重做二次确认测试。  
证据：ShotList 操作、镜头通过/取消/重做、合成按钮门槛、成片预览；前端 34 files / 146 tests、typecheck/build 通过。

## T07 · 旧执行兼容与全站停止验证 ✅

依赖：T02–T06。  
验收：AC-008、AC-011。  
范围：
- 旧 run 只读/核对展示；
- 全站停止脚本的隔离库演练；
- 已提交/unknown/queued 三种 job 的边界；
- 不退款、不硬杀上游、不重复扣费；
- 生产停止操作单独审批。  
证据：`manual_mode_test.go` 覆盖 queued/已提交 polling/reconcile、二次 stop 幂等；旧执行阶段页只读提示。

## T08 · 全量验收与独立复审 ✅（代码/单元验收；生产已部署）

依赖：T02–T07。  
验收：AC-001–AC-014。  
范围：
- 后端/前端全量、typecheck/build；
- 四阶段导航、门禁、并发、权限、金额、旧 run 回归；
- 逐条更新证据；未验证项列出；
- 不部署、不执行生产停止，等待单独授权。  
证据：后端全量通过；前端 34 files/146 tests、typecheck/build 通过；浏览器/移动端人工验收未执行（见 completion-t07-t08）。

## 实施顺序

T01 → T02 → T03 → T04 → T05 → T06 → T07 → T08。

## 明确禁止

- 未确认前编码/迁移/停生产 run；
- 用前端隐藏按钮代替后端门禁；
- 硬杀已提交上游或退款；
- 改价格/积分/退款/分佣；
- 生产数据/真实付款/生成测试；
- 未经批准部署。
