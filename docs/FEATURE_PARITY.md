# 功能覆盖与复刻状态

本矩阵 F01–F29 记录原 Plane 社区范围。2026-09-12 新增的三阶段需求独立
追踪于[37 条故事基线](REQUIREMENTS_BASELINE.md)、[12 个 Sprint 计划](ITERATION_PLAN.md)
与[新增验收记录](REQUIREMENTS_VALIDATION.md)。旧矩阵的“已实现”不表示新增范围已验收。

基准目录：`/home/dev/found/plane`。基准提交：
`1fec307f91003df96351557af32ce87891a3678a`。清点日期：2026-09-09；参考工作树干净。

本文件是独立编写的行为范围说明，参考代码仅用于只读核对，不复制其实现、文案、
样式或资源。来源路径均相对于基准目录。原版未在本次环境启动；参考渲染图只用于
视觉观察，不进入原创应用。当前原创实现已有真实服务和浏览器证据，具体命令、
结果及未验证范围见 `VALIDATION.md`。

## 状态口径

- **基准实现**：已发现注册路由及对应处理逻辑，或接入页面的交互实现；属于复刻范围。
- **基准 API**：服务端有处理逻辑，当前主站未提供完整入口；仍需纳入服务能力清单。
- **基准占位**：升级展示、关闭的开关、未注册页面或遗留模型；不能算作完整可用功能。
- **待核对**：仅存在字段、类型或间接引用，尚不足以确定完整用户行为。

my-jira 的实现、自动验证、浏览器验证、视觉验证分别登记。下表已更新为当前工作树
状态：**已实现**表示对应原创行为已落地；后续文字说明实际取得的验证，不代表该
组所有组合、真实外部服务或生产容量都经过测试。`PARITY_GAPS.md` 保留具体剩余
工作。用户已确认不需要任何 Plane API 或数据兼容；新 API 独立设计。

## 功能矩阵

