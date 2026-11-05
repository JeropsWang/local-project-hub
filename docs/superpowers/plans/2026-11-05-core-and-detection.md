# Core and Detection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 形成可独立测试的项目登记、工作空间和只读兼容检查核心。

**Architecture:** Schema 结构验证后再作领域关系验证。SQLite 是本机权威记录，探测通过端口执行，检测模式不执行项目脚本。

**Tech Stack:** Go、JSON Schema v6、database/sql + modernc SQLite、生态版本适配器

**Spec:** [工程设计](../specs/2026-11-05-engineering-design.md)、[兼容设计](../specs/2026-11-05-compatibility-design.md)、[配置契约](../../contracts/README.md)。

## Global Constraints

- Go 模块 github.com/JeropsWang/local-project-hub；构建基线 Go 1.26.1；首轮 Windows 11 x64。
- 格式版本 1.0；未知版本拒绝；配置、环境满足和实际运行验证分开。
- 领域与应用不依赖 Wails、Docker SDK、Windows API；入口只组装；源码注释并格式化。
- 原项目和真实业务数据不修改；测试使用虚构数据、临时目录与受控进程。
- 不把 audit.local、密钥、真实项目登记和日志提交 Git。

## Review Focus

- 重复 JSON 键与未知字段不能被解析器静默覆盖（Task 1）。
- 多个项目使用同名 source 绑定互不覆盖，保存 revision 冲突不丢修改（Task 2）。
- 工具挂起、输出超长和工作空间移盘不会误报可启动（Task 3）。
- npm 奇偶主版本、PEP 440 未支持语法不能用宽松比较放行（Task 4）。
- 声明写“已验证”或仅有 CI 配置不能制造本机运行证据（Task 4）。

## 文件与类型归属

Task 1 定义 domain/project 与 domain/check 的纯类型；Task 2 定义 application/contracts.go 中工程设计列出的 DTO。后续计划复用这些类型，不另起同名结构。应用 CheckItem 为领域 check.Item 的类型别名。WorkspaceView 含 workspaceId、name、root、state（ready/rebind-required）、dbSchemaVersion；本机 root 不进入导出。CheckStatus 使用工程设计的 satisfied/missing/incompatible/binding-required/unknown，Reason/Evidence/RepairStep 字段按工程设计固定。

### Task 1: 契约解码与依赖图

**Files:**
- Create: `go.mod`, `go.sum`, `internal/domain/project/{document,requirement,unit,command,path,validate}.go`
- Create: `internal/domain/check/model.go`, `internal/adapters/contract/decoder.go`
- Create: `internal/adapters/contract/schemas/`（复制现有三份 Schema，测试校验与 docs 源文件相同）
- Test: `internal/adapters/contract/decoder_test.go`, `internal/domain/project/validate_test.go`

**Interfaces:**
- Consumes: `docs/contracts/*.schema.json` 与示例字节。
- Produces: `contract.DecodeProject(data []byte) (project.Document, error)`；`project.Validate(doc Document) error`；`project.Order(doc Document, profileID string) ([]string, error)`。

以下是测试名及关键断言片段；夹具在列出的测试文件内构建，输入使用仓库脱敏示例，不依赖真实工程。

- [ ] **Step 1: 写失败测试**

```go
func TestDecodeRejectsDuplicateKeysAndUnknownVersion(t *testing.T) {
    // 分别喂入重复 projectId、schemaVersion=9.0、顶层 controlGrant。
    if err == nil { t.Fatal("invalid document accepted") }
}
func TestOrderRejectsCycleAndMissingStop(t *testing.T) {
    // a->b->a 返回 DEPENDENCY_CYCLE；缺失 stop 返回格式错误。
    if code != "DEPENDENCY_CYCLE" { t.Fatal(code) }
}
func TestOrderExpandsDependencies(t *testing.T) {
    if strings.Join(order, ",") != "database,api" { t.Fatal(order) }
}
```

- [ ] **Step 2: 验证红灯**

Run: `go test ./internal/adapters/contract ./internal/domain/project`
Expected: FAIL，缺少下述接口或行为；不能因测试环境损坏而把失败当成有效红灯。

- [ ] **Step 3: 实现本任务接口**

创建 Go 模块并锁定验证依赖。解码前检查重复键与大小，嵌入本地 Schema，拒绝远程引用。Validate 检查引用、保留环境变量、Windows 环境名冲突、128 单元/32 profile/512 字符范围限额、依赖与 feature 关系；Order 稳定拓扑排序。manual stop 可登记，自动启动资格由后续规划器拒绝。给每个公开类型/规则加意图与失败行为注释。

