package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/linuxsuren/open-img-sync/internal/crypt"
	"github.com/linuxsuren/open-img-sync/internal/errs"
	"github.com/linuxsuren/open-img-sync/internal/model"
	"github.com/linuxsuren/open-img-sync/internal/registry"
)

// bucket 常量。
const (
	bucketRegistries = "registries"
	bucketImages     = "images"
)

func registryView(r model.RegistryRecord) model.RegistryView {
	v := model.RegistryView{
		ID: r.ID, Name: r.Name, Address: r.Address, Type: r.Type, Role: r.Role,
		Username: r.Username, SecretFingerprint: r.SecretFingerprint,
		Aliyun:    model.AliyunView{Region: r.Aliyun.Region, Instance: r.Aliyun.Instance},
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if r.SecretSealed != "" {
		v.SecretMask = "reg-****"
	}
	if r.Aliyun.AKSealed != "" {
		v.Aliyun.AKMask = "ak-****"
		v.Aliyun.AKFingerprint = r.Aliyun.AKFingerprint
	}
	return v
}

func (s *Server) validateRegistryInput(in model.RegistryInput, existing *model.RegistryRecord) error {
	if strings.TrimSpace(in.Name) == "" {
		return errs.New(errs.CodeBadRequest, "名称不能为空")
	}
	if strings.TrimSpace(in.Address) == "" {
		return errs.New(errs.CodeBadRequest, "地址不能为空")
	}
	switch in.Type {
	case model.RegistryTypeStandard, model.RegistryTypeAliyunTemp, model.RegistryTypeAnonymous:
	default:
		return errs.New(errs.CodeBadRequest, "类型必须是 standard、aliyun-temp 或 anonymous")
	}
	switch in.Role {
	case model.RegistryRoleSource, model.RegistryRoleRelay, "": // 缺省 source，由调用方补全
	default:
		return errs.New(errs.CodeBadRequest, "角色必须是 source 或 relay")
	}
	if in.Type == model.RegistryTypeStandard {
		if strings.TrimSpace(in.Username) == "" {
			return errs.New(errs.CodeBadRequest, "standard 类型必须提供用户名")
		}
		if existing == nil && strings.TrimSpace(in.Secret) == "" {
			return errs.New(errs.CodeBadRequest, "standard 类型必须提供密码或 Token")
		}
	}
	if in.Type == model.RegistryTypeAliyunTemp {
		if existing == nil {
			if strings.TrimSpace(in.Aliyun.AccessKeyID) == "" || strings.TrimSpace(in.Aliyun.AccessKeySecret) == "" {
				return errs.New(errs.CodeBadRequest, "aliyun-temp 类型必须提供 AccessKey ID 与 Secret")
			}
		}
	}
	return nil
}

func (s *Server) handleRegistryCreate(w http.ResponseWriter, r *http.Request) {
	var in model.RegistryInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		s.writeErr(w, errs.Wrap(errs.CodeBadRequest, "请求体不是合法 JSON", err))
		return
	}
	if err := s.validateRegistryInput(in, nil); err != nil {
		s.writeErr(w, errs.From(err))
		return
	}
	now := s.now()
	rec := model.RegistryRecord{
		ID: model.NewID("reg"), Name: in.Name,
		Address: registry.NormalizeAddress(in.Address),
		Type:    in.Type, Role: in.Role, Username: in.Username,
		CreatedAt: now, UpdatedAt: now,
	}
	if rec.Role == "" {
		rec.Role = model.RegistryRoleSource
	}
	if err := s.applyRegistrySecrets(&rec, in); err != nil {
		s.writeErr(w, errs.From(err))
		return
	}
	if err := s.deps.DB.Put(bucketRegistries, rec.ID, mustJSON(rec)); err != nil {
		s.writeErr(w, errs.Wrap(errs.CodeInternal, "保存失败", err))
		return
	}
	writeJSON(w, http.StatusOK, registryView(rec))
}

// applyRegistrySecrets 按“留空不变更”语义写入凭据密文与指纹。
func (s *Server) applyRegistrySecrets(rec *model.RegistryRecord, in model.RegistryInput) error {
	if in.Secret != "" {
		sealed, err := s.deps.Secrets.Seal(in.Secret)
		if err != nil {
			return err
		}
		rec.SecretSealed = sealed
		rec.SecretFingerprint = crypt.Fingerprint(in.Secret)
	}
	if in.Aliyun.AccessKeyID != "" {
		akSealed, err := s.deps.Secrets.Seal(in.Aliyun.AccessKeyID)
		if err != nil {
			return err
		}
		rec.Aliyun.AKSealed = akSealed
		rec.Aliyun.AKFingerprint = crypt.Fingerprint(in.Aliyun.AccessKeyID)
	}
	if in.Aliyun.AccessKeySecret != "" {
		skSealed, err := s.deps.Secrets.Seal(in.Aliyun.AccessKeySecret)
		if err != nil {
			return err
		}
		rec.Aliyun.SKSealed = skSealed
	}
	if in.Aliyun.Region != "" {
		rec.Aliyun.Region = in.Aliyun.Region
	}
	if in.Aliyun.Instance != "" {
		rec.Aliyun.Instance = in.Aliyun.Instance
	}
	return nil
}

