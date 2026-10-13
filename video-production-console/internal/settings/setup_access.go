package settings

import "context"

// ReadSecret 供配置体检读取最新保存的密钥，不改变当前进程的运行快照。
// 返回值只用于服务端请求，不得放入 HTTP 响应或日志。
func (s *Service) ReadSecret(ctx context.Context, key string) (string, error) {
	if err := validateSecretUpdate(key, ""); err != nil {
		return "", err
	}
	value, _, _, err := s.secretValue(ctx, key)
	return value, err
}
