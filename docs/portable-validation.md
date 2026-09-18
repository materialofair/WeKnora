# 便携改造验证记录

日期：2026-09-18–19。验证主机：macOS ARM64，另有 GitHub 原生 CI。以下区分新实现的实测结果与复用功能；不把本地 mock 当作公司模型联调结果。

## 已验证

| 范围 | 证据 |
|---|---|
| Portable 标准版、无外部数据库启动 | `node scripts/smoke-portable.cjs`：真实 Go binary、健康检查、Web、注册、登录、重启、稳定端口、备份恢复再登录通过 |
| 完整入库与恢复 | `node scripts/smoke-ingestion.cjs`：真实上传 TXT/DOCX、异步解析、摘要、三种检索、原文引用、停机备份恢复、凭据解密认证、原文件字节一致性通过。模型是本地 mock（8 次 Embedding、2 次 LLM） |
| 文档解析 | `go test -tags 'sqlite_fts5,anydoc' ./internal/infrastructure/docparser/...`：真实 Rust 转换器、DOCX 图像、CSV、PDF、错误和回退路径通过 |
| 持久任务 | `go test -race -tags sqlite_fts5 ./internal/router -run TestLocal`：重试、延迟、并发、取消、重启和结果写库故障恢复通过 |
| 评估存储 | service `TestEvaluation` / `TestMemoryEvaluation`：重启、租户隔离、并发更新和错误路径通过，race 通过 |
| 图谱 | `go test -tags sqlite_fts5 ./internal/application/repository/retriever/sqlitegraph`：持久化、文档/知识库隔离、关系检索、删除、批量查询通过 |
| Wiki 与历史 | `go test -tags sqlite_fts5 ./internal/application/repository -run 'TestWiki\|TestSessionRepository'`：SQLite 来源关联、批量查询、正则搜索、相似标题与会话特殊字符搜索通过；正则使用 Go RE2，相似查询为流式扫描，最多保留 50 个结果 |
| 长期记忆 | `go test -tags sqlite_fts5 ./internal/application/service/memory`：现有离线测试通过；不代表公司模型召回质量验收 |
| 沙箱绑定 | sandbox `TestSQLiteBindings`：远程绑定保存、CAS、stale、密钥加密、租户隔离、错误路径及 race 通过 |
| 备份 | portable tests：停机锁、文件恢复、穿越/链接拒绝、截断归档及失败清理通过 |
| Electron | `npm test --prefix desktop`：地址验证、缺少后端、强制退出等待及即时重启通过；`node desktop/test-electron.cjs <packaged-executable>`：真实登录/设置窗口、无 Node 注入、退出关闭后端通过 |
| 前端 | `npm ci`、`npm run build`、飞书凭据流程 6 项测试通过；更新依赖后 `npm audit` 无已知漏洞 |
| 分发 | `node scripts/package-portable.mjs`、`npm run pack --prefix desktop -- --config.mac.identity=null` 在 macOS ARM64 成功；其余平台由 native CI 验证 |
| Linux 原生 CI | Ubuntu 22.04 x64：原生 Go/Rust 构建、两项真实运行烟测、Wiki/历史测试、桌面单元测试、Electron AppImage 打包与归档上传通过（[Actions run 35368679529](https://github.com/materialofair/WeKnora/actions/runs/35368679529)，提交 adf02d87） |
| macOS 原生 CI | macOS 14 ARM64：同一提交的原生构建、两项运行烟测、Wiki/历史测试、桌面单元测试、DMG/ZIP 打包及上传通过；真实 Electron 窗口另在本机验证 |
| Windows 原生 CI | Windows Server 2022 x64：同一提交的原生构建、运行库依赖检查、两项运行烟测、桌面单元测试、Electron NSIS/便携 EXE 打包、服务 ZIP 归档及上传通过；Unix 专用强制退出测试跳过。Wiki/历史专项 SQL 测试在 Mac/Linux 执行 |

## 下载本轮构建产物

提交 `adf02d87` 的 [三平台流水线](https://github.com/materialofair/WeKnora/actions/runs/35368679529) 全部成功。登录 GitHub 后可下载：

- [Windows x64：安装 EXE、便携 EXE、服务 ZIP](https://github.com/materialofair/WeKnora/actions/runs/35368679529/artifacts/10558486747)
- [macOS ARM64：DMG、应用 ZIP、服务归档](https://github.com/materialofair/WeKnora/actions/runs/35368679529/artifacts/10558045654)
- [Linux x64：AppImage 归档、服务归档](https://github.com/materialofair/WeKnora/actions/runs/35368679529/artifacts/10558105016)

安装与配置参见 [部署说明](portable.md)。制品是本轮测试构建，没有配置发行签名；公司终端验收仍是独立步骤。

## 能力基线状态

| 能力编号 | 此次处理 | 尚需验收 |
|---|---|---|
| CAP-01–06 导入、解析、入库、检索、引用 | 复用现有业务，加入真实原生解析器和本地持久任务 | 公司模型、复杂版面、扫描 OCR、批量真实语料 |
| CAP-07 Agent | 复用标准版执行/授权流程 | 真实内网模型工具调用、多轮质量与取消行为 |
| CAP-08–09 Skills/MCP/沙箱 | 保留适配器，补无 Redis 时的远程沙箱绑定持久化 | 公司远程沙箱/MCP地址和权限；本机强隔离未实现 |
| CAP-10–12 Wiki/记忆/历史 | 保留现有 Go 服务和标准版入口，相关异步任务接入持久队列 | 实际模型驱动生成、召回质量、远程文件检查点 |
| CAP-13 GraphRAG | 新增 SQLite 关系存储、检索与删除 | 大型图谱性能和抽取质量 |
| CAP-14 飞书 | 保留 Wiki/Drive 与 IM；移除指定七类数据源 | 私有飞书实际认证、增量同步、回调 |
| CAP-15–16 模型/权限 | 保留标准版与租户权限，不用 Lite 绕过登录 | 公司身份系统、模型厂商特有协议 |
| CAP-17 可靠运行 | 持久任务、密钥、备份、恢复、迁移失败中止、桌面生命周期 | 公司云桌面与目标服务器、升级样本；任务至少一次执行，外部副作用仍须幂等 |
| CAP-18 评估 | 结果持久化，重启中断明确标失败 | 真实基准集与质量门槛 |
| CAP-19 其他入口 | 保留未明确排除的 Web/API/IM/存储扩展；移除小程序 | 各外部服务单独联调 |

## 已知验证限制

全量 sandbox 测试中的 `TestPolicyAllowsPublicHostname` 在本机失败：DNS 代理将 `api.e2b.dev` 解析为 `198.18.0.18`，被既有地址保护拒绝。其余 sandbox 测试以 `-skip TestPolicyAllowsPublicHostname` 通过。没有修改安全策略来放行该地址。

未获得公司模型/飞书/远程沙箱、Windows 云桌面和目标服务器环境；相关项不声明完成验收。三平台的原生后端运行烟测已通过；Electron 图形界面仅在本机 macOS 实测，Windows 安装和 Linux 桌面仍须目标环境验收。制品未配置发行签名，企业策略与其他 Linux 发行版的系统库兼容性仍需实机确认。原生 CI 通过不等于所有公司环境已通过。
