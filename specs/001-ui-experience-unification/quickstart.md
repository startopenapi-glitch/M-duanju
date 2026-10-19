> 2026-10-18 执行状态更新：本方案已获用户批准并完成三批实现；以下为原计划/研究记录。实际验收、偏差与未验证项以 [completion.md](completion.md)、[acceptance.md](acceptance.md) 为准。部署未获执行授权。

# 执行与验证说明（尚未执行）

## 起点与授权

项目根：`/Users/wanglinhong/Documents/ChatGPT/vibe coding/work/drama-admin-upgrade-20261018/source`。开始时运行git status和git log，读本目录规范/方案/任务及可能新增的AGENTS.md。本轮用户只批准写方案；获得下一次实施确认前不要创建产品组件、安装产品依赖、修改API或运行部署。

实施时复用合适隔离worktree；保留现有变更，不为了目录名切换/重置他人工作。

## 测试安全准备

- 本地或隔离服务器的测试DB必须名为`drama_remediation_test_20261017`；用受限测试账号，不能指向生产连接。连接保存在.gitignore忽略的.test/database_url（0600），日志不得打印DSN。
- 使用现有testutil.DB随机schema和清理机制；核对所有数据库测试入口。验证保护拒绝错误DB，在打开连接前就失败。
- 测试进程环境仅继承必要PATH/HOME/LANG/TZ等；LIMENG只指loopback模拟服务，凭据为synthetic；关闭真实Resend/SMTP/支付证书等变量。模拟支付以既有测试设置WXPAY_MOCK=1、NODE_ENV=test，不能改生产env。
- 浏览器仅连127.0.0.1/localhost的隔离静态站，所有/api均受控拦截；未知API与外域请求失败。隔离后端测试使用httptest/mock，不请求真实微信、退款或生成上游。
- T01适配verification/release_gate.py中Linux工具路径与编码器检测，保留禁止skip与配置缺失失败规则。不能仅删前置检查让脚本变绿。

## 实施后的验证命令

以下为未来执行入口，前提是上述隔离环境与工具已核实；现在没有运行这些业务检查。

```bash
# 在项目根，T01完成跨环境适配后：
python3 verification/release_gate.py

# 前端模块变更的常规全量检查（frontend目录）：
npm test
npm run typecheck
npm run build

# 后端由安全启动器注入隔离TEST_DATABASE_URL，不在命令里放密码：
# 在backend目录：
go test -json ./... -count=1 -timeout=180s
go test -race -json ./... -count=1 -timeout=180s
go build ./cmd/server

# 以下脚本在T01创建；当前文件不存在，不能声称已执行：
node verification/ui/run.mjs --suite all --base http://127.0.0.1:4193
node verification/ui/measure.mjs --base http://127.0.0.1:4193 --label before --runs 5
node verification/ui/measure.mjs --base http://127.0.0.1:4193 --label after --runs 5
```

浏览器工具使用现有隔离Playwright运行时；位置通过环境配置，不把某台Mac的绝对路径写进仓库。viewport=375×812、768×1024、1440×900，deviceScaleFactor=1；固定Chromium版本/操作系统/网络模拟参数。性能采用生产构建、同一mock响应延迟和关闭缓存的独立上下文。5次原始样本按路由分别保存，不混合三档尺寸。

LCP比较登录、项目首页、项目详情、充值页；有权限的管理/推广首屏各取一页。固定操作为“打开新建面板→输入→展开设置”和“打开已入账订单→展示原run状态”；从动作触发至目标内容渲染测量，不称为真实用户INP。初始JS取路由所加载文件的gzip字节和，包含共享chunk且不重复计数。媒体请求另列。基线没有可用LCP时记录原因，不能用0通过阈值。

## 证据结构

`.test/ui-unification/{before,after}/` 保存截图、网络日志、性能原始样本和构建指纹。`coverage.json`每项记录AC编号、命令、结果、证据路径、提交、环境、限制。服务器日志或源码中出现密钥一律脱敏，截图只用合成身份。

独立复审必须覆盖请求重放/晚响应、重复与乱序通知、待结算/未知提交、账号停用/切换、退款和价格基线。数据库集成skip数量必须0；无法跑PG10或真机软键盘时单列，不用模拟冒充。

## 发布准备

当前不部署。计划通过后再准备备份、制品校验和、当前/上一个release路径、服务健康与只读冒烟、回滚命令。新后端兼容旧前端，先后端后前端；回滚先前端后后端，无数据库回退步骤，不变更金融记录。
