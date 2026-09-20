package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"relayhub/internal/checkin"
	"time"
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
	results := []outcome{}
	found := input.ChannelID == ""
	ctx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
	defer cancel()
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
		if ctx.Err() != nil {
			s.fail(w, r, unavailable("check-in request timed out"))
			return
		}
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
		results = append(results, item)
	}
	if !found {
		s.fail(w, r, notFound("channel not found"))
		return
	}
	s.auditEvent(r.Context(), "checkin_run", r, map[string]string{"channel_id": input.ChannelID})
	s.write(w, r, http.StatusOK, map[string]any{"results": results})
}