func (s *Server) handleRegistryList(w http.ResponseWriter, r *http.Request) {
	page := model.NormalizePage(queryInt(r, "page", 1), queryInt(r, "page_size", 20))
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	role := strings.TrimSpace(r.URL.Query().Get("role"))
	var records []model.RegistryRecord
	err := s.deps.DB.ForEach(bucketRegistries, func(_ string, value []byte) error {
		var rec model.RegistryRecord
		if err := json.Unmarshal(value, &rec); err != nil {
			return err
		}
		if name != "" && !strings.Contains(rec.Name, name) {
			return nil
		}
		if role != "" && rec.Role != role {
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
	items := make([]model.RegistryView, 0, end-start)
	for _, rec := range records[start:end] {
		items = append(items, registryView(rec))
	}
	writeJSON(w, http.StatusOK, model.PageEnvelope[model.RegistryView]{Data: items, Total: total, Page: page.Page, PageSize: page.PageSize})
}

func (s *Server) loadRegistry(r *http.Request) (model.RegistryRecord, *errs.E) {
	id := r.PathValue("id")
	raw, err := s.deps.DB.Get(bucketRegistries, id)
	if err != nil {
		return model.RegistryRecord{}, errs.Wrap(errs.CodeInternal, "读取失败", err)
	}
	if raw == nil {
		return model.RegistryRecord{}, errs.New(errs.CodeNotFound, "registry 不存在")
	}
	var rec model.RegistryRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return model.RegistryRecord{}, errs.Wrap(errs.CodeInternal, "数据损坏", err)
	}
	return rec, nil
}

func (s *Server) handleRegistryGet(w http.ResponseWriter, r *http.Request) {
	rec, e := s.loadRegistry(r)
	if e != nil {
		s.writeErr(w, e)
		return
	}
	writeJSON(w, http.StatusOK, registryView(rec))
}

func (s *Server) handleRegistryUpdate(w http.ResponseWriter, r *http.Request) {
	rec, e := s.loadRegistry(r)
	if e != nil {
		s.writeErr(w, e)
		return
	}
	var in model.RegistryInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		s.writeErr(w, errs.Wrap(errs.CodeBadRequest, "请求体不是合法 JSON", err))
		return
	}
	if err := s.validateRegistryInput(in, &rec); err != nil {
		s.writeErr(w, errs.From(err))
		return
	}
	rec.Name = in.Name
	rec.Address = registry.NormalizeAddress(in.Address)
	rec.Type = in.Type
	if in.Role != "" {
		rec.Role = in.Role
	}
	rec.Username = in.Username
	if err := s.applyRegistrySecrets(&rec, in); err != nil {
		s.writeErr(w, errs.From(err))
		return
	}
	rec.UpdatedAt = s.now()
	if err := s.deps.DB.Put(bucketRegistries, rec.ID, mustJSON(rec)); err != nil {
		s.writeErr(w, errs.Wrap(errs.CodeInternal, "保存失败", err))
		return
	}
	// 凭据可能已变更：失效该实例的临时凭据缓存（#4）
	if s.deps.Aliyun != nil {
		s.deps.Aliyun.Invalidate(rec.ID)
	}
	writeJSON(w, http.StatusOK, registryView(rec))
}

func (s *Server) handleRegistryDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// 级联：同时删除该来源下的镜像清单项
	var imageIDs []string
	_ = s.deps.DB.ForEach(bucketImages, func(_ string, value []byte) error {
		var img model.ImageRecord
		if json.Unmarshal(value, &img) == nil && img.RegistryID == id {
			imageIDs = append(imageIDs, img.ID)
		}
		return nil
	})
	for _, imgID := range imageIDs {
		if err := s.deps.DB.Delete(bucketImages, imgID); err != nil {
			s.writeErr(w, errs.Wrap(errs.CodeInternal, "删除镜像清单失败", err))
			return
		}
	}
	if err := s.deps.DB.Delete(bucketRegistries, id); err != nil {
		s.writeErr(w, errs.Wrap(errs.CodeInternal, "删除失败", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "images_removed": len(imageIDs)})
}

// handleRegistryTest 连通性测试：GET /v2/，区分网络/认证/非 registry（3001/3002/3003）。
func (s *Server) handleRegistryTest(w http.ResponseWriter, r *http.Request) {
	rec, e := s.loadRegistry(r)
	if e != nil {
		s.writeErr(w, e)
		return
	}
	username, secret := "", ""
	switch rec.Type {
	case model.RegistryTypeStandard:
		username = rec.Username
		plain, err := s.deps.Secrets.Open(rec.SecretSealed)
		if err != nil {
			s.writeErr(w, errs.From(err))
			return
		}
		secret = plain
	case model.RegistryTypeAliyunTemp:
		// 临时凭据路径：AK/SK 换取临时用户名密码后探测（#4）
		if s.deps.Aliyun == nil {
			s.writeErr(w, errs.New(errs.CodeInternal, "临时凭据提供者未配置"))
			return
		}
		tok, err := s.deps.Aliyun.Fetch(r.Context(), rec)
		if err != nil {
			fetchErr := errs.From(err)
			writeJSON(w, http.StatusOK, registry.PingResult{OK: false, Code: fetchErr.Code, Message: fetchErr.Message})
			return
		}
		username, secret = tok.Username, tok.Password
	}
	res := registry.Ping(rec.Address, username, secret)
	slog.Info("registry test", "registry_id", rec.ID, "code", res.Code) // 不落任何凭据
	writeJSON(w, http.StatusOK, res)
}

// handleImageBatch 批量录入镜像清单：每行一个引用，逐行给结果（第几行、原因）。
func (s *Server) handleImageBatch(w http.ResponseWriter, r *http.Request) {
	rec, e := s.loadRegistry(r)
	if e != nil {
		s.writeErr(w, e)
		return
	}
	var in model.ImageBatchInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		s.writeErr(w, errs.Wrap(errs.CodeBadRequest, "请求体不是合法 JSON", err))
		return
	}
	results := make([]model.ImageBatchItem, 0)
	now := s.now()
	created := 0
	for line, raw := range strings.Split(in.Refs, "\n") {
		lineNum := line + 1
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue // 空行跳过
		}
		ref, err := registry.ParseRef(raw)
		if err != nil {
			results = append(results, model.ImageBatchItem{Line: lineNum, OK: false, Reason: errs.From(err).Message})
			continue
		}
		// 逐条递增时间戳，保证清单顺序与录入顺序一致
		createdAt := now.Add(time.Duration(created) * time.Millisecond)
		created++
		img := model.ImageRecord{ID: model.NewID("img"), RegistryID: rec.ID, Ref: ref.String(), CreatedAt: createdAt}
		if err := s.deps.DB.Put(bucketImages, img.ID, mustJSON(img)); err != nil {
			results = append(results, model.ImageBatchItem{Line: lineNum, OK: false, Reason: "保存失败"})
			continue
		}
		results = append(results, model.ImageBatchItem{Line: lineNum, OK: true, Ref: img.Ref})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": results})
}

