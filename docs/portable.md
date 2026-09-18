# Knowledge Assistant：Electron / Web 便携运行

本分支复用 WeKnora 的 Go 服务和 Vue 界面，便携模式保留标准版账户、组织权限、知识库、Agent、Wiki、记忆、MCP 和 Skills。运行时采用 SQLite、FTS5/sqlite-vec、本地文件、SQLite 图谱及持久化任务，不需要 Docker、Redis、PostgreSQL 或 Neo4j。

## 使用预构建包

- 桌面：安装与你的系统/CPU 对应的 Electron 包。首次运行在本机启动 Go 服务；按网页提示创建账户，再配置公司模型。菜单「本地 / 公司服务器设置」可切换到内网服务。
- Web / 服务器：解压 portable 包，Windows 运行 `start.cmd`，macOS/Linux 运行 `./start.sh`，浏览器访问 `http://127.0.0.1:8080`。
- 终端使用者不需要 npm、Go 或 Rust。构建依赖和 Electron 下载发生在制品制作机器。

服务器示例（安装目录只读、数据另存）：

```sh
./server --portable --resources-dir /opt/knowledge-assistant \
  --data-dir /srv/knowledge-assistant --host 0.0.0.0 --port 8080
```

Windows 同样支持这些参数，程序名为 `server.exe`。便携服务为单进程部署，同一数据目录只能由一个服务持有；请使用本机磁盘。共享服务器应设置公司 HTTPS 反向代理并由操作系统服务管理器负责重启。多副本、大型数据集可使用原有外部数据库部署方案。

数据目录含数据库、上传文件、加密密钥与日志。Electron 默认放在用户应用数据目录下，菜单可打开；不要把它写入安装目录。桌面首次自动选择空闲端口并保存，重启复用，若被占用会明确报错，不连接占用该端口的其他程序。

## 模型、文件、飞书与沙箱

配置公司 LLM、Embedding、重排和视觉模型的接口地址与凭据；模型权重不包含在安装包中。若私网端点被地址保护拒绝，由系统管理员在「系统设置 → 安全 → SSRF 防护白名单」加入实际模型/飞书主机，或由部署方配置 `SSRF_WHITELIST_EXTRA`；优先填写具体主机，不必放开整个内网。不同模型接口的兼容性需在公司环境验收。

制品链接 anydoc 原生解析器，包含中文分词词典，普通文字 PDF、DOC/DOCX、PPT/PPTX、XLSX 等无需 Python 文档服务。**扫描 PDF 的 OCR 回退仍需要配置可用的文档解析/OCR 服务；旧 XLS 不由 anydoc 支持，应转为 XLSX 或配置其他解析引擎。**不要把文字 PDF 验证结果等同于所有复杂文档均可解析。图片理解需要配置视觉模型。

保留飞书/Lark 的知识空间、云盘连接器，以及原有 IM 接入；可以配置企业私有域名。移除 GitLab、Notion、Confluence、语雀、钉钉、RSS、IMA **数据源连接器**与微信小程序客户端。

保留 Docker、E2B、Cube 沙箱适配器和工作空间配置。无 Docker 的电脑可以连接公司提供的远程沙箱；**本包不提供受限云桌面上的本机强隔离执行器**，也不会用普通 shell 冒充安全沙箱。沙箱服务及浏览器自动化等外部能力需要单独配置。

## 备份与恢复

先退出 Electron 或停止服务。完整备份包括数据库、文件和 `secrets.json`，后者缺失会导致已保存凭据无法解密；请按公司敏感数据要求保存备份。

```sh
./assistant-backup --data-dir /srv/knowledge-assistant --archive /backups/assistant.tar.gz backup
./assistant-backup --data-dir /srv/knowledge-assistant-restored --archive /backups/assistant.tar.gz restore
```

恢复目标必须不存在；恢复后先使用同一版本程序启动验证。运行中的数据目录拒绝备份。升级前停机备份，升级只替换程序资源，不覆盖数据目录。

## 制作安装包（开发机器）

需要 Node/npm、Go 1.26、Rust/Cargo、C/C++ 编译器以及 Bash。各平台原生构建；Windows 使用 MinGW-w64 与 Rust GNU target。构建机可从镜像或预热缓存取得依赖，最终安装机器无需访问任何包仓库。

```sh
npm ci --prefix frontend
npm ci --prefix desktop
node scripts/package-portable.mjs
npm run pack --prefix desktop       # 未签名可运行目录，用于验收
npm run dist --prefix desktop       # 各平台安装包
```

输出目录：`dist/portable/<platform>-<arch>` 与 `dist/electron`。Windows 的 platform 为 `win32`，macOS 为 `darwin`。镜像可通过 npm registry、GOPROXY、Cargo registry 和 ELECTRON_MIRROR 配置；Electron二进制不是普通npm包内容，仅有npm镜像时必须在外部构建机预先准备。SheetJS 固定版本随源码保存，避免构建依赖其公网 CDN。

跨平台流水线见 `.github/workflows/portable.yml`。本地 macOS 成功不能代替 Windows/Linux 的执行结果；未通过目标平台构建与实机验收前，不应将对应平台标为正式发布。签名证书由发行方配置，未签名包可能受到公司策略限制。