- [ ] **Step 4: 验证绿灯**

Run: `go test ./internal/adapters/contract ./internal/domain/project`
Expected: PASS；格式化相关 Go 文件并运行 `go vet ./...`。

- [ ] **Step 5: 提交**

显式暂存本任务 Files 中的实现与测试，检查无本机数据后执行 `git commit -m "feat: validate portable project contracts"`。

### Task 2: 事务工作空间与不可变配置版本

**Files:**
- Create: `internal/application/{contracts,workspace,project}.go`, `internal/ports/repository.go`
- Create: `internal/adapters/persistence/{sqlite,migrations,revisions}.go`
- Create: `internal/adapters/filesystem/{workspace,lock_windows,revision_files}.go`
- Test: `internal/application/project_test.go`, `internal/adapters/persistence/recovery_test.go`, `internal/adapters/filesystem/workspace_test.go`

**Interfaces:**
- Consumes: Task 1 `DecodeProject`, `Validate`。
- Produces: 工程设计的 `WorkspaceService.Initialize/Open` 与 `ProjectService.Register/Save/Get/List` 原签名；`repository.Store` 暴露事务读写项目、绑定与 revision 能力，由 application 定义最小操作。

以下是测试名及关键断言片段；夹具在列出的测试文件内构建，输入使用仓库脱敏示例，不依赖真实工程。

- [ ] **Step 1: 写失败测试**

```go
func TestSameBindingKeyIsScopedByProject(t *testing.T) {
    if projectABinding == projectBBinding { t.Fatal("bindings leaked across projects") }
}
func TestSaveRevisionConflictAndCrashRecovery(t *testing.T) {
    if code != "REVISION_CONFLICT" { t.Fatal(code) }
    if currentRevision != 2 || recoveredOriginalHash != originalHash { t.Fatal("lost revision") }
}
func TestRejectNewerDatabaseAndDuplicateOpen(t *testing.T) {
    if code != "WORKSPACE_VERSION_UNSUPPORTED" { t.Fatal(code) }
    // 第二进程持锁打开应为 WORKSPACE_BUSY；原目录内容保持不变。
}
```

- [ ] **Step 2: 验证红灯**

Run: `go test ./internal/application ./internal/adapters/persistence ./internal/adapters/filesystem`
Expected: FAIL，缺少下述接口或行为；不能因测试环境损坏而把失败当成有效红灯。

- [ ] **Step 3: 实现本任务接口**

按工程设计建表与事务；锁定 SQLite 依赖。绑定按 workspace/project/key 定位。文件先暂存校验再改名，数据库事务登记；注入故障验证 journal 恢复与孤儿文件清理只作用于自建目录。拒绝网络共享、新版数据库与重复打开。root 变化进入 rebind-required；有活动运行/变更时切换返回 WORKSPACE_BUSY。只读检查不得创建或迁移被管理项目数据库。

- [ ] **Step 4: 验证绿灯**

Run: `go test ./internal/application ./internal/adapters/persistence ./internal/adapters/filesystem`
Expected: PASS；格式化相关 Go 文件并运行 `go vet ./...`。

- [ ] **Step 5: 提交**

显式暂存本任务 Files 中的实现与测试，检查无本机数据后执行 `git commit -m "feat: persist workspace and project revisions"`。

### Task 3: 有界只读环境发现

**Files:**
- Create: `internal/ports/{probe,filesystem,version}.go`
- Create: `internal/adapters/environment/{discovery,tools,metadata}.go`
- Create: `internal/application/check.go`, `internal/domain/check/report.go`
- Test: `internal/adapters/environment/discovery_test.go`, `internal/application/check_test.go`

**Interfaces:**
- Consumes: `ProjectService.Get(ctx, ProjectID) (ProjectView, error)` 与项目绑定读取端口。
- Produces: `probe.Discover(ctx context.Context, req Request) (Observation, error)`；`CheckService.Check(ctx context.Context, req CheckRequest) (CheckReport, error)`。Request 仅由后端生成，含 tool、args、cwd、timeout；Observation 含定位、版本、架构、时间、退出信息。

以下是测试名及关键断言片段；夹具在列出的测试文件内构建，输入使用仓库脱敏示例，不依赖真实工程。

- [ ] **Step 1: 写失败测试**

