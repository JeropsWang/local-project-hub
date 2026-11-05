# 项目配置、模块接口与验收工程设计

日期：2026-11-05。状态：在已确认兼容范围上细化的工程设计，供评审；本轮提供格式契约和实施计划，尚未建立应用源码或安装依赖。

## 依据与工程选择

遵循 [兼容设计](2026-11-05-compatibility-design.md)、[需求](../../requirements.md) 与 [分层约定](../../engineering-conventions.md)。独立 Windows 桌面应用采用 Go + Wails，前端 React + TypeScript + Vite，CSS Modules。首轮验收 Windows 11 x64、Windows 本机进程及 Docker Desktop Linux 引擎，不在 WSL 中执行项目命令。

用户已确认：三个指定项目优先完整适配、双模式环境处理、用户先初始化目录、关窗留托盘、明确退出时安全停止项目、可编辑脚本、配置导入导出和职责分层。下面的数据存储、并发、限额和接口是助手为落实这些要求所作的具体工程选择，不能当成用户原话或已实现能力。

选择工作空间 SQLite 保存本机登记和任务状态，便于事务；便携项目 JSON 是导入/导出的快照与外部输入，不同时维护第二套可编辑权威记录。选择单个工作空间内串行的资源变更队列，先降低启停、安装与导入相互冲突的复杂度。纯检测可以有限并发。

## 子系统与交付顺序

1. 配置、工作空间和只读检测核心。
2. 任务、环境准备、进程/容器与安全退出。
3. 配置导入导出；依赖核心，可独立验证。
4. Wails 客户端、界面、三个项目集成及 Windows 安装包。

每部分独立列计划和测试，不把四部分一次塞进入口文件。项目仍是模块化单体，不增加必需 HTTP 服务或 Node 后端进程。

## 配置契约

格式文件位于 [docs/contracts](../../contracts/README.md)，是字段、枚举和 JSON 命名的权威定义。版本为 1.0，未知主次版本拒绝导入；未来只执行明确登记的纯数据迁移，不调用包内脚本升级格式。

ProjectDocument 保存 source、requirements、files、units、profiles。每种运行方式单独选择 profile；执行时展开依赖并结合 featureIds。运行单元区分 process、task、compose、external。task 的 effect 区分 build、initialize、migration，cancelPolicy 区分 checkpoint 与 wait。

结构校验、语义校验、机器环境检查和实际启动验证是四件事。stop=manual 可以登记，但不能成为已通过完整管理的 profile。profile 引用的运行单元缺少停止策略、外部绑定或必需环境时，Start 不提交执行。

Schema 本身不执行远程 $ref，不根据输入下载验证器。源项目版本条件只能生成声明或候选要求；不能导入“本机已验收”的证明字段。

## 路径与工作空间

用户选择本机目录后生成 workspace.json 和 workspace.db，并按需创建：

```text
projects/<projectId>/revisions/<revision>/files/
runs/<runId>/
runtimes/<component>/<version>-windows-amd64/
envs/<projectId>/<profileId>/<environmentFingerprint>/
cache/
imports/<previewId>/
logs/
```

首版一个应用实例同时打开一个工作空间，使用跨进程锁防止重复写。可以保存已知工作空间入口；切换前必须没有活动运行或资源变更任务，不能把“修改根目录字符串”当成迁移。

workspace.json 只保存身份、名称、格式版本和创建时间。实际根目录从用户选择的位置取得，与工作空间数据库上次绑定记录比较；移盘后进入重绑定检查。应用少量入口设置位于用户应用配置目录，不把 SDK、环境和大缓存默认放进 C 盘。

PathRef 只保存 binding 与 relativePath，实际绑定记录存本机数据库。source 目录默认只读；自动准备涉及写入时，只在明确管理的源码副本或环境目录操作。原位置工程的既有 venv/.deps 不被覆盖。

## 持久化与事务

SQLite 使用 database/sql，驱动基线为 modernc.org/sqlite，实际依赖版本在实现时锁定；不依赖系统额外安装 SQLite 服务。工作空间拒绝网络共享目录作为首版写入目标。

本机数据表按职责分开：

