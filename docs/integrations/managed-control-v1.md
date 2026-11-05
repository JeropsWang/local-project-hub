# 受管理项目的安全控制协议 v1

状态：协议客户端、Windows 进程适配器与合成退出测试已实现；原项目尚未修改，尚未进行真实业务项目退出验收。

## 目的

管理器明确退出时，需要让受管理项目停止新业务、保存数据、结束工作线程并正常退出。窗口关闭可能只是托盘驻留，不能当作停止。此协议仅在应用被管理器以显式管理模式启动时启用；普通手工启动保持原行为。

## 传输与身份

Windows 当前交互用户会话内的命名管道，名称包含 runId。管道 ACL 限制为当前用户和 SYSTEM。管理器为每次启动创建 32 字节随机控制令牌，通过进程环境传递，不出现在命令行、普通日志或导出包中。本机恢复所需引用受当前用户保护，不能因 PID 相同自动重新授权。

采用 UTF-8 一行一个 JSON，单消息最多 16 KiB。字段为 protocolVersion=1.0、runId、operationId、action、token；action 仅允许 status、quiesce、shutdown。未知字段/版本/身份拒绝，不提供执行任意命令、读文件或发送聊天消息的能力。

同一 operationId 幂等；令牌常量时间比较；同一运行只允许一个停止协调流程。首版不暴露 TCP 端口，不跨用户或机器操作。

被管理应用是管道服务端，管理器是客户端。管道名固定为 `\\.\pipe\LocalProjectHub.<runId>`，runId 为小写 UUID v4。管理器传入 `LPH_RUN_ID`、`LPH_CONTROL_PIPE`、`LPH_CONTROL_TOKEN` 三个环境变量；token 为 32 随机字节的 64 位小写十六进制文本。应用验证环境与管道名一致后创建管道，监听完成前不得宣称控制就绪。这些运行凭据独立于用户的 envBindings；管理器拒绝项目配置覆盖这些保留变量。

请求字段全部必需，且不允许额外字段：

```json
{"protocolVersion":"1.0","runId":"55555555-5555-4555-8555-555555555555","operationId":"66666666-6666-4666-8666-666666666666","action":"shutdown","token":"<64 lowercase hex characters>"}
```

响应必需字段为 protocolVersion、runId、operationId、kind、state；kind 仅 accepted、status、completed、rejected，state 使用下一节的状态。拒绝/失败/阻塞时增加 error 对象，且仅含 code、message，不回显 token 或请求原文。例如：

```json
{"protocolVersion":"1.0","runId":"55555555-5555-4555-8555-555555555555","operationId":"66666666-6666-4666-8666-666666666666","kind":"accepted","state":"quiescing"}
```

每连接发送一个请求，可返回 accepted 后再返回 completed；应用停止前发送 completed/stopped 并刷新管道。status 请求返回一条 status 即可，使用新 operationId 轮询。相同 operationId 与相同 action 返回当前/缓存结果，不重做动作；相同 ID 不同 action 返回 OPERATION_CONFLICT。错误码至少为 INVALID_REQUEST、UNAUTHORIZED、OPERATION_CONFLICT、WORK_PENDING、SAVE_FAILED、INTERNAL。不可解析或超长帧关闭连接；能关联到合法请求的拒绝返回 rejected。客户端核对响应版本、runId、operationId，缓存 completed/stopped，再核对实例实际退出；仅有管道 EOF 不算成功。保存进行中只响应 status，不因连接断开取消保存。

## 状态与确认

应用报告 running、quiescing、blocked、saving、stopped 或 failed。shutdown 返回 accepted 仅说明已接收；只有完成保存且退出前发送 stopped，随后原进程及其受管子进程退出，管理器才能记录正常停止。

status只读返回当前状态。quiesce暂停接收新业务并等待可收敛的在途工作，不保存后退出；无法收敛时报告blocked及原因。shutdown包含quiesce步骤，随后保存和退出，已quiesce时不重复启动业务动作。状态不会因为客户端断开或重新连接而回到running。

