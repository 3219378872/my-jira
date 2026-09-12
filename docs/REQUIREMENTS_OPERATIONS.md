# 需求与自动化运行说明

范围见[37条故事基线](REQUIREMENTS_BASELINE.md)，实际结果见
[验证记录](REQUIREMENTS_VALIDATION.md)。以下命令仅使用本项目专用测试栈。

## 独立验收环境

```sh
make requirements-infra
make requirements-migrate
```

Compose项目名`my-jira-requirements-test`；数据库`myjira_requirements_test`。
API/浏览器使用`requirements_acceptance_v1` schema；Go集成用例另建随机schema。
`requirements-infra`只创建不存在的schema，保留已有验收数据。
在四个终端分别启动进程，然后执行样例脚本：

```sh
bash scripts/requirements-test-runtime.sh api
bash scripts/requirements-test-runtime.sh worker
bash scripts/requirements-test-runtime.sh live
bash scripts/requirements-test-runtime.sh web
```

```sh
make requirements-seed
```

打开`http://127.0.0.1:14173`，测试账号`demo@myjira.local`，测试密码
`MyJira-Local-2026!`。脚本中的公开测试密码、存储凭据与加密材料仅用于此专用栈。
样例脚本只接受localhost:18088，已有实例名必须为`My Jira Requirements Test`。
脚本生成三Epic、五活动、三Sprint、18条基础故事及Backlog，包含多人16小时分摊、
跨执行Sprint、未排期/技能缺口、日历例外、PRD、私有来源及两种场景。
脚本输出真实UUID，浏览器测试动态发现当前workspace/project。

专用端口为API18088、Web14173、live13101、PostgreSQL35432、Redis36379、
MinIO39000/39001、SMTP31025、Mailpit38025。Vite支持`VITE_API_PROXY_URL`和
`VITE_LIVE_PROXY_URL`，原开发环境默认端口保持兼容。

```sh
make requirements-integration
E2E_BASE_URL=http://127.0.0.1:14173 pnpm exec playwright test tests/e2e/requirements.spec.ts tests/e2e/requirements-automation.spec.ts
```

若浏览器缓存与Playwright版本不同，可设置`CHROMIUM_PATH`指向本机Chromium。
截图固定1440×960、中文、浅深主题；两个会话使用独立浏览器context。
原社区E2E可通过`E2E_WORKSPACE_ID`和`E2E_PROJECT_ID`选择当前样例UUID。
本测试栈的邮件回归另设`E2E_MAILPIT_URL=http://127.0.0.1:38025`。

有界真实模型探针为`node scripts/verify-requirements-model.mjs --run`，它只接受
专用实例、复用自己的证据项目及月预算，结束后关闭策略；已完成证据不会发起
第四次调用。整体结果含安全拒绝时返回2，详情见脱敏JSON，不能将退出码改写为
全通过。缺少模型配置时先完成其余本地验证。

worker恢复探针分两步：停止上述专用worker后执行
`node scripts/verify-requirements-worker.mjs --prepare`，保持项目输入稳定，
重启worker后执行`node scripts/verify-requirements-worker.mjs --verify`。
探针不控制服务，仅创建自己私有项目中的零模型预算forecast，保留运行和重放证据，
完成后关闭自己的策略。测试其间修改样例或成员会使旧快照失效并被正确阻止。

## 配置与业务边界

项目设置`settings.requirements_enabled`控制四视图，缺失/null默认开启。
此开关与AI策略`enabled`分开。关闭能力或归档项目会拒绝相关新写入，并通过事件
通知已打开客户端清除缓存；基础工作项保留。重新开启时客户端重新获取授权快照。

工时以整数分钟保存、小时展示；只对叶任务计算负载，不重复计算父级。
多人分摊按稳定顺序守恒分配；容量取成员可用量与项目额度的较小值。
未知和明确的零分别展示。Story的Cycle/日期表示承诺，Task表示执行安排；
Story移动默认保留Task日期和执行Cycle。依赖不生成UML业务交互。

自动化默认为关闭。项目管理员配置允许的能力、实体、字段、操作、来源、人员及
工作项范围，单批上限、模型月预算、频率、原因链轮次与硬截止日。
开启后符合策略的业务命令自动应用；模型结果仍需严格结构、来源、权限和资源校验。
私有PRD不能自动生成对全项目可见的工作项。无解、过期、受阻不能显示为成功应用。

