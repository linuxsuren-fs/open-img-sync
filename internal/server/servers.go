package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/linuxsuren/open-img-sync/internal/crypt"
	"github.com/linuxsuren/open-img-sync/internal/errs"
	"github.com/linuxsuren/open-img-sync/internal/model"
	"github.com/linuxsuren/open-img-sync/internal/sshx"
)

// bucketServers 服务器持久化 bucket。
const bucketServers = "servers"

// view 把持久化记录转为 API 回显：凭据只给掩码与指纹，绝无明文。
func view(r model.ServerRecord) model.ServerView {
	v := model.ServerView{
		ID:                r.ID,
		Name:              r.Name,
		Host:              r.Host,
		Port:              r.Port,
		AuthType:          r.AuthType,
		Username:          r.Username,
		SecretFingerprint: r.SecretFingerprint,
		Tags:              r.Tags,
		CreatedAt:         r.CreatedAt,
		UpdatedAt:         r.UpdatedAt,
	}
	if r.SecretSealed != "" {
		v.SecretMask = "ssh-****"
	}
	if v.Tags == nil {
		v.Tags = []string{}
	}
	return v
}

// recordFromInput 由输入构造/更新持久化记录；secret 留空表示沿用原凭据。
func (s *Server) recordFromInput(r model.ServerRecord, in model.ServerInput, existing *model.ServerRecord) (model.ServerRecord, error) {
	if strings.TrimSpace(in.Name) == "" {
		return r, errs.New(errs.CodeBadRequest, "名称不能为空")
	}
	if strings.TrimSpace(in.Host) == "" {
		return r, errs.New(errs.CodeBadRequest, "主机地址不能为空")
	}
	if in.AuthType != model.AuthPassword && in.AuthType != model.AuthPrivateKey {
		return r, errs.New(errs.CodeBadRequest, "认证方式必须是 password 或 private_key")
	}
	if in.Port < 0 || in.Port > 65535 {
		return r, errs.New(errs.CodeBadRequest, "端口不合法")
	}
	if in.Port == 0 {
		in.Port = 22
	}
	if strings.TrimSpace(in.Username) == "" {
		return r, errs.New(errs.CodeBadRequest, "用户名不能为空")
	}

	r.Name = in.Name
	r.Host = in.Host
	r.Port = in.Port
	r.AuthType = in.AuthType
	r.Username = in.Username
	r.Tags = in.Tags
	if r.Tags == nil {
		r.Tags = []string{}
	}

	if in.Secret != "" {
		sealed, err := s.deps.Secrets.Seal(in.Secret)
		if err != nil {
			return r, err
		}
		r.SecretSealed = sealed
		r.SecretFingerprint = crypt.Fingerprint(in.Secret)
	} else if existing == nil {
		// 新建时必须提供凭据
		return r, errs.New(errs.CodeBadRequest, "凭据不能为空")
	}
	return r, nil
}

func (s *Server) handleServerCreate(w http.ResponseWriter, r *http.Request) {
	var in model.ServerInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		s.writeErr(w, errs.Wrap(errs.CodeBadRequest, "请求体不是合法 JSON", err))
		return
	}
	now := s.now()
	rec, err := s.recordFromInput(model.ServerRecord{ID: model.NewID("srv"), CreatedAt: now, UpdatedAt: now}, in, nil)
	if err != nil {
		s.writeErr(w, errs.From(err))
		return
	}
	if err := s.deps.DB.Put(bucketServers, rec.ID, mustJSON(rec)); err != nil {
		s.writeErr(w, errs.Wrap(errs.CodeInternal, "保存失败", err))
		return
	}
	writeJSON(w, http.StatusOK, view(rec))
}

func (s *Server) handleServerList(w http.ResponseWriter, r *http.Request) {
	page := model.NormalizePage(queryInt(r, "page", 1), queryInt(r, "page_size", 20))
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	tag := strings.TrimSpace(r.URL.Query().Get("tag"))

	var records []model.ServerRecord
	err := s.deps.DB.ForEach(bucketServers, func(_ string, value []byte) error {
		var rec model.ServerRecord
		if err := json.Unmarshal(value, &rec); err != nil {
			return err
		}
		if name != "" && !strings.Contains(rec.Name, name) {
			return nil
		}
		if tag != "" && !hasTag(rec.Tags, tag) {
			return nil
		}
		records = append(records, rec)
		return nil
	})
	if err != nil {
		s.writeErr(w, errs.Wrap(errs.CodeInternal, "读取失败", err))
		return
	}
	sort.Slice(records, func(i, j int) bool {
		if !records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].CreatedAt.Before(records[j].CreatedAt)
		}
		return records[i].ID < records[j].ID
	})
	total := len(records)
	start := (page.Page - 1) * page.PageSize
	if start > total {
		start = total
	}
	end := start + page.PageSize
	if end > total {
		end = total
	}
	items := make([]model.ServerView, 0, end-start)
	for _, rec := range records[start:end] {
		items = append(items, view(rec))
	}
	writeJSON(w, http.StatusOK, model.PageEnvelope[model.ServerView]{Data: items, Total: total, Page: page.Page, PageSize: page.PageSize})
}

func (s *Server) loadServer(r *http.Request) (model.ServerRecord, *errs.E) {
	id := r.PathValue("id")
	raw, err := s.deps.DB.Get(bucketServers, id)
	if err != nil {
		return model.ServerRecord{}, errs.Wrap(errs.CodeInternal, "读取失败", err)
	}
	if raw == nil {
		return model.ServerRecord{}, errs.New(errs.CodeNotFound, "服务器不存在")
	}
	var rec model.ServerRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return model.ServerRecord{}, errs.Wrap(errs.CodeInternal, "数据损坏", err)
	}
	return rec, nil
}