| 表 | 关键字段与约束 |
| --- | --- |
| workspace_meta | workspace_id、db_schema_version、last_bound_root |
| projects | project_id 主键、name、current_revision |
| project_revisions | project_id + revision 唯一、document_json、content_hash、created_at |
| bindings | workspace_id + project_id + binding_key 唯一、kind、local_locator 或 secret_ref |
| resources | resource_id、engine/进程定位、control_grant、配置指纹 |
| tasks | task_id、request_id 唯一、plan_id、kind、state、last_event_seq |
| plans | plan_id、workspace_id、project_revision、fingerprint、payload、状态 |
| runs | run_id、project/profile/revision、immutable_plan、实际实例身份 |
| import_journal | preview_id、staging、目标登记、阶段和恢复标记 |

导入提交和保存新项目版本使用数据库事务。项目配置 revision 乐观并发，保存时传 ExpectedRevision；不匹配返回 REVISION_CONFLICT，不覆盖另一窗口的修改。

脚本与配置资源保存为不可变的版本文件：先写临时目录、校验、原子改名，再在事务中登记。事务失败只留下可识别的未引用文件，之后清理本工具创建的残留；不能反向删除原始绑定目录。程序恢复时依据 journal 核对，不猜测已完成。

实现阶段本机数据库版本为3：版本2为版本文件journal增加随机owner_token和匹配目录标记，版本3增加任务载荷、持久事件和资源预约。恢复清理须同时校验归属，journal中的尝试记录本身不授予删除已有目录的权限。旧本机数据库版本1仅在无活动任务/运行且恢复journal为空时事务升级；旧journal没有可靠归属时保留文件并要求人工恢复。便携配置格式仍为1.0。

密钥值不存入便携 JSON、命令参数或普通日志。本机库只存 secret_ref；密文使用当前 Windows 用户的保护存储，迁移后重新配置。普通检测不解密密钥；仅检查必需引用是否具备，实际执行时才在内存中解析。进程输出采用已知敏感值和规则脱敏，不能宣称能自动识别所有未知敏感文本。

## 核心类型与边界

Go 领域类型按功能分文件：domain/project/document.go、requirement.go、unit.go、command.go、path.go；检测在 domain/check，任务在 domain/task，运行在 domain/run，包在 domain/bundle。JSON 字段对应契约，不能用未校验的 map 在页面与进程执行之间直传。

跨模块标识采用字符串别名 ProjectID、WorkspaceID、ProfileID、TaskID、PlanID、RunID、PreviewID。Revision 为正 int32；事件序号 EventCursor 为十进制字符串，避免前端整数精度丢失。

公共 DTO 固定如下，具体完整字段按本文和 Schema 实现：

```go
type ProfileRef struct {
    ProjectID ProjectID
    ProfileID ProfileID
    ExpectedRevision Revision
}
type SubmitPlanRequest struct {
    PlanID PlanID
    Fingerprint string
    RequestID string
}
type TaskView struct {
    ID TaskID
    Kind TaskKind
    State TaskState
    LastEventSeq EventCursor
    Cancelable bool
    Error *AppError
}
type CheckItem struct {
    RequirementID string
    Status CheckStatus
    Evidence []Evidence
    Reasons []Reason
    Repairs []RepairStep
}
type AppError struct {
    Code string
    Message string
    Retryable bool
    Details map[string]string
}
```

DTO 通过 JSON 使用 lowerCamelCase；错误 Details 不携带命令秘密、SQL、明文 DSN 或原始密钥。接口中业务失败使用稳定 Code，界面负责翻译和展示。

CheckStatus 固定为 satisfied、missing、incompatible、binding-required、unknown，分别对应满足元数据、缺失、不兼容、需绑定、未知；satisfied 不代表实际运行验收。checkSummary 为 ready、not-ready 或 unverified。WorkspaceView 返回 workspaceId、name、root、state（ready/rebind-required）、dbSchemaVersion；root仅限本机界面。

领域结构不得依赖 Wails、Docker SDK 或 Windows API。ports 按能力定义 repository、probe、version、process、container、archive、filesystem、runtime_provider；bootstrap 组装适配器，application 使用 ports 执行业务。Wails services 只接受请求、调用用例和映射返回，前端只有 services 层导入生成的绑定。

## 应用接口

每个服务在独立文件中实现；下列是 Go 应用层接口，Wails 包装层映射可绑定参数。context 由调用边界建立，不把 Go context 传给前端。

