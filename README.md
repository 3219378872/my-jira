# my-jira

当前迭代增加需求故事地图、资源排期与 UML 联动，以及受项目策略约束的 AI
工作流。范围与进度见[需求基线](docs/REQUIREMENTS_BASELINE.md)、
[三阶段计划](docs/ITERATION_PLAN.md)和[独立验证记录](docs/REQUIREMENTS_VALIDATION.md)。

从零开发的团队项目管理应用。前端采用 React / TypeScript，业务 API 采用 Go，
具备工作区和项目、工作项五种布局、周期、模块、保存视图、多人协作文档、通知、
分析、公开站、开放 API、Webhook、文件和导出能力。

功能范围以本地 Plane 社区版本 `1fec307f91003df96351557af32ce87891a3678a`
为基准。应用代码、样式、文案与界面资源独立编写，不导入或改写 Plane 前端实现，
也不提供旧 API、数据库或历史数据兼容。已完成当前社区功能范围的原创实现和
本地验收，[功能差距与边界](docs/PARITY_GAPS.md)保留逐项修复及未验证范围。

## 界面预览

以下截图来自本项目运行中的原创前端，使用本地演示数据，展示浅色与深色主题。
点击图片可查看原始尺寸。

| 工作项列表 · 浅色 | 看板 · 深色 |
| --- | --- |
| [![按状态分组的工作项列表](docs/screenshots/work-items-light.png)](docs/screenshots/work-items-light.png) | [![按状态分列的深色看板](docs/screenshots/kanban-dark.png)](docs/screenshots/kanban-dark.png) |

| 工作项详情 | 协作文档 |
| --- | --- |
| [![工作项详情、属性和评论](docs/screenshots/work-item-detail-light.png)](docs/screenshots/work-item-detail-light.png) | [![文档编辑器和协作连接状态](docs/screenshots/document-editor-light.png)](docs/screenshots/document-editor-light.png) |

| 个人设置 | 公开项目 · 深色 |
| --- | --- |
| [![个人资料与设置导航](docs/screenshots/personal-settings-light.png)](docs/screenshots/personal-settings-light.png) | [![公开项目、投票与需求入口](docs/screenshots/public-project-dark.png)](docs/screenshots/public-project-dark.png) |

## 后端架构

业务后端采用 Go 模块化单体，包含身份与工作区、项目与工作项、周期与模块、
文档、通知、文件及集成等领域。API 与后台 Worker 独立运行；Node 服务负责
实时文档协作与 PDF 渲染。下图展示当前 Docker Compose 部署的主要调用关系。

```mermaid
flowchart TB
    browser["浏览器<br/>React / TypeScript"]
    web["Caddy<br/>静态前端与反向代理"]
    api["Go API<br/>Gin · Ent · 领域模块"]
    live["Node live<br/>Hocuspocus / Yjs 协作<br/>Chromium PDF 渲染"]
    postgres[("PostgreSQL<br/>业务数据 · 文档状态 · outbox")]
    redis[("Redis<br/>Asynq 任务队列")]
    s3[("S3 / MinIO<br/>附件与导出文件")]

    subgraph worker["Go Worker 进程"]
        dispatcher["Outbox Dispatcher<br/>扫描事件并记录投递结果"]
        consumers["Asynq Consumers<br/>通知 · 导出 · Webhook"]
    end

    smtp["SMTP<br/>开发环境使用 Mailpit"]
    webhook["外部 Webhook 接收端"]
    ai["AI 提供商（当前配置）<br/>OpenAI Responses<br/>gpt-5.6-terra"]

    browser --> web
    web -->|/api/*| api
    web -->|/live · WebSocket / PDF| live
    live -->|权限 · 文档 · 附件| api
    api -->|业务与 outbox 事务| postgres
    postgres <-->|outbox 读取与标记| dispatcher
    dispatcher -->|投递任务| redis
    redis -->|消费与重试| consumers
    consumers -->|业务读写| postgres
    api -->|鉴权文件读写| s3
    consumers -->|导出与报表| s3
    consumers -->|发送邮件| smtp
    consumers -->|事件投递| webhook
    api -->|AI 文本辅助| ai
```

需要异步处理的业务变更与 outbox 事件在同一个 PostgreSQL 事务中提交；Worker 内的
Dispatcher 将事件投递到 Redis，由 Asynq 执行任务并处理重试。Worker 还运行定时
维护与过期文件清理。文档权限和持久化统一由 Go API 负责，协作服务通过内部 HTTP
调用读写文档内容与 Yjs 状态。
数据库迁移由独立的 `migrate` 一次性进程执行，完成后才启动 API 与 Worker。
更多设计说明见 [架构决策](docs/ARCHITECTURE.md)和[数据库结构](docs/DATABASE.md)。

