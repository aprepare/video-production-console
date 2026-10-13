package main

import (
	"context"
	"log/slog"

	"video-production-console/internal/settings"
)

type startupActivation interface {
	Get(context.Context) (settings.View, error)
	AutoActivate(context.Context, string) (bool, error)
}
type startupJianying interface {
	AutoDetectJianying(context.Context) (bool, error)
}

func initializeSetup(ctx context.Context, activation startupActivation, detector startupJianying, logger *slog.Logger) {
	// 数据库可能保留了不同于本次启动默认值的数据目录；此时尚不能冻结 Runtime。
	view, err := activation.Get(ctx)
	if err != nil {
		logger.Warn("首启配置读取失败，请在设置中重新激活；继续启动控制台")
	} else if applied, err := activation.AutoActivate(ctx, view.Public.DataRoot); err != nil {
		// 配置码可能包含密钥和带签名的地址，启动日志不附加原始错误或文件内容。
		logger.Warn("首启配置码应用失败，请在设置中重新激活；继续启动控制台")
	} else if applied {
		logger.Info("已应用安装包中的配置码")
	}
	if applied, err := detector.AutoDetectJianying(ctx); err != nil {
		logger.Warn("剪映自动配置未完成，请在设置中重新检测；继续启动控制台")
	} else if applied {
		logger.Info("已自动配置唯一的剪映草稿目录")
	}
}
