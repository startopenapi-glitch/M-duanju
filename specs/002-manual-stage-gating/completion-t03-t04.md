# Completion：T03–T04

日期：2026-10-19 · 状态：Implemented / **未部署**  
范围：后端剧本/定妆照确认门禁、四阶段路由与导航只读壳。T05/T06 操作页未实现，生产迁移/切换未执行。

## T03 后端门禁

- `POST /projects/:id/script/confirm`：
  - 仅 manual、`SCRIPT_REVIEW`、完整角色/场景/镜头；
  - 有活动 keyvisual job 时幂等返回原 job；
  - 调用既有 `EnqueueJob`，由同一入队事务创建定妆照 job/预占/状态；
  - 写 `script_confirmed` 审计。
- `POST /projects/:id/keyvisual/confirm`：
  - 仅 manual、`KEYVISUAL_REVIEW`、`keyvisual_url` 存在；
  - 有活动 shot job 时拒绝；
  - 调用既有 `GenerateShots`，由 queueShots 创建镜头 job/预占/状态；
  - 写 `keyvisual_confirmed` 审计。
- 旧 `/keyvisual/approve` 改为确认别名，不再 no-op。
- 手动项目的旧 `/keyvisual/queue` 只能从 `SCRIPT_REVIEW` 进入；`/shots/queue` 只能从 `KEYVISUAL_REVIEW`/`SHOTS_REVIEW`/`FAILED` 进入，防止绕过确认。
- HTTP `POST /projects/:id/runs` 对 manual 项目返回 409 `manual_mode`；服务层保留旧方法用于历史/内部测试。
- 测试：service 确认、重复、错误阶段、HTTP 路由、手动 start 拒绝、锁定阶段不建 job；全量后端测试通过。

## T04 前端路由/导航

- 路由：
  - `/project/:id` 重定向当前阶段；
  - `/project/:id/script`
  - `/project/:id/keyvisual`
  - `/project/:id/shots`
  - `/project/:id/final`
- `stages.ts`：状态→阶段、阶段锁定、当前路径；`stages.test.ts` 3 条断言。
- `ProjectStageNav`：四阶段可点击；完成/当前/锁定；未来阶段 `aria-disabled`。
- `ProjectStagePage`：只读阶段壳；直接访问未来阶段自动回当前阶段。
- 手动项目移除自动启动入口；旧执行显示历史说明。
- `Project` 类型增加 `workflowMode`。
- 前端 34 个测试文件、146 个测试通过；typecheck/build 通过。

## 明确未完成

- T05：剧本/定妆照的生成、编辑、确认按钮和费用提示。
- T06：逐镜头通过/取消/重做、全部通过后合成、成片操作。
- 因此当前阶段页是**只读壳**，不能完成一次手动成片。
- 生产尚未应用迁移 026，未执行 `stop-automatic`，未部署本批代码；线上仍是旧自动流程。
- 生产切换、全站停止、部署需单独批准。