## 启动开发环境

需要 Go 1.26、Node 22.12+、pnpm 10.24、Docker Compose。初次配置时将
`.env.example` 复制为 `.env`，设置至少 32 字符的随机 `APP_ENCRYPTION_KEY`。
已有 `.env` 时保留现有配置。所有 `make` 命令会加载该文件。

```sh
pnpm install --frozen-lockfile
pnpm exec playwright install --with-deps chromium
make infra
make migrate
pnpm --filter @myjira/editor-schema build
```

分别在四个终端启动：

```sh
make api
make worker
make live
make web
```

打开 http://127.0.0.1:4173，首次访问会引导创建实例管理员和工作区。
`make seed` 可生成本项目原创的中文演示数据；它只接受本地地址。演示账号为
`demo@myjira.local`，密码为 `MyJira-Local-2026!`，工作区地址 `/w/studio`。
本地端口及数据归属见 [开发说明](docs/DEVELOPMENT.md)。

AI 通过 OpenAI Responses 接口调用 `gpt-5.6-terra`。本次环境使用用户
`~/.codex/mine.config.toml` 中配置的接口地址与 `MINE_API_KEY` 环境变量；密钥
不复制进源码、示例配置或文档。生产部署也可在实例服务设置中加密保存凭据。
文档 PDF 导出和浏览器测试默认使用上述 Playwright 安装的 Chromium，也可通过
`CHROMIUM_PATH` 指定已有可执行文件；容器镜像包含 Chromium、中文和表情字体。

## 容器运行

```sh
make app
```

默认访问 http://127.0.0.1:4180。该命令构建原创应用镜像、执行版本迁移，启动
Caddy、Go API/worker 和 Node 协作服务，并使用当前 `my-jira` 项目的数据库、
Redis 与对象存储。`make app-stop` 停止应用，保留数据卷。
对外地址由 `DEPLOY_ORIGIN` 配置；生产部署需要按实际域名配置 TLS。

如果 Docker 构建环境不能连接默认 Go 模块源，可指定可达的模块代理：

```sh
docker compose -f compose.yaml -f compose.app.yaml build --build-arg GOPROXY=https://goproxy.cn
```

## 验证与接口

```sh
make check
make test
TEST_DATABASE_URL='postgres://myjira:myjira_local_dev@127.0.0.1:25432/myjira_test?sslmode=disable' TEST_S3_ENDPOINT=127.0.0.1:29000 make integration
make e2e
make verify-installation
```

集成测试仅接受显式命名的测试数据库，并为每个测试创建独立 schema；配置
`TEST_S3_ENDPOINT` 后还会验证真实对象存储。浏览器测试默认使用本地演示数据，
创建自己的项目或实体；可用 `E2E_BASE_URL` 切换到容器地址。

`verify-installation` 创建独立测试栈，从空实例验证初始化、邮件邀请、文档与PDF，
再验证队列任务跨Redis/worker重启及重复投递，最后清理该栈的临时数据卷。
它使用4181等专用端口，若发现已有同名测试栈会先退出。构建需要替换Go模块源时，
可使用 `MYJIRA_BUILD_GOPROXY=https://goproxy.cn make verify-installation`。

[OpenAPI 3.1](docs/openapi.json) 也通过 `/api/v1/openapi.json` 提供；
[协议说明](docs/API_CONTRACT.md) 包含身份、权限、筛选、分页、版本与异步任务约定。
构建、数据库、浏览器、真实提供商和视觉证据分别登记在
[验证记录](docs/VALIDATION.md)，不能相互替代。

## 备份恢复

`make backup` 生成数据库 dump、对象文件、对象元数据和校验清单。
`make verify-backup SNAPSHOT=/absolute/snapshot/path` 在独立数据库和独立
bucket 中恢复、校验，并通过恢复实例的鉴权 HTTP 下载验证附件。
跨数据库和对象存储的一致快照需要先暂停写入；加密密钥须另行安全保存。
具体恢复步骤与清理边界见 [开发说明](docs/DEVELOPMENT.md)。

进一步阅读：[实施计划](docs/PLAN.md)、[29 组功能矩阵](docs/FEATURE_PARITY.md)、
[架构决策](docs/ARCHITECTURE.md)、[数据库结构](docs/DATABASE.md)。
