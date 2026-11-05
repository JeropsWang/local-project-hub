# Docker 资源归属与正常停止

日期：2026-11-05。当前实现面向本机 Windows 命名管道连接的 Linux Docker Engine；远程服务器和 Windows 容器不在本轮自动管理范围。

## 归属与授权

清单读取引擎 ID、完整容器 ID、创建/启动时间、运行/健康状态和 Compose 的 project/service/配置文件/工作目录标签，不返回容器环境变量。标签和名称仅用于展示来源证据；即使资源 UUID 相同，也不会自动取得控制权。

本机登记分为 reference 和 managed。引用不会触发启停；managed 需要单独授予本工作空间的控制权。接管既有栈时核对引擎、实际 project、所选 service、配置指纹及源目录。源码目录与运行标签不一致时拒绝接管，不能把另一个 checkout 当作当前部署来源。

授权单独存储在本机数据库列中。资源 JSON 和可迁移项目声明不能成为备用授权来源；工作空间迁移确认会撤销本机授权。授权绑定完整实例身份，容器重建或启动代次变化需要重新核对。

## 启动与部分失败

新建栈使用 lph- 加完整资源 UUID 去除连字符的 project 名，不使用目录名。源 Compose 文件按声明顺序传递，显式设置 project-directory。只读预览通过 [compose config](https://docs.docker.com/reference/cli/docker/compose/config/) 的 no-interpolate/no-env-resolution/no-normalize 参数读取配置，只返回指纹和效果说明。

所选服务的依赖必须包含在声明中，并按拓扑顺序记录。含 pre_stop/post_start hook 的配置需要另外审查，不把数据库升级或任意 hook 藏进普通启动步骤。

启动前保存唯一 PendingStart 意图，再生成本机标签 overlay；原 Compose 文件不修改。[compose up](https://docs.docker.com/reference/cli/docker/compose/up/) 使用 no-deps/no-build/no-recreate/pull-never，只创建或启动所选对象。首次缺少镜像或需要更新既有配置时，返回明确修复需求，不隐式升级镜像、重建旧栈或删除卷。

CLI 失败后仍重新读取清单，保存本次已观察到的精确实例，便于正常回滚。失败或取消不能与外部 Engine 形成数据库原子事务，因此保留 PendingStart，直到后续核实；不自动重放未知步骤，不把一次空清单当作无残留证明。未核实意图不能显示为资源已全部停止。

## 共享资源和退出

每个消费者使用 projectId/runId/unitId 独立登记，重复提交不增加计数，只释放本次运行的引用。尚有其他用户时不停止资源；单独停止项目默认保留公共服务，明确全局退出后才处理没有用户且已授权的公共资源。

即使最后一个项目已停止，只要受管理公共容器仍运行或有未完成启动意图，工作空间仍处于活动状态，不能直接关闭或切换。全部正常停止后再解除这一状态。

停止前再次核对引擎 ID、容器 ID、创建及启动时间。先持久记录信号尝试，再使用 Engine 的 signal 接口发送容器配置的 StopSignal，并等待原实例退出；重复等待不重发信号。拒绝 SIGKILL、SIGSTOP 及 SIG9/SIG19 等数字别名。不使用 [默认 stop 超时后 SIGKILL](https://docs.docker.com/reference/cli/docker/container/stop/) 的行为；超时、OOM、异常退出、实例变化或未知状态均保留失败/未完成结果。不会调用 down -v、remove-orphans 或自动数据库迁移。

Engine API 在已实现的 1.45—1.53 区间内协商；太旧或只能使用更高 API 的端点在实例操作前拒绝。项目本身的版本需求与管理接口支持区间分别检查，依据 [Engine API 参考](https://docs.docker.com/reference/api/engine/version/v1.53/)。

## 验证范围

合成 Engine/CLI 与临时 SQLite 测试覆盖授权、共享引用、工作目录差异、命名冲突、信号别名、超时不强杀、不重复发送、重启后信号日志、部分启动和未核实意图。控制 CLI/Compose 插件使用可丢弃的独立 Job；业务进程的非强杀 Job 策略保持独立。

实际本机只读测试验证 Compose 5.4 的配置参数及稳定指纹、插件版本检测和 19 个容器清单；没有启动、停止或修改真实容器。Windows 插件发现必须保留 ProgramFiles 和必要的用户配置目录，见 [Docker CLI 官方实现](https://github.com/docker/cli/blob/master/cli-plugins/manager/manager_windows.go)。这不代表三个业务项目已经完成启停验收。

```powershell
$env:LPH_DOCKER_READONLY_SMOKE = '1'
go test ./tests/integration -run TestLocalDocker -count=1 -timeout 2m -v
```

该显式测试仅对当前本机 named-pipe 端点读取清单，并对临时合成文件执行 config；不拉取镜像、不操作业务数据。
