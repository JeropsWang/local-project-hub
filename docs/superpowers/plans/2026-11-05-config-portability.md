# Configuration Portability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 提供可校验、可预览、可恢复的项目配置导出与导入。

**Architecture:** 依赖核心的配置快照与事务接口，通过运行计划的任务入口提交变更。包只含配置资源；源目录、SDK、模型、业务数据、密钥和接管授权在目标端重新绑定。

**Tech Stack:** Go archive/zip、SHA-256、JSON Schema、本机 SQLite 事务

**Spec:** [工程设计](../specs/2026-11-05-engineering-design.md)、[兼容设计](../specs/2026-11-05-compatibility-design.md)、[配置契约](../../contracts/README.md)。

## Global Constraints

- Go 模块 github.com/JeropsWang/local-project-hub；构建基线 Go 1.26.1；首轮 Windows 11 x64。
- 格式版本 1.0；未知版本拒绝；配置、环境满足和实际运行验证分开。
- 领域与应用不依赖 Wails、Docker SDK、Windows API；入口只组装；源码注释并格式化。
- 原项目和真实业务数据不修改；测试使用虚构数据、临时目录与受控进程。
- 不把 audit.local、密钥、真实项目登记和日志提交 Git。

## Review Focus

- ZIP 路径穿越、ADS、设备名、大小写/Unicode别名及重解析点必须拒绝（Task 2）。
- 实际解压体积超过头部声明、重复项和未列明文件不能绕过清单（Task 2）。
- Compose 的嵌套引用或脚本的动态文件访问不能被当成已完整收集（Task 1）。
- 预览后原包被替换、重复项目 ID 或中断提交不能覆盖现有项目（Task 3）。
- 导入的环境证据和资源 ID 不得变成新电脑的启动资格或接管权（Task 3）。

## 精确限额

配置 ZIP 64 MiB；总解压 128 MiB；单文件 16 MiB；清单 1 MiB；文件数不含清单最多2048。Preview 有效期30分钟，绑定整个 ZIP 内容哈希。限额按实际字节执行，边界值允许，超过1字节拒绝。未知1.0以外格式拒绝。示例 minManagerVersion=0.1.0 表示计划的首版读取要求，不是已有版本发布声明。

### Task 1: 固定版本导出与文件闭包

**Files:**
- Create: `internal/domain/bundle/{manifest,closure}.go`, `internal/application/export.go`
- Create: `internal/ports/archive.go`, `internal/adapters/archive/{writer,compose_refs}.go`
- Test: `internal/adapters/archive/export_test.go`, `internal/domain/bundle/closure_test.go`

**Interfaces:**
- Consumes: `ProjectService.Get`、固定revision及不可变资源文件读取；Task/OperationPlan类型。
- Produces: `BundleService.PlanExport(ctx context.Context, req ExportRequest) (OperationPlan, error)`；`bundle.Collect(doc project.Document, reader FileReader) (Closure, error)`；`ArchiveWriter.Write(ctx context.Context, input ExportInput) (ExportResult, error)`。

以下是测试名及关键断言片段；夹具在列出的测试文件内构建，输入使用仓库脱敏示例，不依赖真实工程。

- [ ] **Step 1: 写失败测试**

```go
func TestMissingOrChangedIncludedFileBlocksExport(t *testing.T) {
    if missingCode != "BUNDLE_FILE_MISSING" || changedCode != "REVISION_FILE_CHANGED" { t.Fatal("incomplete export") }
    if successfulDestinationExists { t.Fatal("half package published") }
}
func TestComposeClosureAndExternalBindings(t *testing.T) {
    if !declaredNestedConfig || !listedExternalSecret || copiedSourceOrData { t.Fatal("wrong file closure") }
}
func TestManifestHashesOriginalBytes(t *testing.T) {
    if recordedHash != actualSHA256 || manifestListsItself { t.Fatal("invalid manifest") }
}
```

- [ ] **Step 2: 验证红灯**

Run: `go test ./internal/domain/bundle ./internal/adapters/archive`
Expected: FAIL，缺少下述接口或行为；不能因测试环境损坏而把失败当成有效红灯。

- [ ] **Step 3: 实现本任务接口**

使用固定revision收集 included 资源并校验原始字节，按清单写临时ZIP，重新校验后原子改名；目标已存在不覆盖。解析Compose env_file/configs/secrets/bind mounts/include/extends/build上下文，显式归类随包/外部/不支持；不执行compose或shell来穷举文件。动态脚本依赖必须有声明并在报告中标注未静态验证。相对引用重写只对明确支持的格式执行，否则阻止导出并说明。环境模板不得收集真实.env；密钥只列绑定键。导出不包含本机Locator/PID/授权/验证状态。

- [ ] **Step 4: 验证绿灯**

Run: `go test ./internal/domain/bundle ./internal/adapters/archive`
Expected: PASS；格式化相关 Go 文件并运行 `go vet ./...`。

- [ ] **Step 5: 提交**

显式暂存本任务 Files 中的实现与测试，检查无本机数据后执行 `git commit -m "feat: export complete versioned configuration bundles"`。

### Task 2: 受限解包与无副作用预览

**Files:**
- Create: `internal/adapters/archive/{reader,paths_windows,limits}.go`
- Create: `internal/application/import_preview.go`, `internal/domain/bundle/preview.go`
- Test: `internal/adapters/archive/reader_test.go`, `internal/adapters/archive/paths_windows_test.go`, `internal/application/import_preview_test.go`

