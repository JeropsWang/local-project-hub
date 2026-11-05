# Desktop and Acceptance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把核心能力接入 Windows 桌面，完成可操作界面与三个项目的分层验收。

**Architecture:** Wails负责窗口、托盘、对话框和绑定，Go服务承接用例。React页面只组装，services/business/router/css分别归位；先完成任务和运行状态界面，再加入封面与动效。

**Tech Stack:** Wails v3稳定线、React/TypeScript/Vite、CSS Modules、Radix Primitives、Zustand、GSAP

**Spec:** [工程设计](../specs/2026-11-05-engineering-design.md)、[兼容设计](../specs/2026-11-05-compatibility-design.md)、[配置契约](../../contracts/README.md)。

## Global Constraints

- Go 模块 github.com/JeropsWang/local-project-hub；构建基线 Go 1.26.1；首轮 Windows 11 x64。
- 格式版本 1.0；未知版本拒绝；配置、环境满足和实际运行验证分开。
- 领域与应用不依赖 Wails、Docker SDK、Windows API；入口只组装；源码注释并格式化。
- 原项目和真实业务数据不修改；测试使用虚构数据、临时目录与受控进程。
- 不把 audit.local、密钥、真实项目登记和日志提交 Git。

## Review Focus

- 关窗不能停止项目；明确退出必须等后端安全停止结果（Task 1）。
- 双击启动、事件断线和页面切换不能重复创建任务或丢失运行状态（Task 2）。
- 键盘、缩放/窄窗口、减少动态效果与长中文名称必须可用（Task 2）。
- 原项目没有控制服务端时，模拟测试通过不能变成完整接入通过（Task 3）。
- 安装升级/卸载和公开CI不能覆盖工作空间或包含本机项目数据（Task 4）。

## 前置与边界

依赖前3份计划。Wails精确版本及前端依赖在实现本任务时从官方发布核对并锁定，禁止nightly/latest漂移。视觉方向是专业中性、可选个性封面；具体页面排版先给可点击原型评审，确认后实现完整动效，原型不连接真实系统资源。

### Task 1: 薄入口、桌面桥接与托盘

**Files:**
- Create: `main.go`, `assets.go`, `internal/bootstrap/app.go`
- Create: `internal/desktop/services/{workspace,project,check,plan,task,bundle,lifecycle,log}.go`
- Create: `internal/desktop/events/publisher.go`, `internal/desktop/window/lifecycle.go`
- Create: `frontend/package.json`, `frontend/package-lock.json`, `frontend/vite.config.ts`, `frontend/tsconfig.json`, `frontend/eslint.config.js`, `frontend/.prettierrc.json`, `frontend/index.html`
- Create: `frontend/src/main.tsx`, `frontend/src/services/generated/`（Wails生成）
- Test: `internal/desktop/window/lifecycle_test.go`, `internal/desktop/services/task_test.go`

**Interfaces:**
- Consumes: 工程设计所有 application 服务；运行计划 TaskService.Events。
- Produces: 无context参数的桌面服务包装；`WindowController.Close() error` 隐藏窗口，`Quit() error` 请求应用层退出；bootstrap只在收到成功退出结果后关闭Wails。

以下是测试名及关键断言片段；夹具在列出的测试文件内构建，输入使用仓库脱敏示例，不依赖真实工程。

- [ ] **Step 1: 写失败测试**

```go
func TestCloseHidesAndExplicitQuitWaits(t *testing.T) {
    if closeStopCalls != 0 || hideCalls != 1 { t.Fatal("close changed project lifecycle") }
    if frameworkQuitBeforeStopped || failedStopClosedManager { t.Fatal("unsafe desktop exit") }
}
func TestServiceRejectsRawExecutionAndMapsErrors(t *testing.T) {
    if acceptsCommandFromFrontend || exposesSecret { t.Fatal("bridge crossed boundary") }
}
```

- [ ] **Step 2: 验证红灯**

Run: `go test ./internal/desktop/...`
Expected: FAIL，缺少下述接口或行为；不能因测试环境损坏而把失败当成有效红灯。

- [ ] **Step 3: 实现本任务接口**

创建最小React/Vite静态入口与Wails配置以验证桥接，版本锁定记录；入口只调用bootstrap，assets.go只嵌入构建产物。services转换DTO/错误，不包含进程或容器业务。生成绑定只由frontend services导入。托盘提供显示窗口/退出，退出失败重新展示等待用户界面。事件携带taskId/seq，补读或缺口时读取快照；应用隐藏暂停装饰动画，不停止核心。

- [ ] **Step 4: 验证绿灯**

Run: `go test ./internal/desktop/...`
Expected: PASS；格式化相关 Go 文件并运行 `go vet ./...`。

- [ ] **Step 5: 提交**

显式暂存本任务 Files 中的实现与测试，检查无本机数据后执行 `git commit -m "feat: connect desktop shell and tray lifecycle"`。

