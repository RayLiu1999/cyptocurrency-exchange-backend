package logger

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Log *zap.Logger

func init() {
	var err error
	Log, err = NewLogger()
	if err != nil {
		panic(err)
	}
}

// NewLogger 初始化 Logger
func NewLogger() (*zap.Logger, error) {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder

	// 使用 JSON 格式，方便 ELK/Splunk 收集
	encoder := zapcore.NewJSONEncoder(encoderConfig)

	core := zapcore.NewCore(
		encoder,
		zapcore.AddSync(os.Stdout),
		zap.InfoLevel, // 預設 Log Level
	)

	return zap.New(core, zap.AddCaller()), nil
}

// Info 快速記錄 Info
func Info(msg string, fields ...zap.Field) {
	Log.Info(msg, fields...)
}

// Error 快速記錄 Error
func Error(msg string, fields ...zap.Field) {
	Log.Error(msg, fields...)
}

// Warn 快速記錄 Warn
func Warn(msg string, fields ...zap.Field) {
	Log.Warn(msg, fields...)
}

// Debug 快速記錄 Debug
func Debug(msg string, fields ...zap.Field) {
	Log.Debug(msg, fields...)
}

// Sync 確保緩衝區寫入
func Sync() {
	_ = Log.Sync()
}
