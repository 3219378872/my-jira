# 验证记录

日期：2026-09-09。对象：`/home/dev/projects/my-jira` 原创工作树。
基准：Plane `1fec307f91003df96351557af32ce87891a3678a`。

本文件区分静态检查、真实基础服务、浏览器和真实外部提供商。路由、构建与截图
数量不能代替行为验收，也不能推出生产容量或全部组合均已验证。

## 环境与数据归属

- Go 1.26.6、Node 24.16.0、pnpm 10.24.0、Chromium。
- 开发Compose仅使用本项目的PostgreSQL `25432`、Redis `26379`、Minio
  `29000/29001`、Mailpit `21025/28025`；其他仓库服务保留。
- Go集成显式指定 `myjira_test`，各用例创建并清理独立schema；Minio用例使用独立
  随机bucket。业务数据库已迁移到 `0008`，启动应用不会隐式修改schema。
- 开发浏览器默认地址 `http://127.0.0.1:4173`，最终容器验收地址
  `http://127.0.0.1:4180`；桌面 `1440×960`、语言 `zh-CN`。
  演示workspace为 `studio`，原始12个演示工作项保留。
- `my-jira-bootstrap-test` 为独立空安装验收栈，单独卷/网络/队列，端口
  `4181/25433/26380/29002/29003/21026/28026`。
- `.local/evidence`、trace、报告、备份和环境密钥均被忽略。Trace可能包含会话及
  邮件令牌，不是可公开分发的交付物。临时项目/workspace经API软删除；E02临时
  注册账户登出后保留，没有用于测试的账号硬删接口。

## Go、数据库与对象存储

```sh
TEST_DATABASE_URL='postgres://myjira:myjira_local_dev@127.0.0.1:25432/myjira_test?sslmode=disable' \
TEST_S3_ENDPOINT=127.0.0.1:29000 make integration
```

最终完整集成已通过：foundation 35.197s、integrations 19.425s、workitems
22.099s、files 10.307s、documents 10.792s、support 15.669s、planning 2.346s、
projection 4.651s。该次包括批量实体资产与workspace生命周期集合权限修复。
随后管理员统计的父实体软删除修复通过foundation全包25.388s及对应vet；未因该
局部变更重复运行无关领域。最后分组业务顺序和原子跨项目拖放追加后，完整
workitems包再次23.428s通过，包含目标属性失败回滚、版本409及等待数据库锁时
成员降级/撤销后的403/404；对应vet通过。

| 范围 | 已取得的真实证据 |
| --- | --- |
| 身份/组织/实例 | 管理员初始化、密码/邮箱验证码、邮箱变更、恢复、会话/CSRF、邀请、token、角色与项目配置；PG；四个OAuth模拟提供商；项目/workspace软删除后当前项目、工作项及逻辑附件容量统计退出 |
| 工作项 | 关联/父子/移动、并发版本409、批量事务回滚、描述历史、草稿/Intake、估点、自动关闭/归档、分组分页；workspace已删除集合检查有效项目角色；跨项目移动与目标分组在同一事务，等待锁后重新验证两侧当前成员资格 |
| 规划 | 周期/模块CRUD、日期边界、转移失败回滚、转移前首次冻结快照、负责人/标签/估点/燃尽；项目/规划/评论事件事务outbox |
| Pages/编辑器 | 显式项目成员、Guest范围、私有owner、锁定/归档、版本/资源；数据库锁等待验证成员变更；原生JSON结构/HTML清洗/工作项嵌入；未经服务签名的非空Yjs二进制拒绝403 |
| 通知/分析/导出 | 通知原因/分页/过滤批量已读；多维/保存分析和档案；CSV/XLSX/JSON与列表共用复合筛选，实际Minio核对文件及撤权后下载拒绝 |
| Webhook | 通用工作项及评论共9次真实本地HTTP投递、独立事件ID；503→轮换密钥→正确签名→成功→重复不投；当前事件配置与角色复查 |
| 资产 | 实际上传/下载/删除/恢复/复制/清理；批量跨2项目和workspace页面6附件逐一核对原字节；Guest/owner/成员变更及整批404 |
| 开放API | 实际Gin路由覆盖校验：190 paths/288 operations/137 schemas；字段及展开投影先授权，保留精确整数；数量不是完成率 |

