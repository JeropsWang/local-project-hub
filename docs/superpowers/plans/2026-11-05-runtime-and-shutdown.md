# Runtime and Safe Shutdown Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在工作空间中按计划准备环境、启动完整依赖并安全停止受管理资源。

**Architecture:** 依赖核心计划的登记与检查接口。所有变更进入同一任务队列，进程/Docker/运行时供应者只在适配层实现；每次运行保留不可变启停快照。

**Tech Stack:** Go、Windows 命名管道/进程 API、Docker Engine/Compose、固定供应包

**Spec:** [工程设计](../specs/2026-11-05-engineering-design.md)、[兼容设计](../specs/2026-11-05-compatibility-design.md)、[配置契约](../../contracts/README.md)。

## Global Constraints

- Go 模块 github.com/JeropsWang/local-project-hub；构建基线 Go 1.26.1；首轮 Windows 11 x64。
- 格式版本 1.0；未知版本拒绝；配置、环境满足和实际运行验证分开。
- 领域与应用不依赖 Wails、Docker SDK、Windows API；入口只组装；源码注释并格式化。
- 原项目和真实业务数据不修改；测试使用虚构数据、临时目录与受控进程。
- 不把 audit.local、密钥、真实项目登记和日志提交 Git。

## Review Focus

- 双击按钮、计划过期和重启恢复不得重复执行副作用（Task 1）。
- 供应包损坏、环境取消和硬编码 venv 不得污染原项目（Task 2）。
- PID 复用、父进程先退出、保存 ACK 后崩溃不得误判安全停止（Task 3）。
- 共享资源最后一个使用者之前不能停止；仅引用资源不能被回滚清理（Task 4/5）。
- 退出停止超时后可等待、重试或中止退出，不发生队列死锁（Task 5）。

## 前置与公共接口

先完成核心计划。补充 domain/task、domain/run 的 DTO 与端口；所有应用接口沿用工程设计的原签名。OperationPlan 由后端保存，前端不能提交任意命令。TaskView/Event/EventCursor 字段依工程设计；ResolveTaskRequest 为 taskId、expectedEventSeq、action。

### Task 1: 持久任务、执行计划与幂等恢复

**Files:**
- Create: `internal/domain/{task,run}/model.go`, `internal/application/{plan,task}.go`
- Create: `internal/ports/{task_repository,clock}.go`, `internal/adapters/persistence/tasks.go`
- Test: `internal/application/task_test.go`, `internal/adapters/persistence/tasks_test.go`

**Interfaces:**
- Consumes: 核心的 `ProjectService.Get`、检查和工作空间事务。
- Produces: `PlanService.BuildPrepare/BuildStart/BuildStop(ctx context.Context, ref ProfileRef) (OperationPlan, error)`；`TaskService.Submit/Get/Cancel/Resolve`（工程设计原签名）；`TaskService.Events(ctx context.Context, id TaskID, after EventCursor) ([]Event, error)`。

以下是测试名及关键断言片段；夹具在列出的测试文件内构建，输入使用仓库脱敏示例，不依赖真实工程。

- [x] **Step 1: 写失败测试**

```go
func TestSubmitIdempotentAndRejectsStalePlan(t *testing.T) {
    if first.ID != second.ID || effectCount != 1 { t.Fatal("duplicate execution") }
    if changedInputCode != "PLAN_STALE" || staleEffects != 0 { t.Fatal("stale plan ran") }
}
func TestRecoveryAndSafeCancellation(t *testing.T) {
    if recovered.State != "interrupted" || replayCount != 0 { t.Fatal("unsafe replay") }
    if waitPolicyInterruptedBeforeCheckpoint { t.Fatal("unsafe cancellation") }
}
```

- [x] **Step 2: 验证红灯**

Run: `go test ./internal/application ./internal/adapters/persistence`
Expected: FAIL，缺少下述接口或行为；不能因测试环境损坏而把失败当成有效红灯。

- [x] **Step 3: 实现本任务接口**

实现服务端计划和指纹核对，输入含 revision、文件、工具、绑定和源身份。requestId 唯一并绑定请求摘要，相同 ID 不同请求返回 REQUEST_CONFLICT。变更串行执行；checkpoint/wait 取消遵守安全点；事件落库后发布，十进制游标可补读。恢复标 interrupted，保留实例记录供核对，不重放安装/迁移/发送。测试使用 fake clock/executor，不真实运行项目。