| ID | 功能组 | 基准真实能力与必须覆盖的行为 | 主要证据路径 | my-jira |
| --- | --- | --- | --- | --- |
| F01 | 实例初始化 | 实例就绪状态、首次管理员注册、管理员登录退出、实例管理员增删改查、初始化提示、配置开关 | `apps/api/plane/license/urls.py`；`apps/admin/app/routes.ts` | 已实现；身份/实例 PG；最新镜像空实例初始化浏览器通过 |
| F02 | 认证 | 邮箱密码登录注册；邮箱校验；邮件验证码登录注册；设置、修改、找回和重置密码；会话与退出；CSRF；主站和公开站的认证入口 | `apps/api/plane/authentication/urls.py`；`apps/api/plane/app/urls/user.py` | 已实现；账号/会话/CSRF PG；登录浏览器；OAuth 真实提供商未配置 |
| F03 | 用户 | 资料、头像、邮箱变更校验、关联账户、引导状态；安全设置、主题和偏好、通知偏好、API token 管理 | `apps/api/plane/app/urls/user.py`；`packages/constants/src/settings/profile.ts` | 已实现；PG；资料/通知/主题偏好与 token 浏览器 |
| F04 | Workspace | 创建、slug 可用性、详情、编辑删除、切换和最后访问记录；邀请、接收邀请、成员列表、退出；成员个人设置 | `apps/api/plane/app/urls/workspace.py`；`apps/api/plane/db/models/workspace.py` | 已实现；workspace/邀请 PG；创建、成员与真实邮件邀请注册接受浏览器通过 |
| F05 | 成员与权限 | workspace/project 两层 Admin、Member、Guest；活跃成员约束；项目可见性、加入邀请、角色变更、移除和离开；来宾可见范围；文档私有、所有者、锁定和归档约束 | `apps/api/plane/app/permissions/`；`apps/api/plane/app/views/issue/base.py`；`apps/api/plane/db/models/project.py` | 已实现；跨领域 PG；四用户真实会话与角色变化浏览器 |
| F06 | 项目 | 项目列表与详情、创建编辑删除、标识校验、封面与图标、负责人/成员、收藏、归档恢复；成员偏好；Cycles、Modules、Views、Pages、Intake 功能开关 | `apps/api/plane/app/urls/project.py`；`apps/web/app/routes/core.ts` | 已实现；状态/标签/项目 PG；项目/成员/开关浏览器 |
| F07 | 工作项 | 创建、详情、修改删除、项目内编号；标题和富文本描述、状态、优先级、多个负责人、标签、开始/截止日期、估点；快捷创建、完整页面与预览面板 | `apps/api/plane/app/urls/issue.py`；`apps/api/plane/db/models/issue.py`；`apps/web/core/components/issues/` | 已实现；事务/并发 PG；创建/编辑/刷新浏览器 |
| F08 | 工作项关系 | 父子工作项、子项列表、关系添加移除、外部链接；Intake 重复项指向。甘特依赖可视化与一般工作项关系分开核对 | `apps/api/plane/app/urls/issue.py`；`apps/api/plane/db/models/intake.py`；`apps/web/core/components/issues/issue-detail-widgets/` | 已实现；关系/父子/移动/回滚 PG；规划关联浏览器 |
| F09 | 工作项协作 | 评论增删改查、提及、评论/工作项表情反应、活动历史、订阅者与订阅切换、附件、描述版本列表和详情、实体元信息与编号深链接 | `apps/api/plane/app/urls/issue.py`；`apps/api/plane/bgtasks/issue_activities_task.py` | 已实现；PG/Minio；评论/附件/持久化浏览器；编号/订阅 PG |
| F10 | 列表与看板 | 行内修改、快捷新增、拖拽排序和跨组修改、分组/子分组、折叠、空分组、分页加载、属性显示、选择状态；批量 API 与升级操作条分开登记 | `apps/web/core/components/issues/issue-layouts/list/`；`apps/web/core/components/issues/issue-layouts/kanban/`；`apps/api/plane/app/urls/issue.py` | 已实现；业务分组顺序/空组/分页 PG；115 项分页、分组拖动、原子跨项目拖放/权限与回滚浏览器 |
| F11 | 表格、日历、甘特 | 可编辑表格列；日历日期分布与交互；甘特周/月/季度范围及日期拖动和调整；筛选、排序、属性显隐、视图偏好；项目、周期、模块、保存视图中的适用布局 | `apps/web/core/components/issues/issue-layouts/`；`apps/web/core/components/gantt-chart/`；`packages/constants/src/issue/filter.ts` | 已实现；表格冲突、日历拖动、甘特移动/双侧调整/缩放浏览器 |
| F12 | 生命周期与 Intake | 草稿保存编辑删除和发布；Intake 的待处理、接受、拒绝、延后、重复项处理及描述版本；工作项归档恢复；已删除列表 API；按项目配置自动关闭和归档 | `apps/api/plane/app/urls/workspace.py`；`apps/api/plane/app/urls/intake.py`；`apps/api/plane/bgtasks/issue_automation_task.py` | 已实现；草稿/Intake/自动化/定时清理 PG；公开 Intake 浏览器 |
| F13 | Cycles | 创建编辑删除、日期检查、工作项加入移除/跨周期转移、收藏、视图偏好、归档恢复、进度与分析、周期详情侧栏 | `apps/api/plane/app/urls/cycle.py`；`apps/web/core/components/cycles/` | 已实现；周期快照/分析 PG；CRUD/转移/冻结/刷新浏览器 |
| F14 | Modules | 创建编辑删除、六种生命周期状态、负责人和成员、开始/目标日期、排序、关联工作项和链接、收藏、视图偏好、进度、归档恢复；模块甘特布局 | `apps/api/plane/app/urls/module.py`；`apps/api/plane/db/models/module.py`；`apps/web/core/components/modules/` | 已实现；模块/关联/链接 PG；CRUD/链接/日期/甘特浏览器 |
| F15 | 保存的 Views | 项目视图和跨项目 workspace 视图增删改查、过滤条件与展示方式保存、可见性、收藏；所有/分配给我/我创建/我订阅的默认集合 | `apps/api/plane/app/urls/views.py`；`apps/api/plane/db/models/view.py`；`packages/constants/src/workspace.ts` | 已实现；私有/跨项目 PG；视图增改删与持久化浏览器 |
| F16 | Pages | 项目页面列表/摘要、创建编辑、标题和图标、复制、收藏、项目内公开或个人私有、锁定、归档恢复/删除、版本列表详情；目录、链接/资产浏览、页面宽度；移动和对外分享当前被关闭 | `apps/api/plane/app/urls/page.py`；`apps/web/core/components/pages/`；`apps/web/core/hooks/use-page-flag.ts` | 已实现；访问/历史/资源 PG；两用户标题/正文/历史/PDF/锁定浏览器 |
| F17 | 编辑器与文档协作 | 富文本和协作文档；标题、段落、列表/任务列表、引用、代码、表格、图片、链接、分隔线、提示块、表情、提及和工作项嵌入；斜杠命令、块拖拽；Yjs 状态持久化、多人在线状态、同步/离线提示、权限验证和连接关闭 | `packages/editor/src/extensions/`；`apps/live/src/hocuspocus.ts`；`apps/live/src/extensions/`；`apps/web/core/components/pages/header/` | 已实现；JSON/HTML/Yjs 协议测试；两用户协作、离线/替换/撤权和编辑器命令浏览器 |
| F18 | 个人区域与首页 | 首页快捷链接、最近访问、便笺及组件显隐排序；个人档案、已分配/已创建/已订阅工作项、活动和统计；收藏分组和排序；侧栏置顶和顺序偏好 | `apps/web/core/components/home/home-dashboard-widgets.tsx`；`apps/api/plane/app/urls/workspace.py`；`packages/constants/src/profile.ts` | 已实现；偏好/档案 PG；首页组件/快捷链接/置顶/收藏排序及档案浏览器 |
| F19 | 通知 | 通知列表详情、未读计数、已读/未读、全部已读、归档、筛选；邮件通知和通知偏好；工作项变化/评论/提及产生通知 | `apps/api/plane/app/urls/notification.py`；`apps/api/plane/bgtasks/notification_task.py`；`apps/web/core/components/workspace-notifications/` | 已实现；偏好/范围/邮件 PG；真实 Redis/worker 通知与 SMTP 浏览器链路 |
| F20 | 搜索与导航 | workspace 搜索、实体搜索、项目工作项搜索；快捷操作和键盘导航、深链接、404/权限/空态；主要实体的预览与跳转 | `apps/api/plane/app/urls/search.py`；`apps/web/app/routes/core.ts`；`apps/web/core/components/issues/peek-overview/` | 已实现；搜索/标识符 PG；导航/命令/404与多角色搜索浏览器 |
| F21 | 分析 | workspace 概览和工作项两页分析、项目活跃情况、创建/解决趋势、优先级/自选维度图表、明细表和日期/项目筛选；项目、周期、模块和个人统计；保存分析和导出 API | `apps/api/plane/app/urls/analytic.py`；`apps/api/plane/app/views/analytic/`；`apps/web/core/components/analytics/` | 已实现；分析/档案 PG；自定义分析、保存、图表/明细及活动导出浏览器 |
| F22 | 公开 Sites | 发布/停用项目、分享锚点、公开元信息与显示配置；公开列表/看板和工作项详情、筛选数据；可配置评论、表情、投票、Intake 提交；公开用户认证与文件 | `apps/api/plane/space/urls/`；`apps/api/plane/db/models/deploy_board.py`；`apps/space/app/routes.ts` | 已实现；公开边界 PG；匿名/登录互动、开关、停用浏览器 |
| F23 | 开放 API | 用户 API token 生命周期；项目/成员/邀请、状态/标签/估点、工作项及关联资源、周期/模块、Intake、文件、便笺；列表、精简表示、分页、鉴权和接口文档 | `apps/api/plane/api/urls/`；`apps/api/plane/app/urls/api.py` | 已实现；token/字段/展开/分页 PG；实际路由 OpenAPI 校验；token 吊销浏览器 |
| F24 | 导出和数据交换 | 工作项 CSV/XLSX/JSON 异步导出、历史/状态/下载；分析导出、个人活动导出；页面 PDF；HTML 到编辑器 JSON/二进制转换。当前没有注册的工作项导入流程 | `apps/api/plane/app/views/exporter/base.py`；`apps/api/plane/bgtasks/export_task.py`；`apps/live/src/controllers/` | 已实现；三格式筛选一致 PG/Minio；实际队列 CSV、活动导出、Pages PDF 浏览器 |
| F25 | 外部服务与 Webhook | Google/GitHub/GitLab/Gitea 登录；可配置 AI 文本辅助；Unsplash 图片搜索；Webhook 创建编辑删除、密钥重置、事件投递和日志；详见外部能力清单 | `apps/api/plane/authentication/urls.py`；`apps/api/plane/app/views/external/base.py`；`apps/api/plane/app/urls/webhook.py` | 已实现；AI 实际 Terra 调用；四种 OAuth 模拟；Webhook 真实本地 HTTP 签名/重试；外部服务状态见 VALIDATION |
| F26 | 实例管理界面 | 独立管理员入口；常规配置、workspace 列表/创建、邮件配置与测试、认证方式及四种 OAuth 配置、AI 提供商/模型设置、图片服务配置 | `apps/admin/app/routes.ts`；`apps/api/plane/license/api/views/` | 已实现；配置/测试邮件/当前实体统计 PG；管理员界面及空安装管理流程浏览器通过 |
| F27 | 文件生命周期 | 用户/workspace/project 资产、上传及确认、下载、删除恢复、重复资产、批量实体资产、工作项附件；公开文件授权；上传未完成和过期文件清理 | `apps/api/plane/app/urls/asset.py`；`apps/api/plane/space/urls/asset.py`；`apps/api/plane/bgtasks/file_asset_task.py` | 已实现；真实 Minio 与批量实体附件、恢复/复制/清理 PG；上传/下载与角色变化浏览器 |
| F28 | 视觉与可用性 | 主站/管理站/公开站各类页面；原创浅深色及主题系统、侧栏、紧凑表格、看板、弹窗、抽屉和编辑器；响应式、焦点、键盘、加载/错误/空态，按同类场景对照 | `apps/web/app/`；`apps/admin/app/`；`apps/space/app/`；只对照呈现，不复用表达或资源 | 原创浅深色和响应式已实现；主站/公开站/窄屏浏览器；14张明确主题截图与独立风格复核，非原版同fixture像素验收 |
| F29 | 部署和运维 | 独立启动、迁移、后台任务与定时任务、协作服务、反代、对象存储、持久数据、健康检查、日志和清理；补齐本项目自己的备份恢复验证 | `docker-compose.yml`；`apps/api/plane/bgtasks/`；`apps/live/src/controllers/health.controller.ts` | 已实现；最新镜像空安装、迁移、健康检查、Redis/worker正常重启及重复任务通过；数据库/对象/恢复实例HTTP验证通过 |

