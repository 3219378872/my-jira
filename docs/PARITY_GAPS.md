# 功能差距处理与交付验收边界

核对基准：`FEATURE_PARITY.md` 的 F01–F29，Plane 固定提交
`1fec307f91003df96351557af32ce87891a3678a`。核对对象是 2026-09-09 当前
my-jira 本地交付工作树。本文件记录实现差距的处理结果与实际验收范围，
不以路由存在、编译通过或单张截图代替功能证据。

G01–G14已修复，当前没有登记中的已知待修实现缺口。最新4180容器合集15项exit0，
13 passed（3.2m）、2个预期skip；被跳过的干净安装与队列重启场景在独立
`make verify-installation` 中另行通过。外部提供商、原版视觉对照与故障注入的
证据边界仍在本文件中明确保留。

## 本轮已补齐的实现差距

G01–G11 均已落入当前源码，不再登记为缺少实现。下面区分实际实现与本轮已取得的
验证证据；未覆盖的浏览器场景或外部提供商不能因这一状态而视为通过。

| ID | 范围 | 当前实现 | 已取得证据与边界 |
| --- | --- | --- | --- |
| G01 | F13/F14/F21 | `planning/progress.go`、迁移0007和 `planning-progress.tsx` 提供负责人/标签数量与估点分布、燃尽、转移前冻结和实时切换。 | PG 验证隔离、失败回滚、重复转移和当前 guest 设置；E03 浏览器验证来源3项冻结、已完成1项保留、未完成2项转入目标及进度界面。 |
| G02 | F21/F24 | support 分析与报表、`analytics-explorer.tsx` 接入日期、维度、分段、指标、保存分析和导出。 | PG/Minio 验证完整过滤、按调用者运行、报表内容、重复任务及撤权下载；`profile-analytics-ui-check.mjs` 已实际通过维度/分段选择、图表与数据表、保存/修改分析及重载还原。 |
| G03 | F18/F24 | support profiles 与 `features/profile.tsx` 提供可导航档案、创建/分配/订阅统计、活动分页和日期 CSV。 | PG 验证当前调用者范围、成员失效、CSV 公式防护及 guest 设置；真实浏览器已通过资料/时区保存重载、档案活动与负责/创建/订阅页签、项目筛选和活动 CSV 内容验证。 |
| G04 | F14/F16 | 模块链接、页面摘要/目录/链接/资产浏览及个人页面宽度偏好已接入。 | PG 验证资源和权限；E03 浏览器验证模块链接创建/修改/刷新、模块日期编辑；页面各读取入口另有撤权回归。 |
| G05 | F17 | `editor-commands.tsx`、`editor-block-controls.tsx` 和共享 schema 实现斜杠菜单、表情、工作项引用、普通块拖拽；引用按当前权限读取。 | 独立浏览器编辑器检查含四项操作、原生块拖动与重载；Go sanitizer 保留三个严格 UUID 属性，JSON grammar 与受信协作 binary 写入另有回归。两会话全链路见 E04。 |
| G06 | F18 | `home-customization.tsx`、`sidebar-projects.tsx` 和 workspace/layout 提供组件显隐排序、快捷链接、侧栏置顶和收藏分组偏好。 | 独立UI检查已通过组件显隐/DOM顺序、快捷链接增改删、项目置顶/顺序、收藏分组/排序/重命名/删除并保留收藏和重载还原；新增收藏同步并发回归已修。最终4180容器合集内 `preferences.spec.ts` 7.4s通过。 |
| G07 | F14 | `ModuleOverviewTimeline` 提供模块日期甘特、缩放、未排期展示和日期编辑入口。 | E03 浏览器验证模块甘特显示、表单修改日期持久化；不是工作项甘特的替代截图。 |
| G08 | F19 | support 与 workspace Inbox 接入原因/read/project筛选、limit/offset分页和同范围批量已读。 | PG 验证分页计数、过滤批量操作和访问变化；E02已通过四个浏览器session的通知可见范围、原因筛选和未读计数随guest设置/撤权变化。 |
| G09 | F25 | rotate-secret、项目/周期/模块/关联/评论通用事件和事务 outbox 已实现。 | 最新 PG 回归包含9次真实本地 HTTP Webhook 投递、签名/密钥重置/权限/删除事件；完整 Redis 队列链路与外部接收方另记 E07。 |
| G10 | F24 | 导出复用工作项 `CompileFilters` 完整语法，JSON number 保持数值类型；默认过滤生命周期一致。 | 最新 PG/对象存储回归验证多值、复合条件、日期及导出结果；CSV/XLSX/JSON 的任务及下载证据分别记录。 |
| G11 | F23 | 工作项精简字段与可选展开已接入 API，并先完成授权再投影。 | 后端字段校验、guest 范围和投影回归通过；不能从此推导每个外部 API 操作已完成真实客户端验收。 |