- [x] **Step 4: 验证绿灯**

Run: `go test ./internal/application ./internal/adapters/persistence`
Expected: PASS；格式化相关 Go 文件并运行 `go vet ./...`。

- [x] **Step 5: 提交**

显式暂存本任务 Files 中的实现与测试，检查无本机数据后执行 `git commit -m "feat: persist idempotent operation plans and tasks"`。

### Task 2: 独立运行时与可取消准备

**Files:**
- Create: `internal/ports/runtime_provider.go`, `internal/adapters/environment/{catalog,download,extract,prepare}.go`
- Create: `internal/adapters/environment/catalog.json`, `internal/application/prepare.go`
- Test: `internal/adapters/environment/prepare_test.go`, `tests/integration/prepare_test.go`

**Interfaces:**
- Consumes: `PlanService.BuildPrepare`、TaskService 与 profile requirement。
- Produces: `RuntimeProvider.Plan(ctx context.Context, req RuntimeRequest) (RuntimeInstallPlan, error)`；`RuntimeProvider.Install(ctx context.Context, plan RuntimeInstallPlan) (RuntimeLocation, error)`；`PrepareExecutor.Execute(ctx context.Context, plan OperationPlan) error`。

以下是测试名及关键断言片段；夹具在列出的测试文件内构建，输入使用仓库脱敏示例，不依赖真实工程。

- [x] **Step 1: 写失败测试**

```go
func TestCorruptDownloadAndCancellationLeaveOriginalUntouched(t *testing.T) {
    if corruptCode != "ARTIFACT_HASH_MISMATCH" || activeEnvChanged { t.Fatal("bad install published") }
    if globalPATHChanged || registryWrites != 0 || originalProjectWrites != 0 { t.Fatal("escaped workspace") }
}
func TestTwoPythonABIsAndHardcodedEnvironment(t *testing.T) {
    if py312Path == py313Path { t.Fatal("ABI environments merged") }
    if hardcodedVenvCode != "SOURCE_COPY_REQUIRED" { t.Fatal("pretended PATH was enough") }
}
```

- [x] **Step 2: 验证红灯**

Run: `go test ./internal/adapters/environment ./tests/integration`
Expected: FAIL，缺少下述接口或行为；不能因测试环境损坏而把失败当成有效红灯。

- [x] **Step 3: 实现本任务接口**

实现内置供应描述（精确 URL/版本/架构/SHA-256/来源许可），Node/Go 官方 ZIP，Python 固定 Astral standalone install_only 构建。实现时从官方发布核对真实制品与哈希后锁定，不填猜测值。下载临时路径校验后原子发布，实际字节限额、路径/链接检查复用受限解包模块；运行时包限额单独为压缩1GiB/展开4GiB/单文件1GiB/文件数100000，配置ZIP限额不用于SDK。缓存/venv/npm/Go缓存均入工作空间。依赖恢复只在明确受管理的洁净源码副本，按锁文件执行；脚本副作用分类展示，不运行名为 NoStart 就假定无启动的脚本。typing 的 .deps 与 Bot 固定 venv 路径必须检查，不能覆盖原目录。Java/MySQL 返回手工步骤；可选模型不隐式下载。

- [x] **Step 4: 验证绿灯**

Run: `go test ./internal/adapters/environment ./tests/integration`
Expected: PASS；格式化相关 Go 文件并运行 `go vet ./...`。

- [x] **Step 5: 提交**

显式暂存本任务 Files 中的实现与测试，检查无本机数据后执行 `git commit -m "feat: prepare pinned workspace runtimes"`。

### Task 3: Windows 进程控制与协议客户端

**Files:**
- Create: `internal/ports/process.go`, `internal/adapters/process/{launch_windows,identity_windows,children_windows,control_client_windows}.go`
- Create: `internal/adapters/process/{logs,redact}.go`, `tests/helpers/controlapp/main.go`
- Test: `internal/adapters/process/control_windows_test.go`, `tests/integration/process_windows_test.go`