## 子功能中容易漏掉的行为

| 范围 | 需要单独生成验收项的细节 |
| --- | --- |
| 状态与标签 | 状态分组、默认状态、Intake 状态、状态排序；项目标签与 workspace 汇总、分组标签、批量标签创建；删除时关联对象处理 |
| 估点 | 数字和类别两类；预置模板和自定义档位、项目启用/切换、工作项估点赋值和相关统计；时间估算见占位表 |
| 筛选与分页 | 优先级、状态/状态分组、负责人、创建者、标签、开始/截止日期、周期、模块、项目、订阅/提及等按页面适用；分组与子分组分页不能只验证普通列表 |
| 权限与集合 | 列表可见性、单对象访问、搜索结果、筛选项枚举、统计、导出、附件和公开站都受同一范围约束；只隐藏按钮不足以满足行为 |
| 归档与删除 | 项目、工作项、周期、模块、页面分别验证；默认列表不能混入草稿、归档和 Intake 工作项；父实体归档影响下属集合 |
| 协作与版本 | 同时编辑、重连、保存结果、标题同步、锁定/访问变化、版本内容和文件引用；生成 PDF 时再检查阅读权限 |
| 后台副作用 | 邀请/验证码/找回密码邮件、活动、提及通知、Webhook、导出、版本生成、对象复制、定时关闭/归档及日志清理 |
| 乐观交互 | 同一工作项出现在列表、看板、详情、周期、模块、保存视图中时一致更新；分组成员、计数和排序变化；失败回滚 |