上述记录须在实现变更后重新读代码并删减，不能继续沿用已修复的差距。新的外部
提供商凭证、线上发布或产品范围选择不因本文件自动获得额外授权。

本轮还补齐了真实项目设置 `guest_can_view_all`（迁移0008，默认 false）、管理员
不能替页面所有者更改隐私、可管理的工作项订阅者与编号查询，以及服务端分组看板
跨组拖拽、日历改期、甘特移动/两侧缩放/四档时间范围。周期/模块选择已有工作项的
入口已补搜索和分页，普通项目列表展示偏好已持久化并由G13验证重载保留。
规划测试使用3项，不构成超过100项“选择已有工作项”弹窗的专项浏览器证据。F27另补
workspace/project 批量实体资产读取，统一复用当前工作项和页面权限，任一实体
不可读则整批拒绝；真实 PG/Minio 与 race 检查已通过。

## 本轮追加修复的交互缺口

以下三项由本轮原创源码只读复核定位，现已实现并取得PG、独立浏览器及最终4180
容器合集验证。它们与此前完成的列表/分组读取、排序API及详情跨项目移动分别登记。

| ID | 范围 | 修复结果与证据 |
| --- | --- | --- |
| G12 | F10/F15 跨项目看板 | workspace返回的项目分组可直接drop，并以一次POST原子提交目标项目与priority/label/state等分组属性；状态按名称/阶段映射，creator跨组与Guest目标禁投。PG已复现并修复等待锁期间目标会员降级/撤销的旧scope竞态。完整workitems包23.428s、独立浏览器及最新4180原子move/权限用例26.2s均通过；403模拟与真实409分开记录。 |
| G13 | F11 分组控件 | 表格/日历/时间线仅显示适用控件；分组/子分组值仍保留，切回列表/看板及保存视图重载后还原。个人与保存视图独立浏览器及最新4180布局控件用例7.0s通过。 |
| G14 | F10 分组顺序 | 服务端主组/子组/空组按业务顺序排列后分页；新增3项真实PG回归13.287s通过。浏览器验证priority顺序urgent/high/medium/low/none及project→state空子组按每个项目独立补齐。 |

全部10个可见分组字段均对应实际服务端分组实现；普通排序菜单的position、最新创建、
最近更新、目标日期、priority以及保存视图额外的name均对应真实排序，不属于空菜单。
排序菜单控制组内工作项，分组桶另由服务端应用G14的业务顺序。创建人不可修改，
跨创建人分组禁止drop，同组手动排序仍可用；这是不可变作者的边界。
`show_empty` 不枚举workspace所有空project/label目录；跨项目看板只对后端返回的
project组提供drop，详情move仍覆盖其他有权限的目标项目。

## 29 个功能组的核对落点

“未列新缺口”仅表示本次静态核对没有另外定位缺失行为，不代表该组通过最终验收。
下表用于保证清点覆盖，不用路由数量计算完成率。