```go
WorkspaceService.Initialize(ctx context.Context, req InitWorkspaceRequest) (WorkspaceView, error)
WorkspaceService.Open(ctx context.Context, root string) (WorkspaceView, error)
WorkspaceService.ConfirmRelocation(ctx context.Context, id WorkspaceID) (WorkspaceView, error)
ProjectService.Register(ctx context.Context, req RegisterProjectRequest) (ProjectView, error)
ProjectService.Save(ctx context.Context, req SaveProjectRequest) (ProjectView, error)
ProjectService.Get(ctx context.Context, id ProjectID) (ProjectView, error)
ProjectService.List(ctx context.Context, query ProjectQuery) ([]ProjectView, error)
CheckService.Check(ctx context.Context, req CheckRequest) (CheckReport, error)
PlanService.BuildPrepare(ctx context.Context, ref ProfileRef) (OperationPlan, error)
PlanService.BuildStart(ctx context.Context, ref ProfileRef) (OperationPlan, error)
PlanService.BuildStop(ctx context.Context, ref ProfileRef) (OperationPlan, error)
TaskService.Submit(ctx context.Context, req SubmitPlanRequest) (TaskView, error)
TaskService.Get(ctx context.Context, id TaskID) (TaskView, error)
TaskService.Cancel(ctx context.Context, id TaskID) (TaskView, error)
TaskService.Resolve(ctx context.Context, req ResolveTaskRequest) (TaskView, error)
TaskService.Events(ctx context.Context, id TaskID, after EventCursor) ([]Event, error)
BundleService.PreviewImport(ctx context.Context, req ImportPreviewRequest) (ImportPreview, error)
BundleService.PlanImport(ctx context.Context, req CommitImportRequest) (OperationPlan, error)
BundleService.PlanExport(ctx context.Context, req ExportRequest) (OperationPlan, error)
LifecycleService.RequestExit(ctx context.Context) (TaskView, error)
LogService.Read(ctx context.Context, query LogQuery) (LogPage, error)
```

InitWorkspaceRequest 为 root、name。RegisterProjectRequest 为 document、bindings、includedFiles；SaveProjectRequest 增加 ExpectedRevision。CheckRequest 为 ProfileRef 和 enabledFeatureIds。ProjectQuery 为 group、search、offset、limit，limit 1—100。ProjectView 至少返回 document、revision、checkSummary、runSummary；不返回 secret 值。

enabledFeatureIds如提供，须与该revision中已保存profile的featureIds一致；变更可选功能先保存新revision，再检测/准备/启动，避免检测与启动使用两套输入。只读文件指纹覆盖显式声明的输入，不冒充整个Git工作树干净的证明；可能含秘密的配置默认仅检查存在性，未核对的期望hash保持unknown。

命令检查包括所选运行单元的工作目录、绑定执行文件和系统工具存在性。Python静态requires-python参与交集；已知依赖文件缺少足够解释器元数据时报告unknown，不从依赖名称猜最低版本，也不运行setup.py或项目代码探测。

OperationPlan 为 id、kind、workspaceId、ProfileRef（适用时）、fingerprint、steps、requiredBindings、warnings、createdAt。步骤记录 effect、cancelPolicy、输入指纹和影响资源，不能把后端命令解释权交给前端。

ImportPreviewRequest 为 sourcePath。ImportPreview 为 id、bundleIdentity、document、issues、requiredBindings、expiresAt。CommitImportRequest 为 previewId、bindings。ExportRequest 为 ProfileRef、destinationPath。导出明确选择某个 revision，不携带数据库、源码、镜像或本机授权。

CheckReport 为 id、ProfileRef、sourceFingerprint、items、summary、createdAt。Evidence 包含 kind（declaration/metadata/observation/runtime-test/desktop-test）、source、observedAt 和可选版本，导入声明不能制造后两类证据。Reason 为 code、subject、message；RepairStep 为 id、label、effect、automaticSupported。

LogQuery 为 runId、unitId、after、limit（最多 2000）；LogPage 为 entries、next、gap。Event 为 taskId、seq、kind、occurredAt 和受控 payload，订阅可通过 after 恢复；发现缺口重新读取任务/运行快照。

## 任务、并发与计划