## 已证实的外部能力与缺失入口

| 能力 | 基准分类 | 静态核对结果 | 证据 |
| --- | --- | --- | --- |
| Google、GitHub、GitLab、Gitea OAuth | 基准实现 | 主站和公开站都有发起及回调路由；管理员界面配置；应实现完整回调/会话/失败流程，真实服务验证另记 | `apps/api/plane/authentication/urls.py`；`apps/admin/app/routes.ts` |
| AI 文本辅助 | 基准实现 | workspace 与项目接口；配置 OpenAI、Anthropic、Gemini 及模型/API key，调用真实提供商的处理逻辑存在 | `apps/api/plane/app/urls/external.py`；`apps/api/plane/app/views/external/base.py`；`apps/admin/app/(all)/(dashboard)/ai/` |
| Unsplash | 基准实现 | 图片检索 API 和实例图片服务配置；无配置时的行为需覆盖 | `apps/api/plane/app/views/external/base.py`；`apps/admin/app/(all)/(dashboard)/image/` |
| Webhook | 基准实现 | 管理列表/详情、删除、密钥重生成、投递日志；后台事件投递逻辑。具体事件集合和重试语义在实现阶段逐方法核对 | `apps/api/plane/app/urls/webhook.py`；`apps/api/plane/bgtasks/webhook_task.py` |
| 工作项导出 | 基准实现 | CSV、XLSX、JSON；异步导出和历史查询 | `apps/api/plane/app/views/exporter/base.py`；`apps/web/core/components/exporter/` |
| 分析、个人活动导出 | 基准 API/实现 | 分析导出 API 和前端导出工具；个人活动导出路由 | `apps/api/plane/app/urls/analytic.py`；`apps/api/plane/app/urls/workspace.py`；`apps/web/core/components/analytics/export.ts` |
| Pages PDF、格式转换 | 基准实现/API | live 服务提供有权限检查的页面 PDF 导出，以及 HTML 转 JSON/Yjs 二进制的转换服务 | `apps/live/src/controllers/pdf-export.controller.ts`；`apps/live/src/controllers/document.controller.ts` |
| GitHub/Jira 导入或同步、Slack 业务集成 | 基准占位/遗留 | 存在前端服务、集成页面文件、GitHub/Slack/Importer 数据模型，但主站路由、设置菜单和后端已注册 URL 未接入这些渠道；不是本地版本可用功能 | `apps/web/core/services/integrations/`；`apps/web/app/(all)/[workspaceSlug]/(settings)/settings/(workspace)/integrations/page.tsx`；`apps/api/plane/db/models/integration/`；`apps/api/plane/db/models/importer.py` |