| 功能组 | 当前原创实现落点 | 验证证据与边界 |
| --- | --- | --- |
| F01 实例初始化 | foundation/admin/auth；主站 setup 与管理员入口 | E01最新干净安装完整流程case13.1s/runner14.1s通过；管理站另见E05 |
| F02 认证 | foundation/auth、account_flows、oauth、email；auth/recovery 页面 | 未列新实现缺口；E01/E06 |
| F03 用户 | 用户资料、邮件变更、会话/关联账号/token；settings | 资料/时区、浅深色/语言及通知偏好保存重载UI已通过；其他账户流程见E01/E06 |
| F04 Workspace | foundation/workspaces/navigation；创建、邀请、成员与设置 | E01真实邮件邀请URL流程和E02角色/撤权均已通过 |
| F05 成员与权限 | platform/identity、guest_can_view_all，各领域 scope 与复查；真实 PG 角色/撤权用例 | 新增开关与owner例外PG回归、E02四session真实UI降级/开关与成员撤销组合均已通过 |
| F06 项目 | foundation/projects/project_metadata/catalog；项目及 settings | 未列新实现缺口；E01/E03 |
| F07 工作项 | workitems/mutations/routes；issue-list/form/detail | 未列新实现缺口；E01/E03 |
| F08 工作项关系 | workitems/interactions；父子、关系、链接及 Intake duplicate | 未列新实现缺口；E02/E03 |
| F09 工作项协作 | comments/reactions/managed subscribers/lookup/versions/activities/files；详情面板 | 新增F09 API与guest读取设置PG通过；E02/E04 |
| F10 列表/看板 | 服务端 groups/filters；普通与服务端分组看板 | E03分页/子分组/复合筛选/priority拖动与回滚、G12跨项目原子move与G14语义桶顺序独立浏览器均通过 |
| F11 表格/日历/甘特 | issue-list 布局、原生日期拖拽/resize/zoom、显示偏好 | E03日期保存/409/拖动/403回滚、E02 Guest边界及G13适用控件显隐/配置保留均有浏览器通过证据 |
| F12 生命周期/Intake | workitems/intake/maintenance；后台定时与项目 intake 页 | 实现与领域检查见VALIDATION；Intake浏览器场景分开登记 |
| F13 Cycles | planning/service/items/dates/progress；规划详情 | E03 已验证增改删、关联移除、转移与冻结；其余日期边界有PG回归 |
| F14 Modules | planning/service/items/links/progress；规划列表和甘特 | E03 已验证增改删、关联移除、链接和甘特日期；大集合选择器另验 |
| F15 保存视图 | planning/views；workspace/project view routes | E03项目视图增改删/分组/刷新，G12跨项目原子拖动与G13保存配置重载浏览器均通过；私有范围另记 |
| F16 Pages | documents/pages/versions_comments/resources；pages 页面 | E02当前页面范围与owner撤权、E04 REST替换/重连/私有/成员撤销已通过；其余组合单列 |
| F17 编辑器/协作 | Tiptap、共享 schema、live Hocuspocus/Yjs；正文一致性回归 | E04旧文档卸载竞态已修；真实生命周期回归、针对性复测及最终4180访问15.7s/协作18.3s均通过 |
| F18 个人/首页 | support profiles/preferences/favorites/recents/stickies；workspace/layout | G03档案与G06首页/侧栏偏好实际UI已通过；E02当前统计范围通过 |
| F19 通知 | integrations producer、support 通知状态、foundation email worker | G08；E02/E06 |
| F20 搜索/导航 | support/search；layout 命令面板、实体链接、404与权限页 | E01/E03 |
| F21 分析 | support/analytics、saved analyses、profiles；planning progress | G01/E03规划进度、G02自定义分析保存编辑重载UI、G03档案UI及E02统计范围均有实跑证据 |
| F22 Sites | integrations/public；public 页面及独立公开会话行为 | E05最新公开站登录/阅读/互动开关/停用用例4.3s通过 |
| F23 开放 API | token 鉴权、OpenAPI 目录、各领域路由 | G11；E02/E07 |
| F24 数据导出 | integrations CSV/XLSX/JSON、support reports/activity CSV、live PDF/convert | G02/G03/G10；E04/E07 |
| F25 外部/Webhook | foundation services/oauth；integrations ai/images/webhook | G09；E07隔离worker/Redis重启通过；容器真实AI生成200，其他提供商状态见E06 |
| F26 管理界面 | foundation admin/services；admin 页面 | E05/E06 |
| F27 文件生命周期 | files 上传/关联/确认/复制/恢复/清理及batch实体资产；Minio 测试 | E02附件协作/读取/撤权通过；batch新增PG/Minio与race通过（files 5.081s、openapi 2.094s）；完整队列与恢复见E07/E09 |
| F28 视觉可用性 | 独立浅深色 tokens、layout/UI与原创新页面 | E08共14张最终浅深色图已刷新并复核风格；没有固定原版同fixture像素对照 |
| F29 运维 | Compose、迁移、worker/live、backup 与 verify-backup 脚本 | E01/E07隔离安装与worker/Redis重启、E09数据库/对象/恢复实例HTTP鉴权读取已通过；测试栈/volumes已清理 |

