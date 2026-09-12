# 三阶段需求验证记录

日期：2026-09-12。实施前基准提交为`85d2ad7a2bbdc39b44061b499df7435bb6d99f87`。
本记录、实现、生成契约及截图一起纳入本次版本化交付。
范围为[37条故事](REQUIREMENTS_BASELINE.md)与[三阶段计划](ITERATION_PLAN.md)。
`VALIDATION.md`保留社区复刻历史证据；本文件记录新增实施和重新运行的检查。

## 证据规则

实现存在、静态类型/构建、纯函数测试、真实数据库、浏览器交互、视觉、真实模型、
GitHub/Actions、真实预测准确性与团队改善效果分开记录。测试跳过不能计为通过。
本轮全部数据库实验仅使用 `myjira_requirements_test` 的独立schema/演示数据。
参考Plane目录始终只读；现有应用业务库、无关容器和服务不在修改范围。

## 运行环境

- 专用Compose：`compose.requirements-test.yaml`，项目名`my-jira-requirements-test`。
- PostgreSQL：127.0.0.1:35432，数据库`myjira_requirements_test`。
- Redis：127.0.0.1:36379；MinIO：127.0.0.1:39000；Mailpit：127.0.0.1:38025。
- API：127.0.0.1:18088；Web：127.0.0.1:14173；协作服务：127.0.0.1:13101。
- 浏览器固定1440×960，locale zh-CN；浅深主题与两独立会话分别取证。
- 迁移通过独立migrate命令执行；API启动不变更schema。

## 本轮结果

37条故事的文档基线、四视图和六类分析/自动执行流程已经落到实现、迁移、API、
客户端和worker。以下是本轮实际检查结果；浏览器覆盖选定的完整业务路径，
不表示所有字段组合、拖拽方式、设备或外部服务均已验证。

| 层次 | 实际结果 | 范围 |
| --- | --- | --- |
| Go全量检查 | 178个顶层测试通过；含子测试245个通过，0失败、0跳过；19个有测试的包通过 | 真实隔离PostgreSQL、专用MinIO、纯算法、提供商模拟、HTTP和契约 |
| Go静态检查 | `go vet ./...`通过 | 最新业务、worker及生成代码 |
| Web测试 | 36个通过，0失败 | 需求语义、SSE缓存竞态、自动化展示与既有组件/状态 |
| 协作服务测试 | 4个通过，0失败、0跳过 | 文档持久化、恢复后旧实例清理、路径与CSRF |
| TypeScript与生产构建 | Web和live构建通过，包含类型检查 | 静态证据，不替代交互验收 |
| 新增浏览器流程 | 7个通过，1.1分钟 | 四视图4项、自动化3项；实际API、PG、Redis与worker |
| 原社区浏览器回归 | 13个不同用例均有通过证据 | 首轮11通过/2测试环境失败，修正后受影响3项全部通过；业务断言保留 |
| 旧数据迁移 | 真实0008→0015通过 | 字段/二进制/关联/未分类旧项保留、历史覆盖起点、触发器、回滚及校验和 |
| 对象存储与恢复 | 专用MinIO真实检查通过 | 附件、过滤导出、分析报告及备份对象/元数据恢复；拒绝覆盖既有目标 |
| worker停止/重启 | 已入库同一运行恢复完成，attempts=1；重放仍为同一运行与一条预测 | PostgreSQL outbox到Redis worker；未做本轮Redis重启或运行中强杀实验 |
| 真实模型 | 两次成功应用，一次结构校验阻止应用；共3次调用 | 固定合成PRD的初次拆解、幂等重放和增量/人工修改保护，详见下文 |

Go全量命令使用与`make requirements-integration`完全相同的显式测试配置：

```sh
make requirements-integration
cd apps/api && go vet ./...
```

最终全套执行加了`-json`保留检查事件并统计上述数量；最长包automation约96.8秒。
没有测试的生成代码包显示`[no test files]`，不计作通过用例。存储测试统一支持
`TEST_S3_ENDPOINT`、`TEST_S3_ACCESS_KEY`、`TEST_S3_SECRET_KEY`；备份测试另用
`S3_TEST_ENDPOINT`和标准S3凭据。专用Make目标已同时设置，避免悄悄跳过存储检查。