### Task 2: 分层页面、完整操作流程与视觉

**Files:**
- Create: `frontend/src/pages/{Workspace,Library,ProjectDetail,Environment,Containers,Scripts,Logs,Transfer,Settings}Page.tsx`
- Create: `frontend/src/router/routes.tsx`, `frontend/src/services/{client,events}.ts`, `frontend/src/business/{projects,tasks,transfer}.ts`
- Create: `frontend/src/stores/{workspace,tasks,preferences}.ts`, `frontend/src/types/contracts.ts`
- Create: `frontend/src/components/{ProjectCover,TaskProgress,CheckReport,BindingForm,ExitProgress}.tsx`
- Create: `frontend/src/css/{tokens,global}.css`, `frontend/src/css/{Workspace,Library,ProjectDetail,Environment,Containers,Scripts,Logs,Transfer,Settings}.module.css`
- Create: `frontend/src/animations/{layout,parallax,useMotion}.ts`, `frontend/tests/{flows,motion,accessibility}.test.tsx`

**Interfaces:**
- Consumes: Task 1生成服务；Go DTO是权威模型，TypeScript适配不重复实现兼容/授权规则。
- Produces: `projectActions.start(ref: ProfileRef): Promise<TaskView>`、`transferActions.preview(path: string): Promise<ImportPreview>`、`taskActions.resolve(req: ResolveTaskRequest): Promise<TaskView>`；稳定requestId防重复提交。

以下是测试名及关键断言片段；夹具在列出的测试文件内构建，输入使用仓库脱敏示例，不依赖真实工程。

- [ ] **Step 1: 写失败测试**

```tsx
it("重复点击只提交一个计划并补读断线事件", async () => {
    expect(submitCalls).toHaveLength(1);
    expect(renderedTaskState).toBe("waiting-user");
});
it("键盘、减少动态效果与长日志仍可维护", async () => {
    expect(startButton).toHaveAccessibleName("启动整个项目");
    expect(activeDecorativeTweens).toBe(0); // reduced-motion 或窗口隐藏
    expect(renderedLogLines.length).toBeLessThanOrEqual(2000);
});
it("导入缺绑定不显示可启动，退出失败提供恢复", async () => {
    expect(startButton).toBeDisabled();
    expect(retryExitButton).toBeVisible();
    expect(abortExitButton).toBeVisible();
});
```

- [ ] **Step 2: 验证红灯**

Run: `npm --prefix frontend run test -- --run`
Expected: FAIL，缺少下述接口或行为；不能因测试环境损坏而把失败当成有效红灯。

- [ ] **Step 3: 实现本任务接口**

先用fake services制作库/详情/退出流程原型并给用户评审视觉排版；实现React Router路由、CSS Modules、Radix交互和Zustand本机偏好。Workspace先选目录；Library分组/封面/列表，Detail显示专用/共享/仅引用关系；Environment双模式；Scripts保存revision冲突明确报告且提示当前运行使用旧快照；Transfer预览缺项/绑定/准备各自触发；Logs按100ms批量与2000行限制。按钮等效键盘操作，覆盖200%缩放与最小960×640窗口、长中文/空态/错误态。GSAP通过useGSAP范围清理，Flip仅可控布局过渡，视差仅封面；grain轻量静态纹理，不影响文本。遵守prefers-reduced-motion和隐藏窗口暂停。模板封面不使用无授权角色图。

- [ ] **Step 4: 验证绿灯**

Run: `npm --prefix frontend run test -- --run`
Expected: PASS；运行 `npm --prefix frontend run typecheck`、`npm --prefix frontend run lint`、`npm --prefix frontend run format:check` 与 `npm --prefix frontend run build`。

- [ ] **Step 5: 提交**

显式暂存本任务 Files 中的实现与测试，检查无本机数据后执行 `git commit -m "feat: implement layered project management screens"`。

### Task 3: 三个项目的版本化接入与分层验收

**Files:**
- Create: `profiles/templates/{web-go,wechatbot,typing-tracker}.project.json`（仅脱敏配置/公开来源身份）
- Create: `tests/integration/profiles_windows_test.go`, `docs/integrations/acceptance-matrix.md`
- Modify: `docs/integrations/managed-control-v1.md`（记录协议实测，不改变正常手工启动）
- Separate review: typing-tracker v0.9.8 独立集成分支与 WeChatBot v0.6.1 独立集成分支；原仓库路径/文件从各自源码重新审阅确定，不在本仓库复制私有源码

**Interfaces:**
- Consumes: 兼容设计锁定的来源身份、管理控制协议与前3计划接口。
- Produces: 三类接入模板和每个profile的 config/metadata/runtime/desktop 分级验收记录；原项目服务端遵循协议v1。

以下是测试名及关键断言片段；夹具在列出的测试文件内构建，输入使用仓库脱敏示例，不依赖真实工程。

- [ ] **Step 1: 写失败测试**