## 交付验收结果与证据边界

这里列的是可执行场景与记录要求。缺少某项证据不等于对应实现不存在；已存在的
单元、真实 PG、Minio、SMTP、协议测试及浏览器证据应保留并正确归类。

| ID | 当前结果与待补证据 |
| --- | --- |
| E01 | 最新 `make verify-installation` 完整exit0，干净栈 `first-run` 1项通过（case13.1s、runner14.1s），含初始化、创建workspace/project/工作项、SMTP及从真实邮件读取邀请URL的完整流程。独立安装与重启验证结束后，bootstrap的9容器、3volumes、1network均清理无残留。4180普通合集中的安装用例按设计跳过，由这次独立结果补证。 |
| E02 | `access-scope.spec.ts` 最终4180运行44.0s通过：四session、真实UI降级/guest开关、全部被测读取入口及Member/Guest逐层撤权；Guest隐藏创建/禁批量、本人字段可用、他人列表/表格/看板/日历/甘特/详情控件禁用与只读，owner日期真实保存；3份CSV实际经outbox/Redis/worker/Minio并验证撤权下载。 |
| E03 | 最终4180 `planning.spec.ts` 三项12.4s/22.6s/6.8s通过，含115项分页/子分组、复合筛选、表格日期/真实409、规划CRUD/转移冻结和拖拽回滚；`grouping.spec.ts` 两项26.2s/7.0s通过，含G12原子跨项目move/权限、G13配置保留、G14语义顺序。403回滚明确使用模拟响应并真实GET核对未改，真实撤权另见E02及move PG。 |
| E04 | 删除旧协作文档50ms重复卸载，并在beforeUnload校验文档实例身份；真实Hocuspocus生命周期新回归、针对性两spec复测和最终4180合集均通过。最终access15.7s、product协作18.3s，覆盖历史恢复→继续编辑→锁定/PDF、REST替换/重连/私有/撤权。此前历史恢复后锁定失败已修复并通过最终复测。 |
| E05 | 最终4180 `queue-public.spec.ts` 公开站用例4.3s通过，含匿名阅读、独立公开登录、评论/投票/表情/Intake开关及停用；管理站/设置与响应式主题路由的product用例12.3s通过。提供商配置的真实服务状态按E06分别登记，路由渲染不等同每项外部提供商成功。 |
| E06 | 容器真实AI请求已返回200，model `gpt-5.6-terra`，正文 `MYJIRA_CONTAINER_OK`；证明该配置下真实提供商请求与生成可用，不推断写作质量或其他模型。SMTP有本地真实投递证据。Google/GitHub/GitLab/Gitea OAuth及Unsplash等仍按各自真实凭证/模拟结果登记，未配置的提供商不标通过。 |
| E07 | 最新独立 `make verify-installation` 内 `queue-restart` 1项通过（case15.1s、runner15.9s），包含bootstrap worker/Redis正常重启及清理；4180队列/API用例6.2s、E02真实通知与3份CSV/Minio、SMTP均通过。该重启用例没有验证任务执行中强杀worker的故障情形；领域回归、完整本地队列与外部Webhook接收方证据分别登记。 |
| E08 | `.local/evidence/browser-final-{list,board,detail,editor,settings,admin,public}-{light,dark}.png` 共14图已按显式主题刷新，包括清理旧fixture及admin当前1项目/12工作项。独立人工复核认为中文可读、布局无明显重叠/裁切、主题一致，低饱和背景/细边框/紧凑导航与两张reference渲染图风格相近。未运行固定原版同fixture，不能作为像素/页面等价证据；静态图不证明完整键盘焦点遍历与异常状态。 |
| E09 | `make verify-backup SNAPSHOT=/home/dev/projects/my-jira/.local/backups/snapshot.ad3SUp9Q` 已通过：独立PostgreSQL恢复/迁移、5个对象SHA256/MIME/metadata验证、恢复实例鉴权HTTP下载新fixture附件，匿名401；历史已删除工作项的3个附件仍按当前权限不可读。测试数据库/bucket已清理，新fixture工作项软删除后附件404，原12项seed未改。部署密钥须与备份安全分开保存；验证范围为隔离本地恢复。 |