```sh
pnpm -r --if-present test
pnpm -r --if-present build
E2E_BASE_URL=http://127.0.0.1:14173 pnpm exec playwright test \
  tests/e2e/requirements.spec.ts tests/e2e/requirements-automation.spec.ts
```

本机Chromium缓存与Playwright请求版本不同，执行时另以`CHROMIUM_PATH`指定已安装
浏览器。仓库未硬编码本机缓存路径。

### 数据库、并发与权限

新增领域验证覆盖：显式Epic/Story/Task层级；旧工作项不自动分类；变更集的版本、
幂等、同批引用与整体回滚；完整快照及游标；SSE重放、过期、撤权与晚响应；
16小时按75/25守恒分配；叶任务、零/未知、工作日例外、技能与日历；确定性排期、
既有负载、锁定/开始/完成项、Story承诺与硬期限；场景步骤、分支、SVG转义及历史。

当前来源权限在读取、导出、排队、模型调用和自动应用时分别复查。测试包含等待锁
期间撤权或撤销会话/API令牌，以及Story移动、私有Page、历史场景/质量来源、
归档与关闭工作台后的读取/写入边界。撤销保护覆盖人工修改，以及后来新增的
子任务、场景、评论、附件和关联；冲突时保留整批。

自动化验证包括预算预占、策略实体/字段/动作限制、并发应用、取消/重试、冷启动
预测、时间切分回测、风险去重/解除/行动、改善基线与观察窗口、有限原因链。
最终检查修复并复现验证了两个问题：失效项目授权不再阻断后续项目调度；
单份质量报告的第二个动作不能绕过`max_changes=1`，失败不留下半批记录。

GitHub模拟和PG验证包含setup→OAuth的PKCE、一次性state、会话撤销、仓库管理员
授权、HMAC正文与换delivery-header重放、分页/限流/补偿、安装撤销、乱序、
固定SHA/run/attempt/digest和不同报告格式。旧两段state的code回调被拒绝，
不会调用GitHub、消耗有效state或创建grant；有效三段state流程仍通过。

迁移测试直接应用真实0001–0008、插入旧行，再调用正式迁移器升级到0015；
重复执行保留迁移账本与历史，校验和异常拒绝执行。随机schema完成后清理。
未测试生产数据量、迁移吞吐或生产恢复。已有非布尔`requirements_enabled`
设置会被0015约束拒绝，不能推断所有自定义旧配置都能直接升级。

### 浏览器与视觉

`requirements.spec.ts`实际验证：从UI创建Story并在第二会话看到更新；移动Story
承诺Sprint保留Task执行Cycle/日期；甘特修改日期；完成状态清除未来负载；
编辑结构化场景、导出安全SVG、收紧私有来源后另一会话失去访问；
项目设置关闭/恢复工作台时活动标签清空/恢复，基本工作项保留。

`requirements-automation.spec.ts`实际验证：策略表单/字段限制、PRD输入和无GitHub
配置提示；真实worker冷启动预测、排程应用、重试与过期409、成功及冲突撤销、
风险行动、改善数据不足；Member权限、策略停用及跨会话撤权。未在浏览器中
伪造真实GitHub报告或模型响应来代替外部证据。

固定REQ样例包含3Epic、5活动、3Sprint、18条基线Story及1条Backlog、子任务、
跨执行Sprint、多人分配、日历例外、PRD与场景。可重建样例命令为
`make requirements-seed`。截图固定1440×960、中文与浅深两种主题：

- `screenshots/requirements-story-map-{light,dark}.png`
- `screenshots/requirements-gantt-{light,dark}.png`
- `screenshots/requirements-members-{light,dark}.png`
- `screenshots/requirements-scenarios-{light,dark}.png`
- `screenshots/requirements-automation-runs-{light,dark}.png`

视觉检查曾发现截图把主题写在错误偏好路径，两个主题实际相同；已修正为
`preferences.appearance.theme`并增加实际DOM主题断言。修正后重新运行甘特业务路径
和固定主题截图，两项通过（19.5秒）；同时重建Web/live通过。最终10张PNG均为
1440×960，每对浅深截图的内容均不同，并实际复查浅色甘特和深色地图。
甘特日期标签已按周/月/季度缩放分别每1/2/7天展示，完整日期保留在可访问名称和
悬停提示，月档标签不再拥挤。截图证明所列视口和样例的视觉状态，不替代全部
布局或交互组合的验收。

