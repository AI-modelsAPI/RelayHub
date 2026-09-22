package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"relayhub/internal/checkin"
	"relayhub/internal/domain"
)

func (s *Server) runCheckin(w http.ResponseWriter, r *http.Request) {
	if s.Scheduler == nil || s.Repo == nil {
		s.fail(w, r, unsupported("scheduler not configured"))
		return
	}
	var input struct {
		ChannelID string `json:"channel_id"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		s.fail(w, r, err)
		return
	}
	channels, err := s.Repo.ListChannels(r.Context(), "")
	if err != nil {
		s.fail(w, r, internal(err))
		return
	}
	type outcome struct {
		ChannelID       string `json:"channel_id"`
		Status          string `json:"status"`
		ExecutionStatus string `json:"execution_status,omitempty"`
		Message         string `json:"message,omitempty"`
		ManualURL       string `json:"manual_url,omitempty"`
	}
	// Fan out per channel: the old serial loop gave a single global timeout to
	// a whole fleet, so one slow site starved every later channel and the
	// caller received a partial, mixed result (RH-16/P2-9). Each run keeps an
	// independent deadline and a hard gate bounds total concurrency.
	found := input.ChannelID == ""
	targets := []domain.Channel{}
	for _, ch := range channels {
		if input.ChannelID != "" && input.ChannelID != ch.ID {
			continue
		}
		found = true
		if !ch.Enabled || !ch.CheckinEnabled {
			if input.ChannelID != "" {
				s.fail(w, r, badRequest("checkin_disabled", "channel check-in is disabled"))
				return
			}
			continue
		}
		targets = append(targets, ch)
	}
	results := make([]outcome, len(targets))
	gate := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, ch := range targets {
		wg.Add(1)
		go func(idx int, ch domain.Channel) {
			defer wg.Done()
			gate <- struct{}{}
			defer func() { <-gate }()
			ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
			defer cancel()
			err := s.Scheduler.RunNow(ctx, ch.ID)
			item := outcome{ChannelID: ch.ID, Status: "failed"}
			switch {
			case errors.Is(err, checkin.ErrNeedManual):
				item.Status = "need_manual"
				item.Message = "请打开站点完成签到；尚未确认服务端奖励"
				u, e := url.Parse(ch.BaseURL)
				if e == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil {
					item.ManualURL = u.String()
				}
			case err != nil:
				st := s.Scheduler.GetState(ch.ID)
				if st.Status == checkin.StatusPersistenceFailed {
					item.Status = string(st.Status)
					item.ExecutionStatus = string(st.ExecutionStatus)
					item.Message = "签到结果保存失败；请先核对站点结果，不要直接重试"
				} else if errors.Is(err, context.DeadlineExceeded) {
					item.Message = "渠道签到超时（60s）；其他渠道不受影响"
				} else {
					item.Message = "签到未完成，请查看服务日志；未标记成功"
				}
			default:
				st := s.Scheduler.GetState(ch.ID)
				if st.Status == checkin.StatusSuccess {
					item.Status = "success"
				} else {
					item.Message = "站点未确认签到成功"
				}
			}
			results[idx] = item
		}(i, ch)
	}
	wg.Wait()
	if !found {
		s.fail(w, r, notFound("channel not found"))
		return
	}
	s.auditEvent(r.Context(), "checkin_run", r, map[string]string{"channel_id": input.ChannelID})
	s.write(w, r, http.StatusOK, map[string]any{"results": results})
}