`VALIDATION.md` 与 `FEATURE_PARITY.md` 由集成工作统一更新当前执行事实；本表保留
已发现差距的处理记录，不将未跑场景或未配置提供商直接替换为“完成”。
基准 B01–B09 升级占位/未接通功能仍是排除项，不列为实现差距。

最终4180容器合集15项exit0，13 passed、3.2m，另2个预期skip为first-run与
queue-restart；它们由最新独立干净安装验证提供完整通过证据。最终分项时长为：

| 用例 | 最终4180结果 |
| --- | --- |
| access-scope | 44.0s通过 |
| collaboration-access | 15.7s通过 |
| grouping | 26.2s、7.0s，2项通过 |
| planning | 12.4s、22.6s、6.8s，3项通过 |
| preferences | 7.4s通过 |
| product | 4.6s、18.3s、12.3s，3项通过 |
| queue-public | 6.2s、4.3s，2项通过 |

此前合集的历史恢复后锁定失败已修复并通过上述最终合集。以下较早独立结果保留为
问题定位及专项验证记录，最终状态以上表和最新 `make verify-installation` 为准。

## 本轮已获得的局部证据

- `go test ./internal/documents ./internal/planning ./internal/support/... -count=1`
  在明确的 `myjira_test` 中创建并清理本测试拥有的独立 schema；新增分析/档案、
  模块链接、页面资源、权限变化和正文替换回归通过。
- 配置 `TEST_S3_ENDPOINT=127.0.0.1:29000` 后，同一测试集实际生成 CSV 到
  独立随机测试 bucket，核对内容与项目scope、重试完成事件，并验证撤权下载403。
- 领域专项执行 `go test ./internal/documents ./internal/planning ./internal/support/...
  ./internal/workitems -count=1`，documents 5.734s、planning 0.982s、support
  8.076s、support/data 0.015s、workitems 11.698s，全部通过。覆盖周期快照/分布/事件、通知原因分页和
  过滤批量已读、公开项目撤销成员后的文档/历史/资源/搜索/收藏/通知拒绝，以及
  `pg_blocking_pids` 观察到成员角色变更阻塞文档请求后按当前guest可见范围404的测试。
  新增 `guest_can_view_all` false→true→false 跨领域、owner-only隐私、管理订阅和编号查询回归。
- 普通周期 DTO 与收藏解析已排除原始进度快照，避免来宾通过辅助入口读取全项目
  历史项；冻结汇总依然按当前调用者角色筛选。
- 浏览器执行 `pnpm exec playwright test tests/e2e/planning.spec.ts --reporter=list
  --output=.local/evidence/e03-playwright`：3项通过，共42.0s（12.1s、23.9s、5.1s）。
  使用1440×960浏览器、真实demo登录/session/CSRF和仅本测试拥有的项目；115项fixture与
  3项规划fixture分开。正常结束删除本测试项目；两次测试定位期间超时遗留项目也按准确
  ID和名称核对后通过API软删除。没有删除原有演示项目。
- `.local/evidence/browser-e03-*.png` 九张证据覆盖分页看板、高级筛选、表格日期、周期快照、
  模块甘特、保存视图、分组移动、日历改期和甘特拖拽。它们证明原创界面及所述行为，
  不构成与固定提交原版的成对视觉验收。
