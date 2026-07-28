# Go 基础项目框架设计文档

## 1. 概述

### 1.1 问题 / 背景
当前项目（Electron + Vue3 + Go）需要为 Go 后端建立一套基础框架，包含日志、配置、数据库三个核心基础设施，供后续业务开发复用。

### 1.2 目标
搭建可用的 Go 基础项目骨架，提供：
- **日志系统**：支持分级输出（debug/info/warn/error）、日志轮转、多种输出目标
- **配置读取系统**：支持 YAML 配置文件、环境变量覆盖
- **数据库系统**：支持 MySQL/PostgreSQL，集成 gorm，提供连接池管理

### 1.3 非目标
- 不实现 HTTP Server / RPC 框架
- 不实现业务层代码
- 不做 Docker / K8s 部署配置

## 2. 实现方案

### 2.1 项目结构

```
src/darvin-agent/
├── cmd/
│   └── app/
│       └── main.go           # 应用入口
├── config/
│   └── config.go             # 配置加载逻辑
├── internal/
│   ├── database/
│   │   └── mysql.go          # MySQL 连接
│   ├── logger/
│   │   └── logger.go         # 日志封装
│   └── config/
│       └── config.go         # 配置结构体定义
├── pkg/
│   └── utils/                # 公共工具（可选）
├── config.yaml               # 配置文件
├── go.mod
└── go.sum
```

### 2.2 日志系统（logger）

使用 `uber-go/zap@v1.28.0` 实现：

- **分级日志**：`Debug() / Info() / Warn() / Error() / Fatal()`
- **输出格式**：JSON 格式（生产）/ Console 格式（开发）
- **日志轮转**：按大小或时间切割，使用 ` LumberjackLogger`
- **输出目标**：stdout + 文件（可选）
- **调用方式**：`logger.Info("message", "field", "value")`

### 2.3 配置读取系统（config）

使用 `spf13/viper@v1.20.1` 实现：

- **配置来源优先级**：环境变量 > config.yaml
- **支持类型**：string / int / bool / slice / map
- **配置结构**：
  ```go
  type Config struct {
    App     AppConfig
    Database DatabaseConfig
    Log     LogConfig
  }
  type DatabaseConfig struct {
    DSN      string  // SQLite 数据文件路径，如 ./data.db
  }
  ```

### 2.4 数据库系统（database）

使用 `gorm@v1.20.0` + `gorm.io/driver/sqlite`：

- **连接池**：SQLite 为单文件数据库，通过配置控制连接参数
- **初始化**：单例模式，应用启动时初始化
- **GORM 回调**：集成 logger，自动记录 SQL
- **Migrations**：提供基础 AutoMigrate 能力

## 3. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/darvin-agent/go.mod` | 新增 go.mod，依赖 zap@v1.28.0/viper@v1.20.1/gorm@v1.20.0/sqlite driver |
| `src/darvin-agent/cmd/app/main.go` | 应用入口，组装各组件 |
| `src/darvin-agent/internal/logger/logger.go` | 日志封装 |
| `src/darvin-agent/internal/config/config.go` | 配置结构体定义 + 加载 |
| `src/darvin-agent/internal/database/sqlite.go` | SQLite 连接封装 |
| `src/darvin-agent/config.yaml` | 配置文件 |

## 4. 验收标准

- [ ] `go build ./...` 编译通过，无报错
- [ ] 日志系统可输出 debug/info/warn/error 分级日志
- [ ] 配置文件可从 YAML 加载，且环境变量可覆盖
- [ ] 数据库连接成功，gorm auto migrate 可执行
- [ ] main.go 可正常启动并初始化三个系统