TaskState 为 queued、running、waiting-user、cancel-requested、succeeded、failed、cancelled、interrupted。正常转换 queued→running→终态；用户选择必要时进入 waiting-user；取消只在安全检查点生效。异常恢复后未知的在途任务标 interrupted，不自动重放具有副作用的步骤。

同一 requestId 幂等返回原 taskId。首版资源变更任务在工作空间内串行，包括准备、启动、停止和导入登记。只读探测最多并发 4 项，单个工具探测默认 5 秒，总检测默认 30 秒；业务可报告未验证项，不能无限等待。

ResolveTaskRequest 为 taskId、expectedEventSeq、action；action 仅 retry、continue-wait、abort-exit。waiting-user 在安全点释放队列执行位，但保留资源预约，冲突任务不得绕过；Resolve 原子核对事件序号并重新排队同一任务，不能创建第二个退出协调器。retry 重试未完成的正常停止步骤，continue-wait 仅继续等待已经发起的正常停止，不重复发送业务动作。abort-exit 只适用于退出任务：等当前停止调用收敛后取消剩余退出步骤，已停止项目保持停止，恢复新任务准入；不能撤销已经开始的保存。退出任务处于 waiting-user 时继续禁止新变更，只允许该退出任务的 Resolve、状态和日志查询。非退出任务取消后在安全点释放预约。重启后未完成的退出任务标 interrupted，核对实例后重新生成停止计划，不自动继续退出。

Submit 只接受后端已经保存的 PlanID 与指纹。执行前重新检查配置 revision、相关文件、运行时、资源绑定和所选源身份。不同返回 PLAN_STALE，不能执行旧计划或自动改成新步骤。

启动时保存不可变 RunPlan：启动与停止命令、工作目录、脚本字节/哈希、依赖关系、资源获取记录。运行中可以保存未来的新配置，但停止当前运行始终使用启动时的快照，不被新脚本覆盖。秘密只保留引用，运行时内存中解析。

前端每个日志视图最多保留 2000 行、100 ms 批量刷新；本机每个运行日志 10 MiB 轮转、保留 3 段。隐藏窗口暂停装饰动画，但不会取消后台任务或停止日志采集。

## 启动、共享资源与退出

Start 按依赖拓扑顺序执行，启动前完成必需检查和停止策略检查。已有且仅引用的服务不能被失败回滚停止。失败回滚仅处理本次新获取的资源，并且依照快照正常停止；清理失败保留运行记录及具体失败，不宣称全部已恢复。

resourceId 标识逻辑资源，本机 control_grant 是另外的权限。导入不会自动复制接管权；同名、同 UUID 或配置相似均不足以取得结束既有服务的权限。资源控制/共享关系持久保存，启动与停止在同一调度入口内判断使用者。

Docker 启动保留 Compose 文件顺序、显式 project、engine 和选定 service。停止不执行 down -v、remove-orphans 或自动数据库升级。按容器配置的 StopSignal 请求正常停止并等待；超时需要用户明确强制操作，不能利用默认超时隐式 SIGKILL。

新建受管 Compose 栈的 project 名固定为 lph- 加 resourceId 去除连字符的完整小写值，不依赖目录名。引用或显式接管现有栈时，本机资源绑定记录 engine 身份、实际 Compose project、service 集合及配置指纹；接管时单独校验并授权，后续使用原名，不自动重命名或创建另一套。可迁移配置不携带接管授权，目标端重新绑定。每个项目的 bindingKey 独立解析；共享关系只通过 resourceId 和相容的本机资源绑定建立。

Windows 进程保存 PID、创建时间、实际执行路径和本次控制身份，控制前核对，避免 PID 复用。父脚本退出不代表子进程已退出，必须跟踪整个本工具启动范围。终端和进程适配单独封装。

RequestExit 先关闭新任务准入、取消尚未执行的任务，并在安全点取消或等待在途准备；停止当前所有受管理项目，再处理没有其他使用者的受管理公共资源。stop 失败保持核心与界面可用，显示 waiting-user；全部正常停止后才由 bootstrap 结束 Wails。

同一进程在退出协调未终止前再次调用RequestExit返回同一taskId，不重复创建退出任务；窗口或托盘重复点击均遵守此约定。

