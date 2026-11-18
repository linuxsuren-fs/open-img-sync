package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/linuxsuren/open-img-sync/internal/errs"
	"github.com/linuxsuren/open-img-sync/internal/model"
)

const bucketTasksAPI = "tasks"

func (s *Server) handleTaskCreate(w http.ResponseWriter, r *http.Request) {
	var in model.TaskInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		s.writeErr(w, errs.Wrap(errs.CodeBadRequest, "请求体不是合法 JSON", err))
		return
	}
	task, err := s.deps.Sync.CreateTask(in, time.Now())
	if err != nil {
		s.writeErr(w, errs.From(err))
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleTaskList(w http.ResponseWriter, r *http.Request) {
	page := model.NormalizePage(queryInt(r, "page", 1), queryInt(r, "page_size", 20))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	tasks := s.deps.Sync.ListTasks()
	if status != "" {
		var filtered []model.TaskRecord
		for _, t := range tasks {
			if t.Status == status {
				filtered = append(filtered, t)
			}
		}
		tasks = filtered
	}
	total := len(tasks)
	start := (page.Page - 1) * page.PageSize
	if start > total {
		start = total
	}
	end := start + page.PageSize
	if end > total {
		end = total
	}
	items := make([]model.TaskRecord, 0, end-start)
	for _, t := range tasks[start:end] {
		if len(t.Steps) > 5 { // 列表瘦身：只带前 5 步
			t.Steps = t.Steps[:5]
		}
		items = append(items, t)
	}
	writeJSON(w, http.StatusOK, model.PageEnvelope[model.TaskRecord]{Data: items, Total: total, Page: page.Page, PageSize: page.PageSize})
}

func (s *Server) handleTaskGet(w http.ResponseWriter, r *http.Request) {
	task, ok := s.deps.Sync.GetTask(r.PathValue("id"))
	if !ok {
		s.writeErr(w, errs.New(errs.CodeNotFound, "任务不存在"))
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleTaskCancel(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Sync.Cancel(r.PathValue("id")); err != nil {
		s.writeErr(w, errs.From(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"canceled": true})
}

// handleTaskLogs 日志查看：默认 SSE 流式；带 ?since=<seq> 时退化为轮询 JSON。
func (s *Server) handleTaskLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	flusher, canFlush := w.(http.Flusher)

	if r.URL.Query().Get("since") != "" || !canFlush {
		var since int
		fmt.Sscanf(r.URL.Query().Get("since"), "%d", &since)
		lines, ok := s.deps.Sync.LogsSince(id, since)
		if !ok {
			s.writeErr(w, errs.New(errs.CodeNotFound, "任务不存在"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": lines})
		return
	}

	if _, ok := s.deps.Sync.LogsSince(id, 0); !ok {
		s.writeErr(w, errs.New(errs.CodeNotFound, "任务不存在"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	lastSeq := 0
	deadline := time.Now().Add(30 * time.Minute)
	for {
		if task, ok := s.deps.Sync.GetTask(id); !ok || isTerminal(task.Status) {
			// 任务结束后补发剩余行并关闭
			lines, _ := s.deps.Sync.LogsSince(id, lastSeq)
			for _, ln := range lines {
				fmt.Fprintf(w, "data: %s\n\n", mustJSON(ln))
				lastSeq = ln.Seq
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		lines, _ := s.deps.Sync.LogsSince(id, lastSeq)
		for _, ln := range lines {
			fmt.Fprintf(w, "data: %s\n\n", mustJSON(ln))
			lastSeq = ln.Seq
		}
		if len(lines) > 0 {
			flusher.Flush()
		}
		if time.Now().After(deadline) {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func isTerminal(status string) bool {
	switch status {
	case model.TaskSuccess, model.TaskFailed, model.TaskCanceled:
		return true
	}
	return false
}
