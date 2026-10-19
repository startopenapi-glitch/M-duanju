# Manual stage API contract（草案，不实现）

所有路径均在已鉴权 `/api/projects/:id` 下，沿用 `OwnProject`、账号状态、项目存在性校验。

## POST /script/confirm

请求：`{}`  
前置：`workflow_mode=manual`、`status=SCRIPT_REVIEW`、角色/场景/shots 存在、无活动 keyvisual job。  
效果：同事务校验 + 创建 keyvisual job + 状态 `KEYVISUAL_GENERATING` + audit。  
响应：`{ok:true,data:{stage:"keyvisual",jobId:"..."}}`  
重复：已确认/已存在活动任务时返回幂等结果或 409，不重复创建。

## POST /keyvisual/confirm

请求：`{}`  
前置：`status=KEYVISUAL_REVIEW`、`keyvisual_url` 非空、无活动 shots job。  
效果：同事务创建 shots jobs + 状态 `SHOTS_GENERATING` + audit。  
重复：不重复创建。

## POST /shots/:seq/approve / unapprove

沿用现有接口。  
- approve 仅 `GENERATED` 且有 `video_url`；
- unapprove 仅 `APPROVED`；
- 重做沿用现有 `regenerate`，前端二次确认可能再次计费。

## POST /assemble

沿用现有接口。  
前置：所有 shots `APPROVED` 且有 `video_url`；否则 409。  
效果：`ASSEMBLING` → `COMPLETED` + final_url；不自动再次合成。

## GET /projects/:id

现有读取需返回：
- `workflowMode`；
- 当前阶段可由 status 推导；
- jobs/status/shots 现有字段；
- 不返回 token、密钥或内部凭证。

## 错误语义

| 情况 | HTTP | code（建议） |
| --- | --- | --- |
| 阶段未就绪/跳过 | 409 | `stage_locked` |
| 重复确认 | 200 幂等或 409 | `stage_already_confirmed` |
| 余额不足 | 429 | `balance_insufficient` |
| 未知上游提交 | 409 | `submission_unknown` |
| 无权/已删除 | 403/404 | `project_forbidden`/`project_missing` |
