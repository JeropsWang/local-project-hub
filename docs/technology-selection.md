# 技术选型与比较记录

日期：2026-11-05。状态：用户已选择 Go + Wails；前端由用户授权助手结合实际需求选择。本文记录选型与架构基线，尚未实现应用，也不代表完整功能设计已经批准。

## 当前选择

| 部分 | 选择 | 职责 |
| --- | --- | --- |
| 桌面框架 | Wails，采用当前桌面稳定的 v3 系列作为基线 | 窗口、托盘、系统交互、Go 与界面桥接 |
| 核心语言 | Go | 环境检测、兼容判断、进程与容器管理、导入导出、任务和退出编排 |
| 前端 | React + TypeScript | 项目工作台、脚本编辑和维护界面 |
| 前端构建 | Vite，构建后嵌入静态资源 | 开发与生产资源构建 |
| 样式 | CSS Modules + CSS 变量 | 独立样式文件、主题、组件样式与纹理 |
| 路由 | React Router，优先 HashRouter | 桌面静态资源环境中的页面导航 |
| 界面状态 | Zustand | 选择、筛选、主题及核心状态的展示快照 |
| 基础交互组件 | Radix Primitives，按需使用 | 对话框、菜单等交互基础，自行编写 CSS |
| 动效 | CSS 简单过渡 + GSAP / @gsap/react | 动态排版、视差与可清理的受控动画 |
| 工程检查 | Go 格式化、go vet、TypeScript 类型检查、ESLint、Prettier | 源码格式和静态质量 |

Go + Wails 是用户明确选择。React、Vite、CSS Modules 及配套前端库是助手在用户授权范围内作出的工程选择；实现时仍需锁定并验证具体依赖版本，不能把公开网站展示的 latest 当作可复现构建依据。

## 选择依据

项目主要工作是检测环境、执行受控任务、读写配置、流式采集日志和调用 Docker，当前没有已确认的重型计算需求。选择 Go 重点考虑核心逻辑的可读性、任务编排与后续维护成本，而非承诺比 Rust 更省内存或比 Node 更快。

Wails 提供 Go 与 Web 界面的桥接；Windows 使用 WebView2，能够保留封面、视差与颗粒质感等 Web 视觉能力。安装本工具不要求终端用户准备 Go 或前端构建环境；WebView2 和被管理项目自身的运行环境是另外的依赖。

React 配合独立的服务、业务与状态模块，落实用户要求的目录分层。第三方编辑器或终端以独立组件适配，不将其生命周期与页面核心逻辑混在一起。界面框架本身不保证分层，仍需工程检查与代码评审。

## Go、Rust 与 Next.js 的比较

| 路线 | 可行性与优势 | 对本项目的取舍 |
| --- | --- | --- |
| Go + Wails + React | 本地 Go 核心与 Web 界面；适合任务和资源管理工具 | 已选；核心业务、系统适配和界面仍需明确分层 |
| Rust + Tauri + React | 编译型本地核心，所有权模型可约束资源使用 | 曾考虑；异步与资源状态设计需要对应 Rust 工程能力，当前未采用 |
| Next.js 全栈 + 桌面入口 | 本机 Node 后端也可以执行进程和访问文件 | 可行，但要处理本地服务、端口、运行时和桌面生命周期；当前不采用 |
| Next.js 静态导出 + 本地核心 | 可嵌入静态页面，但请求期服务端功能不在导出产物中 | 此时仍需本地核心；本项目直接采用 Vite 构建 React 界面 |

Rust 与 Go 都能实现版本检查、Windows 进程树控制、迁移与退出清理；换语言不能免除这些设计。Next.js 也不是原生桌面框架；全栈模式与静态导出需要分别比较，不能把静态导出的体积直接当作全栈桌面程序成本。

没有进行三套实际原型的安装包、启动时间或内存测量，因此不记录未经验证的体积和性能数字。

## 本地核心与界面的边界

- 核心保存任务与实际资源状态，界面保存展示状态；窗口隐藏后核心继续运行。
- Go 业务用例独立于 Wails；桌面桥接层负责参数、错误和结果映射。
- 长任务需要明确任务标识、进度、取消和超时。页面卸载不能直接等价于任务取消。
- 日志由核心采集并保留，界面采用有限缓冲、批量更新与按需展示，不能无限堆积到全局状态中。
- 明确退出时，先完成受管理项目的安全停止，再结束应用；不因用户关闭窗口就结束项目。
- 样式规则保存在 CSS 文件中。GSAP 运行时生成的位移或透明度参数属于动效适配行为，不据此把整页样式写进组件脚本。

## 接下来需要确定

- 首批支持的项目语言、依赖工具、Windows 版本与 Docker 环境。
- 项目声明与包格式、版本判断来源、迁移规则。
- 持久化模型、任务和事件契约、具体模块接口。
- 编辑器与交互终端的选择及语言支持；终端显示组件自身不等于进程执行能力。
- 页面原型、性能验证范围与完整设计。

## 官方资料

- [Wails FAQ](https://wails.io/faq/) 和 [桌面兼容与版本状态](https://wails.io/status/)。
- [Next.js 部署方式](https://nextjs.org/docs/app/getting-started/deploying) 和 [静态导出限制](https://nextjs.org/docs/app/guides/static-exports)。
- [Node.js 子进程能力](https://nodejs.org/api/child_process.html)。
- [Vite CSS Modules](https://vite.dev/guide/features.html#css-modules)。
- [React 状态逻辑拆分](https://react.dev/learn/scaling-up-with-reducer-and-context) 和 [Zustand](https://github.com/pmndrs/zustand)。
- [Radix 样式边界](https://www.radix-ui.com/primitives/docs/guides/styling)。
- [GSAP React 生命周期](https://gsap.com/resources/React/)。