- 较早独立浏览器执行 `pnpm exec playwright test tests/e2e/access-scope.spec.ts --reporter=list
  --output=.local/evidence/e02-playwright`：1项通过，共36.4s（测试本体35.4s）。
  四个独立浏览器上下文使用1440×960视口、真实session/CSRF和独立workspace/private/public
  项目；没有route模拟。真实UI降级/开关与逐层撤销后，再经对应浏览器凭据读取所有被测
  资源；Guest为可读他人工作项上传附件属于允许的协作，字段编辑仍拒绝。
  3份CSV分别包含private、public和整个workspace的当前工作项，实际经队列与对象存储
  完成，下载内容和权限重查通过。`.local/evidence/browser-e02-member-role.png`、
  `browser-e02-guest-owned.png`、`browser-e02-guest-all.png`、
  `browser-e02-private-membership-revoked.png` 记录真实UI。测试项目/workspace软删除，
  临时账户登出；没有账户删除API，注册账户仍留存，不改demo成员身份或偏好。
- E02随后补充Guest可见控制的UI断言，并在4180容器合集40.4s通过：隐藏创建入口及
  快捷键创建，批量checkbox禁用；所有者列表状态/优先级和表格日期可编辑且日期实际
  保存；他人字段禁用，普通/分组看板与日历/甘特draggable及resize按owner区分；详情
  他人标题/正文/属性只读，owner可编辑。新增截图
  `.local/evidence/browser-e02-guest-ui-readonly.png` 和 `browser-e02-guest-ui-owner.png`。
- `tests/e2e/collaboration-access.spec.ts` 较早独立18.6s通过，结果位于
  `.local/evidence/live-access-playwright/`；此用例覆盖REST替换、断线重连、私有页面与
  项目成员撤销的当前协作访问边界，PDF/convert同样重新鉴权。4180最新同一用例
  在第一次合集15.4s通过；该轮历史恢复后锁定的另一用例曾失败。删除50ms重复卸载并
  增加beforeUnload文档实例身份校验后，真实Hocuspocus生命周期新回归通过；4180两spec
  针对性重跑2 passed、37.6s（access 16.6s、product 20.1s），历史恢复后的继续编辑、
  锁定和PDF均通过。
- `.local/profile-analytics-ui-check.mjs` 实际通过资料/时区保存重载、浅深色/语言切换
  重载、通知偏好保存、档案页签/活动导出以及自定义分析保存/修改/重载。
  `.local/evidence/browser-member-profile-light.png`、`browser-analytics-light.png`、
  `browser-preferences-dark.png` 为对应原创UI证据。
- `.local/home-sidebar-ui-check.mjs` 在1440×1000视口实际完成首页/侧栏偏好UI组合：
  组件显隐和DOM顺序、快捷链接增改删、项目置顶和顺序、收藏分组/排序/重命名/删除并
  保留收藏，逐项重载核对。新收藏与分组同步并发回归已修复并验证；截图为
  `.local/evidence/browser-home-sidebar-light.png`。上述两项独立UI检查清理自身fixture并
  恢复原偏好，不能由此推断其他用户/其他设备上的全部组合已测试。随后可复现的
  `tests/e2e/preferences.spec.ts` 在4180合集7.1s通过。
- F27批量实体资产已加入 `files/batch.go` 和OpenAPI；当前调用者逐个授权后才返回
  整批结果，workspace请求允许跨有权项目，project请求严格限定自己的实体。
  在 `apps/api` 中配置明确的 `TEST_DATABASE_URL`（`myjira_test`）及
  `TEST_S3_ENDPOINT=127.0.0.1:29000` 后，`go test -race -count=1 ./internal/files
  ./internal/openapi` 本轮通过，files 5.081s、openapi 2.094s；文件测试实际使用独立
  schema/bucket并清理，包含当前Guest设置、页面owner/显式PM、外部workspace和混合
  可见/不可见实体的拒绝。`go vet ./internal/files ./internal/openapi
  ./internal/application` 同轮无错误。