Qt 托盘程序与机器人通过 [项目控制协议](../../integrations/managed-control-v1.md) 适配。新增原项目接口属于单独集成变更；在它们通过验证以前，不能将三个项目完整支持标成完成。普通关窗、强杀和真实微信自动操作都不替代该协议。

## 导入导出与恢复

采用 [契约](../../contracts/README.md) 中的 64 MiB ZIP、128 MiB 总解压、16 MiB 单文件和 2048 文件限额。所有条目都检查原始路径及规范化后的 Windows 冲突；拒绝符号链接、加密 ZIP、重复项、未列明项和清单自身校验项。实际累计解压字节，manifest.json 不可超 1 MiB。

导入预览只写本工具 imports 目录，不写源项目、不登记、不执行包内代码。Preview 有效期 30 分钟，绑定到 ZIP 内容哈希；正式导入再核对原包。重复 projectId 首版拒绝，不做静默覆盖或自动克隆。

提交前 staged 文件完整校验，通过事务登记；失败恢复 journal，不删除用户原始目录。未知格式版本拒绝并保留原包。导出从固定 revision 生成临时文件，成功校验后改名；资源变化或目录冲突时中止，不留“成功”半包。

SQLite 或工作空间文件的 schema 比当前版本更高时拒绝写入；不猜测降级。恢复逻辑不对被管理业务数据库执行迁移。

## 工程依赖与验证

Go 模块路径选择 github.com/JeropsWang/local-project-hub，Go 构建基线 1.26.1。依赖实际选定后锁 go.mod/go.sum；前端锁 package-lock.json。Wails 使用已选 v3 稳定线，精确补丁在壳集成任务开始时记录。

JSON 验证使用 santhosh-tekuri/jsonschema/v6，禁止输入触发远程加载。Node 范围使用 Masterminds/semver/v3 作为实现基线并与固定 npm semver 夹具对照；不支持的语法返回 unknown，不能当作通过。Python 采用 go-pep440-version，先拒绝未支持的 ===，不把它按其他比较符处理。Go 与 Java 使用各自明确规则，不将所有生态统一套 SemVer。

自动准备的下载源由管理器内置并锁定的供应描述控制，不接受项目 JSON 任意指定安装器。Node 使用官方 Windows ZIP，Go 使用官方 Windows ZIP；Python 使用 Astral python-build-standalone 的固定发布构建，直接解包其 install_only 发行物，不调用会注册全局解释器的安装命令。描述包含精确版本、架构、发布 URL、SHA-256 和许可/来源，缺失则报告不支持自动准备。解包到工作空间后创建项目 venv，不修改系统 PATH、注册表或原项目环境；项目的 .deps 覆盖和硬编码 venv 需要洁净副本/适配检查，不能靠 PATH 掩盖。

工具探测输出 stdout/stderr 合计最多64KiB。运行时供应包另设压缩1GiB、展开4GiB、单文件1GiB、文件数100000限额，与配置ZIP限额分开；解包仍需校验路径、类型与实际字节。相同requestId但请求摘要不同返回REQUEST_CONFLICT。桌面布局以最小960×640窗口及200%缩放检查可用性，具体视觉排版在原型评审时确认。

契约已用现有 Ajv 2020-12 验证器验证结构与样例；这不等同于 Go 应用已构建或业务校验已实现。后续验收覆盖格式、关系、数据事务、并发、路径、ABI、源身份、停止适配及隔离环境运行。

真实项目运行、模型下载、微信桌面操作和原仓库变更按各自边界执行；本轮设计与计划不触发它们。细化后的设计仍需用户评审，然后按 [实施计划入口](../plans/2026-11-05-implementation-index.md) 分阶段落实。

## 官方技术资料

- [JSON Schema Go 验证器](https://pkg.go.dev/github.com/santhosh-tekuri/jsonschema/v6)。
- [Go SemVer 范围](https://github.com/Masterminds/semver) 与 [Python PEP 440 实现](https://github.com/aquasecurity/go-pep440-version)。
- [SQLite Go 驱动](https://pkg.go.dev/modernc.org/sqlite)。
- [Python 独立发行物](https://docs.astral.sh/python-build-standalone/) 与 [uv 的 Windows 解释器注册行为说明](https://docs.astral.sh/uv/concepts/python-versions/)。