**Interfaces:**
- Consumes: Task 1 不可变 RunPlan；[协议 v1](../../integrations/managed-control-v1.md)。
- Produces: `ProcessAdapter.Start(ctx context.Context, spec ProcessSpec) (InstanceIdentity, error)`；`Stop(ctx context.Context, instance InstanceIdentity, spec StopSpec) (StopResult, error)`；`Inspect(ctx context.Context, instance InstanceIdentity) (InstanceState, error)`。

以下是测试名及关键断言片段；夹具在列出的测试文件内构建，输入使用仓库脱敏示例，不依赖真实工程。

- [ ] **Step 1: 写失败测试**

```go
func TestPIDReuseAndParentExitAreNotCompletion(t *testing.T) {
    if reusedPIDStopCalls != 0 || markedStoppedWithChildAlive { t.Fatal("wrong instance controlled") }
}
func TestControlRejectsWrongIdentityAndAckThenCrash(t *testing.T) {
    if unauthorizedAccepted || crashResult.Graceful { t.Fatal("false graceful shutdown") }
}
func TestStopUsesOriginalScriptAndRedactsLogs(t *testing.T) {
    if usedScriptHash != startSnapshotHash || strings.Contains(logText, secret) { t.Fatal("snapshot or secret failure") }
}
```

- [ ] **Step 2: 验证红灯**

Run: `go test ./internal/adapters/process ./tests/integration`
Expected: FAIL，缺少下述接口或行为；不能因测试环境损坏而把失败当成有效红灯。

- [ ] **Step 3: 实现本任务接口**

按工程设计保留 PID/创建时间/路径/控制身份。对子进程采用 Windows job/成员核对，禁止 KILL_ON_JOB_CLOSE 冒充正常退出；不能因父 shell 结束丢掉子树。启动前解析 executable/cwd/args，禁止把参数拼接为 shell 源码。仅显式脚本适配才调用相应 shell。实现当前用户 ACL 管道客户端与临时模拟服务；token 运行期受保护保存，不可导出。对 status/accepted/completed/stopped 与实际退出组合判断。日志单运行10MiB×3，已知秘密脱敏、UI分页最多2000；超过期限返回未完成，不隐式 TerminateProcess。

- [ ] **Step 4: 验证绿灯**

Run: `go test ./internal/adapters/process ./tests/integration`
Expected: PASS；格式化相关 Go 文件并运行 `go vet ./...`。

- [ ] **Step 5: 提交**

显式暂存本任务 Files 中的实现与测试，检查无本机数据后执行 `git commit -m "feat: manage Windows process lifecycles safely"`。

### Task 4: Docker 归属与正常停止

**Files:**
- Create: `internal/ports/container.go`, `internal/adapters/docker/{inventory,compose,stop}.go`
- Create: `internal/application/resources.go`, `internal/adapters/persistence/resources.go`
- Test: `internal/adapters/docker/compose_test.go`, `internal/application/resources_test.go`

**Interfaces:**
- Consumes: 领域 ComposeUnit、resourceId 与本机 control_grant。
- Produces: `ContainerAdapter.Inventory(ctx context.Context, engine string) ([]ContainerObservation, error)`；`Start(ctx context.Context, spec ComposeSpec) ([]InstanceIdentity, error)`；`Stop(ctx context.Context, instance InstanceIdentity) (StopResult, error)`。

以下是测试名及关键断言片段；夹具在列出的测试文件内构建，输入使用仓库脱敏示例，不依赖真实工程。

- [ ] **Step 1: 写失败测试**

```go
func TestComposeIdentityAndFileOrder(t *testing.T) {
    if gotProject != "lph-22222222222242228222222222222222" { t.Fatal(gotProject) }
    if strings.Join(files, ",") != "base.yml,override.yml" { t.Fatal(files) }
}
func TestExternalResourcesAndStopTimeout(t *testing.T) {
    if referenceStopCalls != 0 || volumeDeletes != 0 || forceKillCalls != 0 { t.Fatal("unsafe Docker effect") }
    if timeoutResult.Graceful { t.Fatal("timeout reported stopped") }
}
```

- [ ] **Step 2: 验证红灯**

Run: `go test ./internal/adapters/docker ./internal/application`
Expected: FAIL，缺少下述接口或行为；不能因测试环境损坏而把失败当成有效红灯。

- [ ] **Step 3: 实现本任务接口**