- E09成功快照为 `.local/backups/snapshot.ad3SUp9Q`。`make verify-backup
  SNAPSHOT=/home/dev/projects/my-jira/.local/backups/snapshot.ad3SUp9Q` 实际恢复后验证
  5个对象与应用HTTP附件读取；创建并清理测试数据库
  `myjira_restore_test_20260909100112_419250` 和bucket
  `myjira-restore-test-20260909100112-419250-6488`。新建fixture工作项
  `5ec87594-8516-4908-8136-afbaaa4b62e7` 随后软删除，其附件
  `fdafb81b-de61-4b63-aa04-4d33e04fde98` 返回404；恢复策略保留对象，不删除原12项seed。
- 较早 `make verify-installation` 完整返回0：隔离干净安装 `first-run` 1 passed（15.1s）、
  隔离 `queue-restart` 1 passed（13.3s），包括从SMTP邮件读取真实邀请URL和
  bootstrap worker/Redis重启；专属Compose栈及volumes已清理。
- 最终14张 `browser-final-*-{light,dark}.png` 已刷新；主站/管理站/公开站均有明确
  浅深色结果，admin图采用当前1项目/12工作项。detail/settings/admin/public八图
  与仅有的两个reference渲染图经独立只读复核，无明显文字缺失、重叠或裁切，详情
  抽屉焦点环可见；reference没有这些对应页面的同fixture运行，因此结论限于原创
  界面可读与相近风格，完整键盘焦点、hover及错误状态仍需各自行为证据。
- 真实容器AI调用返回HTTP200，model `gpt-5.6-terra` 与正文 `MYJIRA_CONTAINER_OK`。
  这是一次真实提供商连通/生成证据；外部OAuth/图片提供商与内容质量不能由此推断。
- G12后端move原子目标changes新旧PG回归1.806s通过，校验目标project下的state、
  assignee、label、cycle、module及estimate，非法目标与409保持原工作项/关联/目标序号
  不变，成功只创建一次moved事件。随后真实PG复现等待graph锁期间目标Member降级为
  Guest后旧scope仍允许200移动；修复在graphs/estimates等待结束后共享锁workspace、
  WM、user、projects、PM，并通过事务q重新验证源/目标Member。
  `TestMoveRechecksMembershipAfterWaitingForGraphLock` 两种目标降级/撤销均实际观察
  `pg_blocking_pids`，提交权限变化并释放graph锁后请求403/404，原item的version、
  priority、project不变；连同原子changes和既有move共3项定向回归2.582s通过。
  后续完整workitems包23.428s和vet通过，包含新增move并发回归。
- G14服务端主组/子组/空组统一业务排序后分页，3项新增真实PG回归13.287s通过。
  `make check`、web 10项和live 4项单元测试均通过；这是构建/单元与领域证据，另有
  下列真实浏览器验证。
- `CHROMIUM_PATH=/home/dev/.local/bin/chromium pnpm exec playwright test
  tests/e2e/grouping.spec.ts --output .local/evidence/grouping-playwright --reporter list`
  在4173前端与已更新API上2 passed、37.6s（28.2s跨项目/权限，8.2s布局控件），
  没有pageerrors；专属workspace测试后DELETE204。覆盖单次POST提交项目及priority/
  label/显式state、状态名称/阶段映射，模拟403后的完整源保留与真实409重载无部分
  移动，creator跨组禁止drop/同组排序、Guest目标禁止drop；priority DOM顺序正确，
  project→state空子组按项目分别补齐；个人及保存视图在表格/日历/时间线隐藏分组
  控件，切回与保存重载仍保留原设置。后续最终4180同两项分别26.2s/7.0s通过。

最新完整 `make verify-installation` 再次exit0：first-run case13.1s/runner14.1s、
queue-restart case15.1s/runner15.9s均通过；bootstrap的9容器、3volumes、1network
清理后无残留。主站4180最终合集与独立安装验证共同构成本次容器交付证据。

当前没有登记中的已知待修实现问题。保留的证据边界是：真实OAuth/Unsplash缺少
凭据；AI已验证一次真实生成但未评估内容质量；视觉参考不是固定原版同fixture
像素对照；队列重启覆盖正常重启，没有验证任务执行中强杀worker的情形。