```go
func TestProfilesKeepDifferentDatabasesAndOptionalFeatures(t *testing.T) {
    if mergedPG17With18 || mergedRedis7With8 || startedOptionalSearchOrModel { t.Fatal("profile changed requirements") }
}
func TestMissingStopAdapterBlocksManagedStart(t *testing.T) {
    if startedWithoutControlServer || runtimeVerifiedFromFakeOnly { t.Fatal("incomplete integration marked done") }
}
func TestIsolatedNormalExitFlushesData(t *testing.T) {
    if !fakeDataFlushed || !allManagedChildrenExited || stoppedExternalClient { t.Fatal("lifecycle contract failed") }
}
```

- [ ] **Step 2: 验证红灯**

Run: `go test ./tests/integration -run 'Test(Profiles|MissingStopAdapter|IsolatedNormalExit)'`
Expected: FAIL，缺少下述接口或行为；不能因测试环境损坏而把失败当成有效红灯。

- [ ] **Step 3: 实现本任务接口**

公开模板不放PersonWeb私有路径或配置。typing固定正式v0.9.8对应提交，Bot固定v0.6.1对应提交，分别保留源码/制品profile；Go/Web按本地选定源快照而非既有容器目录自动推断。先完成模拟测试，再为原仓库最小控制服务端准备独立变更并确认授权后实现；不能用本计划去覆盖原仓库未提交修改。WeChatBot只用合成数据/关闭真实发送的bridge；typing使用临时数据目录。隔离真实运行另作opt-in，不连接生产数据库/真实微信。实际停止服务端完成前矩阵标blocked，首版未完成；不得放宽成只检测。原项目变更需要在各自仓库运行适用构建和测试。

- [ ] **Step 4: 验证绿灯**

Run: `go test ./tests/integration -run 'Test(Profiles|MissingStopAdapter|IsolatedNormalExit)'`
Expected: PASS；格式化相关 Go 文件并运行 `go vet ./...`。

- [ ] **Step 5: 提交**

显式暂存本任务 Files 中的实现与测试，检查无本机数据后执行 `git commit -m "test: define versioned project lifecycle acceptance"`。

### Task 4: Windows构建、安装与发布候选

**Files:**
- Create: `.github/workflows/ci.yml`, `build/windows/installer.yaml`, `scripts/build.ps1`
- Create: `docs/development.md`, `docs/releases/windows-checklist.md`
- Modify: `README.md`, `.gitignore`
- Test: `tests/integration/packaging_windows_test.go`

**Interfaces:**
- Consumes: 前述模块、前端锁文件和受控集成结果。
- Produces: 公开CI可复现的Windows x64开发安装包、SHA256和验证报告；正式发布为单独明确动作。

以下是测试名及关键断言片段；夹具在列出的测试文件内构建，输入使用仓库脱敏示例，不依赖真实工程。

- [ ] **Step 1: 写失败测试**

```go
func TestPackageHasOnlyManagerAssets(t *testing.T) {
    if includesLocalBindings || includesCredentials || includesAudit || includesProjectData { t.Fatal("private data bundled") }
}
func TestUpgradeAndUninstallPreserveWorkspace(t *testing.T) {
    if workspaceHashAfter != workspaceHashBefore { t.Fatal("installer destroyed user data") }
}
```

- [ ] **Step 2: 验证红灯**

Run: `go test ./tests/integration -run 'Test(Package|UpgradeAndUninstall)'`
Expected: FAIL，缺少下述接口或行为；不能因测试环境损坏而把失败当成有效红灯。

- [ ] **Step 3: 实现本任务接口**

使用Wails稳定版官方构建/安装配置，scripts/build.ps1串行调用锁定工具，失败立即停止。CI运行 go test ./...、go vet ./...、TS类型/lint/format/test/build，Windows受控进程测试与安装冒烟；headless CI不伪称GUI实机验收。安装器检查WebView2、提供官方修复路径，不自动给项目安装SDK。升级/卸载只处理管理器安装文件，保留用户指定工作空间。无签名证书时交付明确标识的开发构建，不宣称已签名。实际发布版本/安装包前确认验收矩阵无必需blocked项，签名和发布渠道另按用户明确选择。

- [ ] **Step 4: 验证绿灯**

Run: `go test ./tests/integration -run 'Test(Package|UpgradeAndUninstall)'`
Expected: PASS；格式化相关 Go 文件并运行 `go vet ./...`。

- [ ] **Step 5: 提交**

显式暂存本任务 Files 中的实现与测试，检查无本机数据后执行 `git commit -m "build: add Windows manager packaging and CI"`。

## 完成条件

三个指定项目必须分别通过真实来源核对、独立环境/外部绑定检测、完整启动、故障定位、配置往返和正常退出；模拟通过、公开CI通过与真实桌面通过分别记录。若原项目控制接口尚未合入，仅交付管理器里程碑，不标首版完整完成。
