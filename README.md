# my-jira

从零开发的团队项目管理应用。前端采用 React / TypeScript，业务 API 采用 Go，
具备工作区和项目、工作项五种布局、周期、模块、保存视图、多人协作文档、通知、
分析、公开站、开放 API、Webhook、文件和导出能力。

功能范围以本地 Plane 社区版本 `1fec307f91003df96351557af32ce87891a3678a`
为基准。应用代码、样式、文案与界面资源独立编写，不导入或改写 Plane 前端实现，
也不提供旧 API、数据库或历史数据兼容。已完成当前社区功能范围的原创实现和
本地验收，[功能差距与边界](docs/PARITY_GAPS.md)保留逐项修复及未验证范围。

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