社区回归执行access-scope、collaboration-access、grouping、planning、preferences、
product及queue-public七组。当前studio/ORBIT演示ID通过`E2E_WORKSPACE_ID`和
`E2E_PROJECT_ID`传入，Mailpit用`E2E_MAILPIT_URL=http://127.0.0.1:38025`。
最初两个失败分别是异步偏好尚未出现时解引用空映射，以及测试固定了另一个
Mailpit端口；修正测试环境适配后通过。first-run和旧queue-restart要求另一套
bootstrap Compose环境，本轮未重跑，不能继承历史结果为当前证据。

### 真实模型与恢复证据

保留现有指定模型`gpt-5.6-terra`和已配置Responses传输，没有换模型或提供商配置。
测试使用注释PRD的有界英文合成子集，3Epic/3Story/3Task和一项增量任务，
不代表完整中文PRD或一般输入准确性。结构采用提示词约束与服务端严格校验；
没有把它声称为已启用线上wire级Structured Outputs。

| 真实运行 | 结果与观察 |
| --- | --- |
| `64c8d528-42d9-4291-94ec-34647971aba7` | 创建并应用9实体，来源原文/行号、3段需求、区间和依赖通过；未决条件保留为partial |
| 同一请求重放 | 复用首个run ID，没有额外模型调用 |
| `36dba694-0685-4b96-80dd-346ac70c4515` | 父级选择不合法而blocked，0命令、无建项/删项；严格校验未放宽 |
| `474efbae-de01-4fae-963f-bf6dab7c18f7` | 明确提示词后增量应用8更新+1新Task；10实体、原9个ID不变、人工Story修改保留、2条依赖与来源区间通过；仍披露未决条件 |

[脱敏模型证据](REQUIREMENTS_MODEL_EVIDENCE.json)保留输入/源码hash、run和批次ID、
语义检查与被拒绝记录，整体状态`partial`；验证脚本因此返回2而非伪装全通过。
私有验收项目策略最终version4、disabled，月预算3已使用3；没有第四次调用。
原始模型文本未保留在此公开证据文件，因此不能确定第二次错误是哪一种具体
无效父级组合。源码已明确互斥字段、稳定父级key及禁止空/零UUID。

[worker恢复证据](REQUIREMENTS_WORKER_EVIDENCE.json)记录运行
`27c88d80-c69a-4feb-bfd5-ee00966c9bcd`在worker停止时`queued/attempts=0`及未分发
outbox，重启后`completed/attempts=1`，只有一条`insufficient_data`预测。
完全相同请求重放复用该运行；模型调用为0；测试策略最终关闭。首次实验因重新
seed导致输入修订变化而blocked的记录一并保留，不将安全拒绝算成恢复成功。

## 外部及效果验证

| 项目 | 状态 | 所需证据 |
| --- | --- | --- |
| 真实模型结构化拆解与增量应用 | 选定子集部分通过 | 初次与增量成功；一次无效父级被拒绝；其他输入/完整PRD未验证 |
| GitHub App / Actions真实链路 | 未验证 | 独立安装授权、固定SHA/run/attempt、真实产物及撤销 |
| 真实历史预测准确性 | 未验证 | 至少20条完成记录与4周历史；时间切分回测报告 |
| 真实团队改善效果 | 未验证 | 行动前基线、足够观察窗口、范围变化解释与后续指标 |
| 生产容量与高可用 | 未验证 | 独立容量、故障和恢复实验；本地通过不能替代 |

## 运行与恢复

先停用项目自动化再回退应用，保留运行输入、历史事实、变更批次及场景和质量版本。
数据库备份自动包含新增表；对象证据同时备份对象存储。恢复至独立库验证权限与
版本查询后再切换，不能把恢复演练指向业务库。保留测试数据卷便于复核；任何清理
必须精确限定本测试项目的卷，禁止复用或删除原应用卷。