连接断开、进程崩溃或退出码不符合预期不能伪装 stopped。取消前端等待不会撤销已经开始的数据保存。超时返回状态和原因，管理器保留自身运行；强制终止是另外的明确操作。

## typing-tracker / Sariana

在 GUI 主线程通过排队信号进入既有停止协调：停止新命令和采集、处理未完成交互、取消可取消请求、保存最终统计、关闭数据库和工作线程，再退出。控制线程不能直接访问 QWidget 或 SQLite。

未完成练习或保存失败时返回 blocked，并显示原因；不自动丢弃未保存工作。不能使用 WM_CLOSE 作为协议实现，因为现有应用会驻留托盘。

应在以 GitHub v0.9.8 为基线的独立集成分支准备最小修改，不重写 tag、不把 master 0.9.5 当成 0.9.8。原项目的架构、数据目录和单实例规则保持。

## WeChatBot

先全局暂停新任务，等待或报告在途发送状态，停止由该工作台启动的桥接，再关闭 Fastify runtime、队列与数据连接。不能关闭、重启或重新登录用户微信，也不能自动重试不确定发送。

管理接口需要复用现有业务门禁。测试只使用合成数据和关闭真实发送的模拟桥接；实际桌面和收发验收另行明确授权。

## 变更与验收边界

本仓库已实现协议客户端、模拟服务和失败场景测试。原仓库新增服务端分别作为独立可评审变更处理，未取得该变更授权时不修改原仓库。协议及原项目集成未完成时，整个首版不能宣称三个项目安全退出已全部实现。

验收至少包括：身份错误、重复请求、保存失败、未完成工作、停止超时、ACK 后崩溃、正常保存且退出、源配置编辑后仍按本次运行身份停止。所有测试使用临时目录与模拟进程，不连接真实数据库或微信。

## Windows 适配器的当前实现

启动命令使用独立 argv，不拼接 Shell 源码。进程先挂起创建，加入业务 Job、记录 PID/创建时间/实际执行路径并保护控制凭据后再恢复。业务 Job 不设置 KILL_ON_JOB_CLOSE；准备依赖用的可丢弃安装 Job 另有独立策略。关闭活动适配器会返回未停止结果。

管理器保留原进程内核句柄；父进程退出后仍通过 Job 观察子进程。完成端口在空 Job 阶段关联，成员提示需核对 Job 归属并保留句柄，退出码异常不能伪装正常停止。缺失或过快退出的成员无法核实则报告未验证，不凭 PID 或进程数为零接管。参考 [Microsoft Job 完成端口说明](https://learn.microsoft.com/en-us/windows/win32/api/winnt/ns-winnt-jobobject_associate_completion_port)。

客户端使用固定 [go-winio v0.6.4](https://github.com/microsoft/go-winio/releases/tag/v0.6.4)，发送令牌前核对管道服务端 PID 和原实例身份。运行凭据以 CurrentUser DPAPI 和 runId 相关熵保存到工作空间的本机 secrets 目录，不随配置迁移。当前适配器只控制本次明确取得句柄的实例；恢复协调器须单独核实重启后记录，解密成功不等于取得控制权。

启动时冻结停止命令和脚本字节/哈希，保留文件扩展名；编辑未来配置不会替换当前停止命令，快照被改动则拒绝执行。普通等待超时仅结束调用方等待，已接受的保存继续进行；同一操作可继续等待，前次操作收敛后才能显式新建重试。控制传输另有 30 分钟上限，断线或到期保持未完成状态，不强杀进程。

日志在落盘前脱敏，支持秘密跨多次写入；与秘密无关的短行可实时显示。每运行 10MiB×3 轮转，分页最多 2000 条，轮转丢失旧游标时报告缺口。合成测试覆盖父退子留、子进程失败、错误令牌、ACK 后崩溃、保存失败/阻塞、慢保存继续等待、原始停止脚本、快照篡改、DPAPI 及日志边界；这仍不代表三个真实业务项目已经完成接入。