F27补齐后，独立 `go test -race -count=1 ./internal/files ./internal/openapi` 为5.081s/2.094s；
`go vet ./internal/files ./internal/openapi ./internal/application` 通过。
此前foundation/projection的race分别35.650s/4.141s通过。

## 浏览器与真实链路

除明确标出的403失败模拟外，以下成功请求、会话、角色变更、协作、数据库和队列
副作用均真实执行。分开运行的Playwright测试使用各自output目录。

| 场景 | 通过结果与关键断言 |
| --- | --- |
| `product.spec.ts` | 最终容器4.6s/18.3s/12.3s通过：工作项五布局、编辑/评论/附件/刷新；双用户正文/标题、持久化、PDF、恢复后续写和两端锁定；导航/管理员/设置/主题/窄屏。此前锁定失败已通过live卸载竞态修复解决 |
| `planning.spec.ts` | 最终容器3项通过，12.4s/22.6s/6.8s；115项100/15普通分页、50/60分组分页、子分组；UI构造AND/OR恰好4项；真实日期/409与模拟403回滚；周期/模块/视图CRUD、冻结转移、链接；看板/日历/甘特拖动、左右调整/缩放及回滚 |
| `access-scope.spec.ts` | 最终容器44.0s通过；四独立会话、真实UI降Guest与开关；列表/详情/分组/搜索/分析/通知/附件；3份CSV经HTTP→outbox→Redis→worker→Minio；Member及Guest owner的private PM/public PM/WM撤销。包含新增Guest创建/批量入口、所有者/其他作者字段、表格实际保存、看板/日历/甘特与详情编辑权限断言 |
| `collaboration-access.spec.ts` | 最终容器15.7s通过；两用户HTML转换、REST替换、离线立即只读、重连补齐；私有化/成员移除后连接只读及内容404/PDF或转换403；owner继续编辑 |
| `queue-public.spec.ts` | 最终容器6.2s/4.3s通过；实际队列通知与Mailpit邮件、导出下载、Bearer token读取/吊销；匿名读取、公开用户评论/反应/投票/Intake和逐项停用 |
| `.local/editor-ui-check.mjs` | 通过、0 page errors；斜杠转标题、原生emoji、按当前权限读取的工作项嵌入、块拖动、保存刷新 |
| `.local/profile-analytics-ui-check.mjs` | 通过、0 page errors；资料/时区/语言/主题/通知保存刷新；档案四tab、项目过滤和活动CSV；自定义分析创建/重载/图表表格/指标更新 |
| 首页/侧栏脚本与 `preferences.spec.ts` | 脚本通过、0 page errors；组件显隐/排序、快捷链接CRUD、项目置顶/排序、两收藏连续分组及组内顺序/改名/删组保留收藏；最终容器连续收藏竞态回归7.4s通过 |
| 选择器与跨项目视图脚本 | 实际浏览器通过105项档案集合100+5分页、父项搜索定位第105项及排除子项、估点筛选、跨项目周期/模块筛选选项、workspace保存视图五布局和项目展示偏好重载 |
| `grouping.spec.ts` | 最终容器26.2s/7.0s通过；跨项目一次move同时选择状态/优先级/标签等目标分组，模拟403保留源数据、真实409刷新；创建者跨组与Guest目标禁投，同组排序可用；优先级语义DOM顺序，非列表/看板隐藏分组控件且切回/重载保留配置。隔离workspace均已软删除 |

已从失败中修正的实际产品行为包括：外置协作socket未attach、逻辑document关闭未
清除可编辑状态、无binary页面的REST替换未失效缓存、连续收藏覆盖偏好、规划视图
缺少拖拽、导出筛选窄于列表、恢复文档后迟到卸载误删新协作登记、分组UUID顺序
不符业务语义，以及等待锁期间成员变更后移动仍使用旧角色。失败测试本身不作为
通过证据，后两类均新增真实依赖/数据库回归后复测。

