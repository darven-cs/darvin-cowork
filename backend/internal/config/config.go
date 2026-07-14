package config

// HttpBasePath 是后端 HTTP 路由的统一前缀，所有 handler 都挂在这下面。
const HttpBasePath = "/api"

// Config 是后端启动所需的运行时配置。
//
// v0.1 第 3 周接入 Viper 之前，字段以硬编码默认值填充。
type Config struct {
	// ListenAddr 是 HTTP server 监听地址，默认 127.0.0.1:8080。
	ListenAddr string
}

// Default 返回内置默认配置。
//
// v0.1 第 3 周接入 Viper 后，Default 会改为从配置文件 / 环境变量加载。
func Default() Config {
	return Config{
		ListenAddr: "127.0.0.1:8080",
	}
}
