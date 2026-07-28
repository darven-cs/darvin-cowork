package logger

type Config struct {
	Level      string
	Encoding   string
	Output     string
	Filename   string
	MaxSize    int
	MaxBackups int
	MaxAge     int
}
