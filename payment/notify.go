package payment

import (
	"context"
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/god-jason/goframe/lock"
	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/logger"
)

// notifyKeyPrefix 是回调幂等键前缀。
const notifyKeyPrefix = "goframe:payment:notify:"

// HandleNotify 处理渠道异步回调：验签解析 → 幂等去重 → 分发业务 handler。
// 返回的 error 中，lock.ErrLocked 表示同一回调正在处理，应应答失败让渠道重试。
func (m *Manager) HandleNotify(ctx context.Context, name ChannelName, r *http.Request) (*NotifyResult, error) {
	ch := m.Channel(name)
	if ch == nil {
		return nil, apperrors.NotFound("payment channel not enabled: " + string(name))
	}

	result, err := ch.ParseNotify(ctx, r)
	if err != nil {
		logger.Get().Warn("payment notify verify failed",
			logger.String("channel", string(name)),
			logger.Error(err),
		)
		return nil, apperrors.Unavailable("payment notify verify failed").WithCause(err)
	}

	if err := m.dispatch(ctx, result); err != nil {
		return result, err
	}
	return result, nil
}

// dispatch 执行幂等检查与处理器分发。
func (m *Manager) dispatch(ctx context.Context, result *NotifyResult) error {
	base := notifyKeyPrefix + string(result.Channel) + ":" + result.IdempotencyKey

	// 已处理完成的回调直接成功，避免渠道重试导致业务重复执行。
	if m.store != nil {
		if done, err := m.store.Exists(ctx, base+":done"); err == nil && done {
			return nil
		}
	}

	unlock, err := m.locker.TryLock(ctx, base+":processing", m.processingTTL)
	if err != nil {
		if err == lock.ErrLocked {
			logger.Get().Warn("payment notify already processing",
				logger.String("channel", string(result.Channel)),
				logger.String("out_trade_no", result.OutTradeNo),
			)
		}
		return err
	}
	defer func() {
		if err := unlock(context.WithoutCancel(ctx)); err != nil {
			logger.Get().Debug("payment notify unlock failed", logger.Error(err))
		}
	}()

	handlers := handlersFor(result.Channel)
	if len(handlers) == 0 {
		logger.Get().Error("payment notify has no handler registered",
			logger.String("channel", string(result.Channel)),
			logger.String("out_trade_no", result.OutTradeNo),
		)
		return apperrors.Unavailable("payment notify handler is not registered")
	}

	for i, handler := range handlers {
		if err := callHandler(ctx, handler, result); err != nil {
			logger.Get().Warn("payment notify handler failed",
				logger.String("channel", string(result.Channel)),
				logger.String("out_trade_no", result.OutTradeNo),
				logger.Int("handler", i),
				logger.Error(err),
			)
			return fmt.Errorf("payment: notify handler %d: %w", i, err)
		}
	}

	if m.store != nil {
		if err := m.store.Set(ctx, base+":done", "1", m.doneTTL); err != nil {
			logger.Get().Warn("payment notify mark done failed", logger.Error(err))
		}
	}
	return nil
}

// callHandler 执行单个处理器，把 panic 转换为错误，避免拖垮回调进程。
func callHandler(ctx context.Context, handler NotifyHandler, result *NotifyResult) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Get().Error("payment notify handler panic",
				logger.String("channel", string(result.Channel)),
				logger.String("panic", fmt.Sprint(recovered)),
				logger.String("stack", string(debug.Stack())),
			)
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return handler(ctx, result)
}
