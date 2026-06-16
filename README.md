# GACOS 自动下载器

用于自动向 [GACOS](http://www.gacos.net/) 提交大气校正数据请求，并从邮箱自动下载 `.tar.gz` 结果的 Go 命令行工具。

## 功能

- 读取配置中的 SAR 成像日期列表，自动按每组 ≤20 个切分提交（GACOS 单次限制）。
- 通过普通 HTTP POST 模拟提交 GACOS 请求表单。
- 使用 IMAP 定期读取邮箱，自动提取邮件正文中的 `.tar.gz` 下载链接。
- 使用 Go 原生 HTTP 下载文件，并自动解压。
- 通过 SQLite 数据库 `state.db` 记录提交、下载、解压进度，支持断点续传和避免重复提交。

## 环境要求

- Go 1.21 或更高版本
- 一个支持 IMAP 的邮箱（如 Gmail、Outlook、QQ 邮箱等）

## 安装

```bash
cd gacos-scraper
go mod tidy
go build -o gacos-scraper.exe ./cmd/gacos-scraper
```

## 配置

复制示例配置并修改：

```bash
cp config.example.yaml config.yaml
```

重点字段说明：

| 字段 | 说明 |
|------|------|
| `gacos.north/south/west/east` | 目标区域经纬度边界 |
| `gacos.hour/minute` | UTC 成像时间 |
| `gacos.dates` | 日期列表，格式 `YYYYMMDD` |
| `gacos.type` | `2`=GeoTIFF，`1`=二进制网格 |
| `gacos.email` | 接收 GACOS 通知的邮箱 |
| `imap.server/username/password` | IMAP 服务器与登录凭据 |
| `output.dir` | 下载/解压输出目录 |
| `polling.interval` | 轮询邮箱间隔 |

常用邮箱 IMAP 设置参考：

| 邮箱 | IMAP 服务器 | 密码说明 |
|------|-------------|----------|
| 163 | `imap.163.com:993` | 填**授权码**，不是网页登录密码。若证书验证失败，设 `imap.skip_tls_verify: true` |
| Gmail | `imap.gmail.com:993` | 填**应用专用密码**（App Password） |
| QQ | `imap.qq.com:993` | 填**授权码** |
| Outlook | `outlook.office365.com:993` | 邮箱密码 |

日期也可以单独放到文本文件中（每行一个），通过 `--dates dates.txt` 加载。

## 使用

本工具默认以**守护进程模式**运行：启动后会先提交所有日期组，然后一直检查邮箱，收到 GACOS 下载邮件就立即下载并解压，直到按 `Ctrl+C` 退出。

```bash
./gacos-scraper.exe --config config.yaml
```

提交和邮箱轮询是**并行执行**的，因此前面几组的下载邮件到了就能立刻处理，不用等所有组都提交完。

### 查看当前进度

```bash
./gacos-scraper.exe --config config.yaml --status
```

输出示例：

```text
State summary:
  Submissions: 1 total, 1 pending, 0 failed
  Downloads:   2 total, 0 pending, 0 failed
  Extractions: 2 total, 0 failed
```

### 查看历史任务列表

```bash
./gacos-scraper.exe --config config.yaml --tasks
```

### 调试日志

```bash
./gacos-scraper.exe --config config.yaml --verbose
```

## 输出目录结构

```
downloads/
├── staging/        # 下载的原始 .tar.gz
└── extracted/      # 解压后的文件
state.db            # SQLite 任务状态数据库
```

## 常见问题

**Q: 163 邮箱登录失败？**  
A: 163 必须使用**客户端授权码**（在网页版“设置 → POP3/SMTP/IMAP → 客户端授权密码”中开启）。`imap.password` 填这个授权码，不是 163 网页登录密码。

**Q: Gmail 登录失败？**  
A: 需要开启 IMAP，并使用“应用专用密码”（App Password），不能直接用 Gmail 网页密码。

**Q: 程序会把邮件标为已读吗？**  
A: 只有当邮件中的链接被成功下载后，才会标记为已读；失败或跳过的邮件保持未读。

**Q: 支持增量运行吗？**  
A: 支持。`state.db` 会记录已提交的日期组、已下载的 URL、已解压的压缩包，重启后会自动跳过。

## 许可证

MIT