持续worker按分钟合并稳定修订，关联拆解、排期、风险和改善分析。
少于20个完成项或不足4周历史时预测保持`insufficient_data`；不可访问的历史
不能通过幸存样本制造高置信度。改善行动的执行时间与观察窗口固定后才评估效果。

## 独立GitHub App

通过环境提供`GITHUB_APP_ID`、`GITHUB_APP_SLUG`、`GITHUB_APP_PRIVATE_KEY`、
`GITHUB_APP_CLIENT_ID`、`GITHUB_APP_CLIENT_SECRET`、`GITHUB_WEBHOOK_SECRET`。
这些凭据不进入浏览器、源码和证据文件；App与平台登录OAuth独立。
授予Contents、Pull requests、Actions、Checks和必要Metadata读取权限。

Webhook URL为`APP_URL/api/v1/github/webhook`。Setup URL与OAuth Callback URL
均为`APP_URL/api/v1/github/callback`，关闭GitHub“Request user authorization during
installation”；平台在setup callback后显式发起OAuth，使用会话绑定的一次性state
和PKCE，验证仓库管理员权。详见[质量模块说明](../apps/api/internal/quality/README.md)。

报告固定repository、SHA、PR、Actions run/attempt、产物digest和文档版本。
平台读取源码、差异与SARIF/JUnit/Cobertura/LCOV等已有报告，不运行仓库代码。
失败、跳过、缺失与未知分开；文档检查覆盖缺失叙述/验收及显式矛盾，缺少可靠
实现映射时一般语义一致性保持未知。签名按原始正文验证，更换delivery header
也不能重放同一签名正文。安装撤销后停止读取与自动动作。

worker每15分钟投递`quality.reconcile`，分页补查Actions、PR、commit和本地文档版本，
补偿遗漏事件。接入未配置时界面明确显示；模拟provider通过不代表真实GitHub通过。

## 迁移、回退与恢复

版本迁移0009–0015覆盖需求/事件/长期历史、资源、场景、自动化、质量及归档/能力
权限事件。0009给旧工作项写入`coverage_started`基线，不虚构迁移前状态。
迁移由独立migrate执行，API与worker启动不自动修改schema。
新增自定义触发器/分析表采用版本SQL，Ent工作项字段按原生成流程生成并保留标记。
真实0008升级检查保留旧行、关联及未分类语义；缺失/null开关兼容。
0015拒绝既有非布尔`requirements_enabled`值，升级前应识别并按实际业务意图处理
这种自定义设置冲突，迁移不猜测或静默改写原值。

回退前关闭项目自动化、停止worker新增消费，保留来源快照、映射、运行记录、审计、
变更批次、场景和质量版本。Redis与数据库保留后重启继续消费，重复任务由持久状态
与幂等键去重。撤销检查后续人工修改、子任务、场景、评论、附件与关联历史，冲突
时整批保留；不能先撤销一部分再报告失败。

原有`make backup`覆盖新增数据库表与对象存储；用独立测试数据库和bucket执行
`verify-backup`恢复演练，并验证当前权限下的历史/导出读取。加密密钥需要独立
安全备份。保留本轮测试卷以便复核；不要用清理测试栈的命令删除原应用卷。

## 运行观察

界面显示持久run状态、尝试数、失败与撤销冲突、预算用量、来源版本、质量同步
状态与SSE连接状态。结合Asynq队列检查以下数据库记录：

```sql
SELECT kind,status,count(*),min(created_at) FROM automation_runs GROUP BY kind,status;
SELECT topic,count(*),min(created_at) FROM outbox_events
 WHERE dispatched_at IS NULL AND deleted_at IS NULL GROUP BY topic;
SELECT sync_status,count(*),min(next_retry_at) FROM github_bindings GROUP BY sync_status;
SELECT project_id,revision,updated_at FROM project_revisions ORDER BY updated_at DESC LIMIT 50;
```

固定源版本、输入指纹、事件游标和命令批次用于定位失败并重试。
本地结构/单元/PG/浏览器证据不能替代真实模型、GitHub、历史预测准确性、团队改善、
生产容量或高可用验证；所有结果在独立验证记录分开登记。