func (s *Server) handleServerGet(w http.ResponseWriter, r *http.Request) {
	rec, e := s.loadServer(r)
	if e != nil {
		s.writeErr(w, e)
		return
	}
	writeJSON(w, http.StatusOK, view(rec))
}

func (s *Server) handleServerUpdate(w http.ResponseWriter, r *http.Request) {
	rec, e := s.loadServer(r)
	if e != nil {
		s.writeErr(w, e)
		return
	}
	var in model.ServerInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		s.writeErr(w, errs.Wrap(errs.CodeBadRequest, "请求体不是合法 JSON", err))
		return
	}
	updated, err := s.recordFromInput(rec, in, &rec)
	if err != nil {
		s.writeErr(w, errs.From(err))
		return
	}
	updated.UpdatedAt = s.now()
	if err := s.deps.DB.Put(bucketServers, updated.ID, mustJSON(updated)); err != nil {
		s.writeErr(w, errs.Wrap(errs.CodeInternal, "保存失败", err))
		return
	}
	writeJSON(w, http.StatusOK, view(updated))
}

func (s *Server) handleServerDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.deps.DB.Get(bucketServers, id); err != nil {
		s.writeErr(w, errs.Wrap(errs.CodeInternal, "读取失败", err))
		return
	}
	if err := s.deps.DB.Delete(bucketServers, id); err != nil {
		s.writeErr(w, errs.Wrap(errs.CodeInternal, "删除失败", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// handleServerTest 连通性测试：SSH 握手 + Docker 探测，结果带稳定错误码。
func (s *Server) handleServerTest(w http.ResponseWriter, r *http.Request) {
	rec, e := s.loadServer(r)
	if e != nil {
		s.writeErr(w, e)
		return
	}
	secret, err := s.deps.Secrets.Open(rec.SecretSealed)
	if err != nil {
		s.writeErr(w, errs.From(err))
		return
	}
	opt := sshx.Options{Host: rec.Host, Port: rec.Port, Username: rec.Username, AuthType: rec.AuthType}
	switch rec.AuthType {
	case model.AuthPassword:
		opt.Password = secret
	case model.AuthPrivateKey:
		opt.PrivateKey = secret
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.opt.TestTimeout)
	defer cancel()
	res := s.sshTester(ctx, opt)
	slog.Info("server test", "server_id", rec.ID, "code", res.Code) // 不落任何凭据
	writeJSON(w, http.StatusOK, res)
}

// handleServerExport 导出全部服务器；凭据以加密块呈现，绝无明文。
func (s *Server) handleServerExport(w http.ResponseWriter, _ *http.Request) {
	items := make([]model.ServerExportItem, 0)
	err := s.deps.DB.ForEach(bucketServers, func(_ string, value []byte) error {
		var rec model.ServerRecord
		if err := json.Unmarshal(value, &rec); err != nil {
			return err
		}
		item := model.ServerExportItem{
			Name: rec.Name, Host: rec.Host, Port: rec.Port,
			AuthType: rec.AuthType, Username: rec.Username,
			SecretSealed: rec.SecretSealed, Tags: rec.Tags,
		}
		if item.Tags == nil {
			item.Tags = []string{}
		}
		items = append(items, item)
		return nil
	})
	if err != nil {
		s.writeErr(w, errs.Wrap(errs.CodeInternal, "导出失败", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

// handleServerImport 批量导入；SecretSealed 加密块原样迁移，凭据无需明文往返。
func (s *Server) handleServerImport(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Data []model.ServerExportItem `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		s.writeErr(w, errs.Wrap(errs.CodeBadRequest, "请求体不是合法 JSON", err))
		return
	}
	results := make([]map[string]any, 0, len(payload.Data))
	now := s.now()
	for i, item := range payload.Data {
		result := map[string]any{"line": i + 1, "name": item.Name}
		if strings.TrimSpace(item.Name) == "" || strings.TrimSpace(item.Host) == "" {
			result["ok"] = false
			result["reason"] = "名称或主机地址为空"
			results = append(results, result)
			continue
		}
		port := item.Port
		if port == 0 {
			port = 22
		}
		rec := model.ServerRecord{
			ID: model.NewID("srv"), Name: item.Name, Host: item.Host, Port: port,
			AuthType: item.AuthType, Username: item.Username,
			SecretSealed: item.SecretSealed, Tags: item.Tags,
			CreatedAt: now, UpdatedAt: now,
		}
		if rec.Tags == nil {
			rec.Tags = []string{}
		}
		// 有加密块则解密重算指纹（导入侧持有主密钥）；无则凭据留空待补录
		if rec.SecretSealed != "" {
			plain, err := s.deps.Secrets.Open(rec.SecretSealed)
			if err != nil {
				result["ok"] = false
				result["reason"] = "凭据加密块无法解密（主密钥不一致）"
				results = append(results, result)
				continue
			}
			rec.SecretFingerprint = crypt.Fingerprint(plain)
		}
		if err := s.deps.DB.Put(bucketServers, rec.ID, mustJSON(rec)); err != nil {
			result["ok"] = false
			result["reason"] = "保存失败"
			results = append(results, result)
			continue
		}
		result["ok"] = true
		result["id"] = rec.ID
		results = append(results, result)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": results})
}

func hasTag(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}

func queryInt(r *http.Request, key string, fallback int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

func mustJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		// 记录结构只含可序列化字段，此路径不可达；防御性处理
		panic(err)
	}
	return raw
}