// handleImageList 某来源下的镜像清单（分页）。
func (s *Server) handleImageList(w http.ResponseWriter, r *http.Request) {
	rec, e := s.loadRegistry(r)
	if e != nil {
		s.writeErr(w, e)
		return
	}
	page := model.NormalizePage(queryInt(r, "page", 1), queryInt(r, "page_size", 20))
	var records []model.ImageRecord
	err := s.deps.DB.ForEach(bucketImages, func(_ string, value []byte) error {
		var img model.ImageRecord
		if err := json.Unmarshal(value, &img); err != nil {
			return err
		}
		if img.RegistryID == rec.ID {
			records = append(records, img)
		}
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
	items := make([]model.ImageView, 0, end-start)
	for _, img := range records[start:end] {
		items = append(items, model.ImageView{ID: img.ID, RegistryID: img.RegistryID, Ref: img.Ref, CreatedAt: img.CreatedAt})
	}
	writeJSON(w, http.StatusOK, model.PageEnvelope[model.ImageView]{Data: items, Total: total, Page: page.Page, PageSize: page.PageSize})
}

func (s *Server) handleImageDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.deps.DB.Get(bucketImages, id); err != nil {
		s.writeErr(w, errs.Wrap(errs.CodeInternal, "读取失败", err))
		return
	}
	if err := s.deps.DB.Delete(bucketImages, id); err != nil {
		s.writeErr(w, errs.Wrap(errs.CodeInternal, "删除失败", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