最后一行不包含 GitHub OAuth；OAuth 登录已单独确认为真实功能。也不能从常量中的
导入器名称推断存在可运行导入任务。若用户希望新增上述导入/同步渠道，需要明确新增
范围与第三方验证要求。

## 基准占位和未接通的高级能力

| ID | 能力或入口 | 核对结果 | 证据 |
| --- | --- | --- | --- |
| B01 | workspace Active Cycles 汇总 | 页面只渲染升级介绍，无跨项目周期分析应用 | `apps/web/app/(all)/[workspaceSlug]/(projects)/active-cycles/page.tsx` |
| B02 | 批量选择后的操作工具条 | 当前工具条只显示升级入口；服务端批量删除和批量归档是真实 API，必须分开计数 | `apps/web/core/components/issues/bulk-operations/root.tsx`；`apps/api/plane/app/urls/issue.py` |
| B03 | 时间估算 | 存在常量和类型，但 UI 启用函数只允许数字/类别；时间系统被标记为扩展功能 | `apps/web/core/components/estimates/create/helper.tsx`；`packages/constants/src/estimates.ts` |
| B04 | 页面移动、页面对外分享 | 功能开关均为 false；页面“公开”是项目成员可见，不能解释成匿名公开链接 | `apps/web/core/hooks/use-page-flag.ts`；`apps/api/plane/app/permissions/page.py` |
| B05 | 付费订阅/结算 | 本地页面展示社区套餐与套餐比较、跳转；不能据此声称有本地收款或订阅账务系统 | `apps/web/core/components/workspace/billing/root.tsx`；`packages/constants/src/payment.ts` |
| B06 | 首页更新资讯、教程组件 | 组件映射是 null；真实首页组件是快捷链接、最近访问、便笺 | `apps/web/core/components/home/home-dashboard-widgets.tsx` |
| B07 | 高级扩展路由 | `extendedRoutes` 为空。Epics、工作项类型等共享模型/枚举不构成已可用的完整功能证据 | `apps/web/app/routes/extended.ts`；`apps/api/plane/db/models/issue_type.py` |
| B08 | 甘特依赖展示 | 常量 `ENABLE_ISSUE_DEPENDENCIES` 为 false；一般工作项关系 API 仍真实存在 | `packages/constants/src/issue/filter.ts`；`apps/api/plane/app/urls/issue.py` |
| B09 | 完整页面树、全局 Wiki | 模型含 parent/is_global 等字段，但本次已注册路由仅证实项目 Pages；不能仅凭字段承诺完整 Wiki/树形能力 | `apps/api/plane/db/models/page.py`；`apps/web/app/routes/core.ts` |

基准占位应保持可见的范围记录，不以空壳实现冒充高级功能。是否在原创产品中保留
升级宣传页面属于产品决策；不为复刻社区源码而自动添加商业付费流程。

## 角色与访问边界

