# 固定运行时供应与验证

日期：2026-11-05。运行时包由管理器内置目录选择，不接受项目配置中的任意安装链接；供应包和项目配置包使用不同限额。

| 运行时 | 固定版本 | 来源与校验依据 |
| --- | --- | --- |
| Node.js | 22.19.0 Windows x64，附带 npm 10.9.3 | [官方 SHA-256 清单](https://nodejs.org/download/release/v22.19.0/SHASUMS256.txt)；npm 版本核对固定 tag 的 deps/npm/package.json |
| Go | 1.26.1 Windows amd64 | [官方发布清单](https://go.dev/dl/?mode=json&include=all) |
| Python | 3.12.16 / 3.13.18 Windows x64，常规 GIL 构建 | [Astral python-build-standalone 20261026](https://github.com/astral-sh/python-build-standalone/releases/tag/20261026) 的 install_only 包及发布资产 SHA-256 |

这是首轮供应基线，不是各项目的理论最低版本。版本选择先满足声明与源码元数据交集，再考虑偏好版本；不匹配时给出不支持自动准备的结果。

实现先下载到本工作空间的独立临时文件，按实际字节限额和 SHA-256 校验，再解包到本次新建的 staging 目录。拒绝越界路径、链接、加密 ZIP、Windows 路径冲突及不支持的文件类型。成功后原子发布，取消只清理本次创建的临时对象，不替换已有冲突 SDK。

SDK 元数据保存每个文件的大小与 SHA-256，复用前重新核对；本机 PATH 和注册表保持原状。Python 环境仍在目标位置重建，不能把安装好 SDK 当作项目依赖已就绪。

合成测试已覆盖损坏包、取消、两个 Python ABI 共存、目录逃逸、计划目标篡改及已安装文件同尺寸篡改。另已在忽略的开发工作空间实际下载四个固定供应包，校验、解包并执行固定版本查询，四项通过；这些测试不代表三个业务项目运行验收通过。

真实供应测试默认跳过。开发者明确启用时运行：

```powershell
$env:LPH_RUNTIME_SUPPLY_SMOKE = '1'
go test ./tests/integration -run TestPinnedRuntimeSupplySmoke -count=1 -timeout 20m -v
```

目录记录位于 internal/adapters/environment/catalog.json，管理器生成的 SDK、缓存和测试工作空间不提交 Git。

## 洁净项目副本与依赖恢复

自动准备先生成本机计划，记录所选源码绑定的逐文件 SHA-256、原路径、固定 SDK 和安装副作用。执行前重新扫描；新增、删除或改变源码均使计划失效。每次准备使用 envs 下新的独立目录，失败或取消只删除本次新建副本，原项目、旧环境及其他目录保留。

副本排除继承的 .venv、.venv-image-match、.deps、node_modules，以及常见 Git、缓存、构建、密钥、日志和数据目录。必需声明若落入排除范围，明确要求重新绑定或手工处理，不显示为完整副本。规则不能识别任意文件中的秘密或脚本动态依赖，需要项目的显式声明。

支持 npm package-lock v2/v3 的 npm ci、requirements.txt 的目标位置 venv 重建及 pip check、Go 模块下载和校验。npm 生命周期和 Python 构建后端属于明确展示的安装副作用；不调用未知 setup 脚本。未启用的可选模型不恢复。只有 pyproject 锁恢复、同一配置中多个 Python 环境等尚未支持的组合会返回手工步骤；不猜测安装命令。requirements.txt 未必锁定传递依赖，报告会保留这一限制。

Python venv 在最终目录直接建立，之后不重命名搬迁；这是 [Python 官方说明](https://docs.python.org/3.12/library/venv.html) 要求重新建立环境的原因。固定探测和准备命令显式禁止写字节码，防止查询或环境创建改写共享 SDK 的 .pyc。运行时损坏不会被静默当作可信环境接管。

依赖准备进程使用私有 home/temp/cache、受限环境变量、隐藏窗口及每条命令最多 1MiB 本地日志。进程先以挂起状态建立，加入 Windows Job 后再恢复；取消只终止该准备 Job 的进程树，依据 [Windows Job 文档](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects)。这不是操作系统权限沙箱，安装脚本仍有当前用户的权限；长期业务项目的安全停止另由生命周期适配器处理。

所有准备步骤通过后，保存本机 ready 回执，再以数据库事务同时切换源码与运行时绑定，不改变可迁移配置版本。事务失败、路径绑定冲突或项目已运行时保留原选择。ready 仅表示这次准备完成，启动仍须通过独立检测、必要文件和服务就绪检查。

最小项目的真实 SDK 测试显式启用：

```powershell
$env:LPH_PROJECT_PREPARATION_SMOKE = '1'
go test ./tests/integration -run TestManagedProjectPreparationSmoke -count=1 -timeout 7m -v
```

先运行上面的供应测试建立忽略的开发工作空间。该测试只使用合成空依赖项目，检查 Node 安装钩子在副本执行、Python 3.12/3.13 的 venv 和 pip check、Go 模块恢复，以及准备后 SDK 完整性；不启动三个真实业务项目。
