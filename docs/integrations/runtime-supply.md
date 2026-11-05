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