```go
func TestDetectionIsReadOnlyAndBounded(t *testing.T) {
    if installs != 0 || starts != 0 || projectWrites != 0 { t.Fatal("detection has effects") }
    if maxConcurrent > 4 || elapsed > 30*time.Second+time.Second { t.Fatal("probe budget") }
}
func TestTimeoutOutputCapAndRelocatedRoot(t *testing.T) {
    if timeoutCode != "PROBE_TIMEOUT" || outputBytes > 64*1024 { t.Fatal("unbounded probe") }
    if relocatedSummary == "ready" { t.Fatal("stale binding accepted") }
}
```

- [ ] **Step 2: 验证红灯**

Run: `go test ./internal/adapters/environment ./internal/application`
Expected: FAIL，缺少下述接口或行为；不能因测试环境损坏而把失败当成有效红灯。

- [ ] **Step 3: 实现本任务接口**

实现固定工具的 --version 等只读命令与静态元数据读取，不执行 npm lifecycle、setup 或包内脚本；不得加载真实密钥。并发4、单项5秒、总30秒，单探测 stdout/stderr 合计64KiB，超限返回明确原因。区分 Node/npm、Go/toolchain、Python/ABI、Java、Docker/Compose、PG/MySQL/Redis、交互会话；缺工具与 Docker 未启动记录 unknown/missing，不补装。metadata 读取范围受绑定根和文件限额约束。

- [ ] **Step 4: 验证绿灯**

Run: `go test ./internal/adapters/environment ./internal/application`
Expected: PASS；格式化相关 Go 文件并运行 `go vet ./...`。

- [ ] **Step 5: 提交**

显式暂存本任务 Files 中的实现与测试，检查无本机数据后执行 `git commit -m "feat: report bounded read-only environment checks"`。

### Task 4: 生态约束与检测报告验收

**Files:**
- Create: `internal/adapters/version/{npm,pep440,golang,java,exact}.go`
- Create: `tests/fixtures/compatibility/{npm,python,go,java,evidence}.json`
- Create: `tests/integration/detection_test.go`
- Test: `internal/adapters/version/evaluate_test.go`, `internal/application/check_test.go`

**Interfaces:**
- Consumes: Task 3 `Observation` 与 `CheckService.Check`。
- Produces: `version.Evaluate(req project.Requirement, observation probe.Observation) (check.Item, error)`；不支持语法返回状态 unknown 和稳定原因，不返回满足。

以下是测试名及关键断言片段；夹具在列出的测试文件内构建，输入使用仓库脱敏示例，不依赖真实工程。

- [ ] **Step 1: 写失败测试**

```go
func TestLockedNodeEngines(t *testing.T) {
    // 固定夹具为本次审查的脱敏范围集合，非实时 npm 查询。
    if !passes["22.19.0"] || passes["23.0.0"] || !passes["24.0.0"] || passes["25.0.0"] { t.Fatal(passes) }
}
func TestUnsupportedPythonAndABI(t *testing.T) {
    if exactArbitraryStatus != "unknown" || cp313On312Status == "satisfied" { t.Fatal("unsafe Python match") }
}
func TestEvidenceDoesNotPromoteCIToRuntime(t *testing.T) {
    if runtimeVerified || acceptedImportedProof { t.Fatal("fabricated evidence") }
}
```

- [ ] **Step 2: 验证红灯**

Run: `go test ./internal/adapters/version ./internal/application ./tests/integration`
Expected: FAIL，缺少下述接口或行为；不能因测试环境损坏而把失败当成有效红灯。

- [ ] **Step 3: 实现本任务接口**

锁 Masterminds semver 与 PEP440 库，npm 夹具使用固定 npm semver 生成的期望值，覆盖 OR/预发布/空白范围；=== 不支持明确 unknown。Go 解析 go.mod/go.work/toolchain，Java 区分工具链、release 与 JVM。用合成夹具还原三项目的已知约束：两套 Python、独立 PG/Redis 主版本、可选重型功能、源与运行归属不一致。汇总状态仍不能代表桌面运行认证。最终 go test ./... 与 go vet ./... 通过后记录证据。

- [ ] **Step 4: 验证绿灯**

Run: `go test ./internal/adapters/version ./internal/application ./tests/integration`
Expected: PASS；格式化相关 Go 文件并运行 `go vet ./...`。

- [ ] **Step 5: 提交**

显式暂存本任务 Files 中的实现与测试，检查无本机数据后执行 `git commit -m "feat: evaluate ecosystem compatibility with evidence"`。

## 独立交付与后续

本计划完成后可以通过 Go 测试/调用检查器验证登记与报告，不需要 Wails、下载环境或启动真实项目。交付报告列明数据/版本规则覆盖；不宣称三个项目生命周期已完成。下一步进入运行计划或配置迁移计划。