## 容器与队列重启

API、worker/migrate、live与web镜像已构建并完成独立空安装验收，Caddy负责反代。
自动创建、验证、清理独立栈的命令为 `make verify-installation`，本次设置可达的
`MYJIRA_BUILD_GOPROXY=https://goproxy.cn`，完整命令exit 0。
其内部依次执行：

```sh
E2E_FIRST_RUN=1 E2E_BASE_URL=http://127.0.0.1:4181 \
E2E_MAILPIT_URL=http://127.0.0.1:28026 \
pnpm exec playwright test tests/e2e/first-run.spec.ts \
  --output=.local/evidence/bootstrap-playwright --reporter=list

E2E_FIRST_RUN=1 E2E_BASE_URL=http://127.0.0.1:4181 \
pnpm exec playwright test tests/e2e/queue-restart.spec.ts \
  --output=.local/evidence/queue-restart-playwright --reporter=list
```

最新镜像首次安装1项14.1s通过（测试本体13.1s）：管理员初始化→workspace→项目→工作项→Mailpit真实邀请
URL→独立浏览器新账号注册并接受邀请→管理站→协作文档持久化→PDF。
初次测试误从响应取未公开的邀请token；修正测试后从全新的空数据库完整重跑通过。
队列测试暂停消费、确认目标任务已入Redis，重启worker/Redis检查同一任务保留，
完成导出，再以新任务ID重放同一业务payload，核对完成时间及下载字节不变。
最新镜像队列重启1项15.9s通过（测试本体15.1s）；确认的是精确 `export.generate` 任务、暂停状态及DB
queued状态，重启后同一任务完成且重复执行不改完成时间或导出字节。
命令已清理专属 `my-jira-bootstrap-test` 的9个容器、1个网络和3个数据卷，随后按
标签和名称双重检查没有残留。主实例8个服务持续运行。
它验证正常重启及重复交付，不是主机断电、执行中强杀或极端故障注入验证。

## 备份恢复

```sh
make backup
make verify-backup SNAPSHOT=/home/dev/projects/my-jira/.local/backups/snapshot.ad3SUp9Q
```

实际通过：隔离数据库恢复/迁移、5对象SHA256/MIME/元数据、恢复实例中已登录用户
HTTP下载验证附件并核对原字节、匿名401。三份关联已删除工作项的历史资产仍不可读，
符合恢复后的当前权限。

数据库 `myjira_restore_test_20260909100112_419250`、bucket
`myjira-restore-test-20260909100112-419250-6488` 已清理。原实例中专门创建的验证
工作项随后API软删除，其附件现404；快照保留，12个演示工作项未删。
严格跨数据库/对象存储的一致快照需要暂停写入，加密密钥单独保存；在线持续写入的
恢复点、跨地域灾备和恢复时间目标未验证。

## 外部服务

| 服务 | 实现与证据 | 未验证范围 |
| --- | --- | --- |
| AI | 用户指定接口；开发环境真实已登录应用请求HTTP200、`MYJIRA_APP_OK`；正式容器再次经业务API请求HTTP200、model=`gpt-5.6-terra`、输出`MYJIRA_CONTAINER_OK`；文本辅助/加密服务配置已实现 | 未评估语义质量、容量、费用或长期可用性 |
| Google/GitHub/GitLab/Gitea OAuth | 配置、state、回调、关联和会话；4种模拟提供商通过 | 无真实应用凭据，真实授权流程未验证 |
| Unsplash | 配置、查询与错误处理已实现 | 无真实凭据，真实检索未验证 |
| SMTP | 真实本地Mailpit接收邀请/通知，配置测试和发送时权限复核通过 | 公网服务、域名认证和外部邮箱投递未验证 |
| Webhook | 本地真实HTTP验证签名、失败/重试、去重、过滤、轮换及撤权 | 没有向用户未指定的外部目标发送消息 |

## 视觉与原创边界

