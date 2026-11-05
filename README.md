# Local Project Hub

面向本地项目的桌面管理工具，计划统一管理项目分组、Docker 容器归属、公共服务依赖、本机进程和可编辑的启停脚本，并提供项目一键导出与导入。

**当前状态：工程设计与实施计划评审阶段。已有配置契约和分阶段计划，尚无可运行应用或安装包。**

## 已确认的需求

- 以项目为中心汇总和分组，优先管理本地项目。
- 明确容器属于哪些项目，以及哪些容器被多个项目共用。
- 从项目找到相关容器，便于定位故障、维护与修改。
- 一键启动完整项目，包括 Docker 服务和本机前端、后端等进程。
- 项目保存自己的脚本，用户可以编辑脚本。
- 提供桌面程序，同时支持配置一键导出、迁移与导入识别。
- 规范配置兼容性，导入时检查文件完整性和缺失依赖。
- 使用 Git 管理本工具的版本，托管在 `JeropsWang` 的公开 GitHub 仓库。
- 采用独立桌面程序路线，首版仅支持 Windows。
- 环境处理同时支持自动准备项目独立环境、仅检测并提供修复步骤两种模式，由用户选择。
- 准备环境前，由用户先初始化并指定使用哪个磁盘、哪个文件夹。
- 关闭管理器窗口后，已启动项目继续在后台运行。
- 明确退出管理器时，先安全停止全部已启动项目，再退出应用。
- 默认风格专业中性，支持个性化封面，加入动态排版、视差与颗粒质感。
- 源码须有有效注释并统一格式化，页面、CSS、路由、服务和业务逻辑分层组织。
- 核心采用 Go + Wails；前端由用户授权助手选用 React + TypeScript + Vite，样式使用 CSS Modules。
- 首版优先完整接入指定三个项目；Java、MySQL 等先提供环境识别与修复报告，后续按样本扩展自动准备。

## 讨论文档

- [需求与边界](docs/requirements.md)
- [开源参考与候选路线](docs/approaches.md)
- [配置迁移与兼容规则建议](docs/portability.md)
- [工作空间初始化与环境处理](docs/environments.md)
- [视觉方向](docs/visual-direction.md)
- [工程约定与分层建议](docs/engineering-conventions.md)
- [仓库开发约定](AGENTS.md)
- [技术选型与比较](docs/technology-selection.md)
- [Windows 兼容与配置迁移设计草案](docs/superpowers/specs/2026-11-05-compatibility-design.md)
- [工程设计与模块接口](docs/superpowers/specs/2026-11-05-engineering-design.md)
- [配置格式与虚构示例](docs/contracts/README.md)
- [分阶段实施计划](docs/superpowers/plans/2026-11-05-implementation-index.md)
- [安全控制协议](docs/integrations/managed-control-v1.md)

## 架构与迁移边界

桌面管理工具读取可随项目保存的配置，统一调用 Docker 和本地进程执行能力。项目与公共服务通过显式依赖关系关联，配置与本机路径分开保存，便于导出和重新导入。

桌面安装包与项目导出包是两种产物：前者安装管理工具，后者迁移被管理项目的配置。首版包只携带声明、受控脚本、Compose和显式配置资源；源码、制品、镜像、模型、凭据与业务数据作为外部要求重新绑定，后续再扩展全量或离线打包。

本公开仓库保存管理工具的文档与后续源代码。被管理项目的源码、真实环境配置、日志、数据库和凭据，不会因登记项目而自动进入本仓库。

## 开源参考

- [Tilt](https://github.com/tilt-dev/tilt)：本地进程和容器开发编排。
- [Dockge](https://github.com/louislam/dockge)：围绕 Compose 管理服务栈。
- [Komodo](https://github.com/moghtech/komodo)：服务器、部署及自动化流程。
- [Portainer CE](https://github.com/portainer/portainer)：通用容器维护。

以上为研究对象。已选择独立桌面程序路线，不直接 fork 其中一个项目；尚未复制其代码。