Inventory 读取引擎身份/Compose标签/实例状态，展示待归属资源，不凭名称接管。新建project按工程设计推导；既有栈必须校验本机绑定、源/运行目录差异、服务与配置指纹。固定命令参数保留 Compose文件顺序，配置解析不展开或记录秘密。使用引擎配置 StopSignal 发正常信号并等待，不能调用会到时SIGKILL的默认stop行为。禁止 down -v、remove-orphans 和数据升级。共享资源通过显式授权及引用计数处理，UUID匹配不是授权。

- [ ] **Step 4: 验证绿灯**

Run: `go test ./internal/adapters/docker ./internal/application`
Expected: PASS；格式化相关 Go 文件并运行 `go vet ./...`。

- [ ] **Step 5: 提交**

显式暂存本任务 Files 中的实现与测试，检查无本机数据后执行 `git commit -m "feat: track Docker ownership and graceful stops"`。

### Task 5: 完整启动、失败回滚与全局退出

**Files:**
- Create: `internal/application/{start,stop,lifecycle}.go`, `internal/domain/run/snapshot.go`
- Create: `internal/adapters/persistence/runs.go`
- Test: `internal/application/lifecycle_test.go`, `tests/integration/lifecycle_windows_test.go`

**Interfaces:**
- Consumes: 前4任务 Task/Process/Container/Runtime 接口及持久资源关系。
- Produces: `LifecycleService.RequestExit(ctx context.Context) (TaskView, error)`；Start/Stop 计划的执行器；TaskService.Resolve 实现 retry/continue-wait/abort-exit。

以下是测试名及关键断言片段；夹具在列出的测试文件内构建，输入使用仓库脱敏示例，不依赖真实工程。

- [ ] **Step 1: 写失败测试**

```go
func TestRollbackOnlyNewAcquisitionsAndStopSharedLast(t *testing.T) {
    if preexistingStops != 0 || sharedStoppedWithUsers != 0 { t.Fatal("wrong resource released") }
    if strings.Join(stopOrder, ",") != "api,database" { t.Fatal(stopOrder) }
}
func TestQuitFailureCanResumeOrAbortWithoutDeadlock(t *testing.T) {
    if quitCalledBeforeAllGraceful || admittedNewStartDuringExit { t.Fatal("unsafe exit") }
    if repeatedExitTaskID != exitTaskID { t.Fatal("duplicate exit coordinator") }
    // waiting-user 不占执行位，Resolve 同一任务继续；旧游标返回 TASK_CONFLICT。
    if retryTaskID != exitTaskID || !admissionRestoredAfterAbort { t.Fatal("resume deadlock") }
}
func TestEditedConfigCannotChangeRunningStop(t *testing.T) {
    if stoppedRevision != startedRevision { t.Fatal("mutable run plan") }
}
```

- [ ] **Step 2: 验证红灯**

Run: `go test ./internal/application ./tests/integration`
Expected: FAIL，缺少下述接口或行为；不能因测试环境损坏而把失败当成有效红灯。

- [ ] **Step 3: 实现本任务接口**

启动前核对环境、可验证停止策略与授权；拓扑启动，失败只回滚本次新获取资源。RunPlan冻结脚本字节/指纹与启停参数。退出关闭准入、取消排队任务、在安全点等待准备，倒序停止全部受管理运行和无人引用的受管公共资源。失败 waiting-user 保留运行；Resolve按设计恢复，abort-exit不重新启动已停止项目。全部成功才发允许退出事件。完整跑 go test ./...、go vet ./...；Windows合成进程可在临时目录真实启停，Docker默认用fake，另有明确 opt-in 临时专用栈测试，禁止操作用户已有容器。

- [ ] **Step 4: 验证绿灯**

Run: `go test ./internal/application ./tests/integration`
Expected: PASS；格式化相关 Go 文件并运行 `go vet ./...`。

- [ ] **Step 5: 提交**

显式暂存本任务 Files 中的实现与测试，检查无本机数据后执行 `git commit -m "feat: coordinate full startup and safe manager exit"`。

## 集成门槛

本计划只建立管理器端和模拟项目，不能代替原项目适配。typing-tracker 与 WeChatBot 的服务端变更、原项目构建和隔离验收列在桌面计划中，须独立可评审；未完成不得对外声称三个项目完整接入。