截图在 `.local/evidence/browser-*.png`，覆盖主站、规划、详情、编辑器、设置、档案、
分析、公开站、管理员与窄屏。最终14张截图已按显式浅/深主题、1440×1000视口与
清理后的演示fixture刷新：`browser-final-{list,board,detail,editor,settings,admin,public}-{light,dark}.png`。
管理页最后从4180容器重新截图，当前项目1、工作项12；原始演示项未改。
全部功能收尾后，列表/看板四张截图再从最终4180镜像刷新，仍为1440×1000和
原12项fixture；无页面错误或全页横向溢出，用户偏好/布局/最近访问未改变。
独立复核没有发现明显缺字、重叠、裁切或混合主题。早期
`browser-work-item-light.png` 实际受账号深色偏好影响，文件名不能证明主题。

参考渲染为基准README引用的公开图：
`https://media.docs.plane.so/GitHub-readme/github-top.webp`、
`https://media.docs.plane.so/GitHub-readme/github-work-items.webp`。
只保存在忽略目录用于观察灰阶、边框、卡片密度和布局，不进入产品；宣传图可能
含社区版外界面，不能据此扩大功能范围。

原创界面采用紧凑侧栏、顶部路径、列表/卡片、低饱和主题、细边框、抽屉和富文本。
当前视觉证据是相近风格的人工观察与原创浏览器检查；未启动固定参考提交做同数据
同视口的像素比较，不能声称像素级一致。测试机的emoji缺字已用独立FONTCONFIG和
Noto Color Emoji解决；没有复用Plane资源。live容器包含CJK/emoji字体供PDF使用。

## 最终工作树

最终源代码的 `make check` 前端/live类型和Go vet已通过；前端生产构建通过，
web单元10项、live单元/协议4项全部通过。前端新增移动缓存用例验证等待/失败不
修改项目归属，只有请求成功才更新索引，且没有第二次补偿写入。
新增回归使用真实Hocuspocus实例重现旧文档卸载后同名新文档登记，再次清理旧实例
必须保留新实例及正文，同时新实例的正常卸载仍可完成。

早期4180合集曾为10 passed、1 failed、2 expected skips；该次失败推动了协作
卸载竞态修复。最终源代码重新构建后，完整合集为 **13 passed、2 expected skips，
3.2min，exit 0**。两个skip为仅在独立空栈执行的安装和重启测试，最新镜像也分别
实际通过14.1s/15.9s。主合集状态位于 `.local/evidence/final-playwright/.last-run.json`。

```sh
CHROMIUM_PATH=/home/dev/.local/bin/chromium \
E2E_BASE_URL=http://127.0.0.1:4180 \
FONTCONFIG_FILE=/home/dev/projects/my-jira/.local/browser-fonts/fonts.conf \
pnpm exec playwright test --output=.local/evidence/final-playwright --reporter=list
```

上述浏览器路径及字体配置属于本机；代码默认使用Playwright安装的浏览器，其他
机器可按README安装或指定自己的 `CHROMIUM_PATH`。

主环境实际运行镜像：

| 服务 | 镜像ID |
| --- | --- |
| API/worker | `sha256:9f768b83a0d5d24189f5aac85225ab5b0fae4e09f01aeacf761877a721675169` |
| Live | `sha256:bb396f9ce64481f3b0cf28790d2d76d181557bb498592ac39179c3a05c6b23ad` |
| Web | `sha256:d1ea09e8ba032f6f2c532f0daf894f5c4370fb88b2ef1a24e8abe35a8717e3fb` |

空安装项目重用缓存构建，镜像ID因Compose项目标签不同而改变；已逐项核对三对
镜像的 `RootFS.Layers` 完全一致。参考仓库仍处于固定提交且工作树干净。
原创源码扫描未发现Plane包导入、参考资源或其版权文案；该扫描不等同于正式来源鉴定。

交付前通过真实登录会话核对演示workspace仅1个项目、12个原工作项和1个原文档；
本轮测试创建的工作区/项目/页面均按各自清理流程处理。临时开发进程4173/8088/3101
已停止，正式4180的API/worker/live/web和基础服务继续运行，持久卷保留。