**Interfaces:**
- Consumes: 核心 DecodeProject/Validate 与 Task 1 Manifest/Closure。
- Produces: `BundleService.PreviewImport(ctx context.Context, req ImportPreviewRequest) (ImportPreview, error)`；`ArchiveReader.Validate(ctx context.Context, sourcePath string, stagingRoot string) (ValidatedBundle, error)`。

以下是测试名及关键断言片段；夹具在列出的测试文件内构建，输入使用仓库脱敏示例，不依赖真实工程。

- [ ] **Step 1: 写失败测试**

```go
func TestRejectWindowsEscapeAndAliases(t *testing.T) {
    // ../x, C:/x, file:stream, CON, x./x, A/a 重复、Unicode别名、链接逐项覆盖。
    if acceptedInvalidPath || outsideStagingWrites != 0 { t.Fatal("archive escape") }
}
func TestActualLimitsAndInventory(t *testing.T) {
    if acceptsFile16MiBPlusOne || accepts2049Files || acceptsUnlisted || acceptsEncrypted { t.Fatal("limit bypass") }
}
func TestPreviewCannotExecuteOrRegister(t *testing.T) {
    if executions != 0 || projectCountChanged || originalFilesChanged { t.Fatal("preview effects") }
}
```

- [ ] **Step 2: 验证红灯**

Run: `go test ./internal/adapters/archive ./internal/application`
Expected: FAIL，缺少下述接口或行为；不能因测试环境损坏而把失败当成有效红灯。

- [ ] **Step 3: 实现本任务接口**

解压前校验ZIP/manifest界限，再逐条验证原始路径和Windows规范化结果、文件类型、重复与清单闭包；以实际读取累计字节，SHA256/大小均匹配。拒绝加密、symlink和非普通文件；staging父目录不能被重解析点替换，打开/写入边界须复查。预览只写本工具imports目录并存内容身份与到期时间；报告缺少外部绑定可登记但不可启动。使用固定夹具覆盖64/128MiB边界、16MiB+1、2049项、错误hash、缺文件、未知版本；大体积夹具流式生成，避免提交巨型测试包。

- [ ] **Step 4: 验证绿灯**

Run: `go test ./internal/adapters/archive ./internal/application`
Expected: PASS；格式化相关 Go 文件并运行 `go vet ./...`。

- [ ] **Step 5: 提交**

显式暂存本任务 Files 中的实现与测试，检查无本机数据后执行 `git commit -m "feat: preview and validate bounded configuration imports"`。

### Task 3: 事务导入、故障恢复与往返验收

**Files:**
- Create: `internal/application/import_commit.go`, `internal/adapters/persistence/import_journal.go`
- Create: `tests/integration/portability_test.go`
- Test: `internal/application/import_commit_test.go`, `internal/adapters/persistence/import_journal_test.go`

**Interfaces:**
- Consumes: `PreviewImport`、核心项目事务；运行计划 `TaskService.Submit`。
- Produces: `BundleService.PlanImport(ctx context.Context, req CommitImportRequest) (OperationPlan, error)` 与导入/导出任务执行器；接口字段依工程设计。

以下是测试名及关键断言片段；夹具在列出的测试文件内构建，输入使用仓库脱敏示例，不依赖真实工程。

- [ ] **Step 1: 写失败测试**

```go
func TestExpiredOrReplacedPreviewCannotCommit(t *testing.T) {
    if expiredCode != "IMPORT_PREVIEW_EXPIRED" || replacedCode != "BUNDLE_CHANGED" { t.Fatal("TOCTOU accepted") }
    if projectCountChanged { t.Fatal("partial commit") }
}
func TestDuplicateIdentityAndCrashKeepExistingProject(t *testing.T) {
    if duplicateCode != "PROJECT_EXISTS" || oldHash != recoveredHash { t.Fatal("existing project overwritten") }
}
func TestRoundTripRequiresNewBindingsAndGrants(t *testing.T) {
    if importedControlGrant || importedRuntimeVerification || automaticallyStarted { t.Fatal("imported local authority") }
    if !pendingBindings || configHashBefore != configHashAfter { t.Fatal("broken round trip") }
}
```

- [ ] **Step 2: 验证红灯**

Run: `go test ./internal/application ./internal/adapters/persistence ./tests/integration`
Expected: FAIL，缺少下述接口或行为；不能因测试环境损坏而把失败当成有效红灯。

- [ ] **Step 3: 实现本任务接口**

PlanImport校验preview未过期、原包身份、目标工作空间与冲突；Submit再核对指纹。使用journal先记录staging阶段、不可变文件改名与DB事务，故障注入覆盖每一边界，恢复只清理自建残留。重复projectId拒绝，不静默clone/覆盖。绑定按目标项目作用域登记，秘密值走本机vault；缺环境保留待补充。用两临时工作空间完成导出→预览→绑定→登记→再次导出，比较声明与文件hash，确认无源盘符/凭据/授权。最终 go test ./... 和 go vet ./...。

- [ ] **Step 4: 验证绿灯**

Run: `go test ./internal/application ./internal/adapters/persistence ./tests/integration`
Expected: PASS；格式化相关 Go 文件并运行 `go vet ./...`。

- [ ] **Step 5: 提交**

显式暂存本任务 Files 中的实现与测试，检查无本机数据后执行 `git commit -m "feat: commit and recover portable project imports"`。

## 独立验收

本计划可用内存/fake任务执行端口独立验证，无需真实Docker或桌面。接入运行计划后只有用户提交的导入/导出任务改变登记/目标文件，导入本身从不启动项目。
