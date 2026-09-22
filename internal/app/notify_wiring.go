package app

import (
	"context"
	"fmt"
	"time"

	"relayhub/internal/domain"
	"relayhub/internal/health"
	"relayhub/internal/notify"
)

// defaultQuotaLowUSD is the balance below which a funded channel triggers a
// quota_low notification when the operator did not configure a threshold.
const defaultQuotaLowUSD = 0.5

// channelLookup resolves a channel ID to a human label (name, else ID).
type channelLookup func(id string) string

func repoChannelLookup(repo interface {
	GetChannel(context.Context, string) (domain.Channel, error)
}) channelLookup {
	return func(id string) string {
		if repo == nil {
			return id
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if ch, err := repo.GetChannel(ctx, id); err == nil && ch.Name != "" {
			return ch.Name
		}
		return id
	}
}

// composeHealthObservers runs several observers for one transition.
func composeHealthObservers(fns ...func(health.Transition)) func(health.Transition) {
	return func(tr health.Transition) {
		for _, fn := range fns {
			if fn != nil {
				fn(tr)
			}
		}
	}
}

// healthNotifier maps routing-state transitions onto operator events:
// breaker open, auth expired, quota exhausted, and recovery from any of them.
func healthNotifier(d *notify.Dispatcher, label channelLookup) func(health.Transition) {
	return func(tr health.Transition) {
		if !d.Enabled() {
			return
		}
		name := label(tr.ChannelID)
		switch tr.To {
		case health.CircuitOpen:
			body := fmt.Sprintf("连续失败已触发熔断，冷却至 %s", tr.State.CooldownUntil.Local().Format("15:04:05"))
			if tr.State.LastError != "" {
				body += "\n最近错误：" + tr.State.LastError
			}
			d.Notify(notify.Event{Kind: notify.KindBreakerOpen, Severity: notify.SeverityError, Title: "渠道熔断：" + name, Body: body, ChannelID: tr.ChannelID, At: tr.At})
		case health.AuthExpired:
			d.Notify(notify.Event{Kind: notify.KindAuthExpired, Severity: notify.SeverityError, Title: "凭据失效：" + name, Body: "上游返回认证失败，请重新登录或更新 Key", ChannelID: tr.ChannelID, At: tr.At})
		case health.QuotaExhausted:
			d.Notify(notify.Event{Kind: notify.KindQuotaExhausted, Severity: notify.SeverityWarning, Title: "额度耗尽：" + name, Body: "余额为 0，路由已自动跳过该渠道", ChannelID: tr.ChannelID, At: tr.At})
		case health.Healthy:
			if tr.From == health.CircuitOpen || tr.From == health.CircuitHalfOpen || tr.From == health.QuotaExhausted || tr.From == health.AuthExpired {
				d.Notify(notify.Event{Kind: notify.KindRecovered, Severity: notify.SeverityInfo, Title: "渠道恢复：" + name, Body: fmt.Sprintf("由 %s 恢复为 healthy", tr.From), ChannelID: tr.ChannelID, At: tr.At})
			}
		}
	}
}

// quotaLowNotifier emits quota_low when a known balance drops under the
// threshold (exhaustion itself is reported by the health transition).
func quotaLowNotifier(d *notify.Dispatcher, threshold float64) func(domain.Channel, domain.QuotaSnapshot) {
	if threshold <= 0 {
		threshold = defaultQuotaLowUSD
	}
	return func(ch domain.Channel, q domain.QuotaSnapshot) {
		if !d.Enabled() || !q.Known() || q.AvailableUSD <= 0 || q.AvailableUSD >= threshold {
			return
		}
		name := ch.Name
		if name == "" {
			name = ch.ID
		}
		d.Notify(notify.Event{
			Kind:      notify.KindQuotaLow,
			Severity:  notify.SeverityWarning,
			Title:     "额度告急：" + name,
			Body:      fmt.Sprintf("剩余 $%.2f（阈值 $%.2f）", q.AvailableUSD, threshold),
			ChannelID: ch.ID,
			Fields:    map[string]string{"available_usd": fmt.Sprintf("%.2f", q.AvailableUSD), "source": q.Source},
			At:        q.UpdatedAt,
		})
	}
}