| 维度 | 已核对语义 | 实现时的验收重点 |
| --- | --- | --- |
| 角色 | Admin=20、Member=15、Guest=5；workspace 与 project 分开记录；实例管理员为独立入口 | 不将 workspace 管理员自动视为所有私有项目成员；后端校验活跃成员 |
| 项目管理 | workspace Admin/Member 可创建项目；项目修改由项目管理员或已加入该项目的 workspace 管理员处理 | 创建、列表可见性、更新/删除权限分别测试 |
| 工作项 | 一般读取允许三种项目角色；新增/编辑主要是 Admin/Member；部分操作有创建者例外；Guest 可受“仅看自己创建的工作项”限制 | 按端点核对创建者、角色与可见性组合，不压成一条全局 CRUD 权限 |
| Pages | 必须有活动项目关联和项目成员资格；私有页面仅所有者；项目内公开页普通成员可读写，Guest 只读；所有者/管理员掌握删除、锁定、归档等控制 | 私有页读取、URL 中 project 与 page 不匹配、软删关联、锁定/归档状态以及协作连接权限 |
| 项目设置 | 管理设置和功能开关主要为管理员；状态/标签可向成员开放；估点管理为管理员 | UI 与服务端动作逐项对应，不能只沿用导航可见性 |
| 公开站 | 发布锚点与停用状态决定匿名可见性，评论/反应/投票/Intake 为独立配置；互动用户会话与内部项目成员区别处理 | 停用立即失效；公开资源不得绕过项目公开配置或泄露私有附件 |

来源：`apps/api/plane/app/permissions/base.py`、`project.py`、`page.py`，
`apps/api/plane/app/views/issue/base.py`，`apps/api/plane/space/`。

## 页面与接口清单的边界

| 表面 | 已观察规模 | 入口来源 |
| --- | --- | --- |
| 主站 | 68 个 route/index 声明，包含重定向；额外在总入口添加 404 | `apps/web/app/routes/core.ts`；`apps/web/app/routes.ts` |
| 实例管理 | 13 个 route/index 声明，包含 404 | `apps/admin/app/routes.ts` |
| 公开站 | 4 个 route/index 声明，包含 404 | `apps/space/app/routes.ts` |
| 主应用 API | 233 个 path 声明 | `apps/api/plane/app/urls/` |
| 开放 v1 API | 75 个 path 声明；便笺使用路由器动态展开 | `apps/api/plane/api/urls/` |
| 公开 API | 25 个 path 声明 | `apps/api/plane/space/urls/` |
| 实例 API | 14 个 path 声明 | `apps/api/plane/license/urls.py` |
| 认证 | 37 个 path 声明 | `apps/api/plane/authentication/urls.py` |
| 附加服务 | Web 健康检查/robots、可选 schema；live 的协作 WebSocket、文档转换、PDF 和健康检查 | `apps/api/plane/urls.py`；`apps/api/plane/web/urls.py`；`apps/live/src/controllers/` |

五组主要后端路由文件共 **384 个 path 声明**，不是精确 HTTP 操作数：同一路径可有
多个方法，Intake 有历史别名，v1 便笺路由器会展开路径，schema 和健康检查另计。
API 计数只用于遗漏检查，不是完成率分母。

后续机器清单采用原创 JSON 数据结构，只提取事实性元数据，不携带函数体、原文案或
资源。建议拆成如下四份：

| 清单 | 建议字段 | 生成与核对方式 |
| --- | --- | --- |
| 参考页面 | app、path_pattern、entry_file、kind、feature_ids、baseline_status | 解析主站 core/extended 合并结果、管理站与公开站；区分页面、重定向、404 |
| 参考服务操作 | service、path_pattern、method、handler_name、source_file、feature_ids、baseline_status | Python AST 提取 path/as_view 映射和 ViewSet 方法；动态路由器必须展开，无法解析时显式标 unresolved；另读 live 控制器装饰器 |
| 原创实现契约 | operation_id、feature_ids、request_schema、response_schema、auth_policy、side_effects | 由 Go OpenAPI 和领域规格生成，不添加旧接口兼容映射 |
| 验收追踪 | acceptance_id、feature_id、scenario、implementation_paths、automated_evidence、browser_evidence、visual_evidence、status | 每项关联具体行为与证据；未覆盖、未验证、不适用分别表达 |

静态提取不能独自证明 serializer 字段、继承的权限、数据库约束、任务副作用和真实
运行结果。每个功能组实施时继续补全动作级验收；本清单是第一轮完整范围盘点，不是
逐个操作的最终契约或运行通过证明。
