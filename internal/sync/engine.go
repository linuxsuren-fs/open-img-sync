// Package sync 实现同步引擎与任务编排（#5）：
// A2 纯 Go 拉取 + M1 SSH 直推 / M2 中转推送，含状态机、进度、重试、取消、幂等。
package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/linuxsuren/open-img-sync/internal/aliyun"
	"github.com/linuxsuren/open-img-sync/internal/archive"
	"github.com/linuxsuren/open-img-sync/internal/errs"
	"github.com/linuxsuren/open-img-sync/internal/model"
	"github.com/linuxsuren/open-img-sync/internal/registry"
	"github.com/linuxsuren/open-img-sync/internal/sshx"
	"github.com/linuxsuren/open-img-sync/internal/store"
)

func jsonUnmarshal(raw []byte, v any) error { return json.Unmarshal(raw, v) }
func jsonMarshal(v any) ([]byte, error)     { return json.Marshal(v) }

func readFull(r io.Reader, buf []byte) (int, error) { return io.ReadFull(r, buf) }

// 同步段（4xxx）错误码。
const (
	CodeSourceAuthFail = 4001
	CodeTransferStop   = 4002
	CodeTaskCanceled   = 4005
	CodePartialFail    = 4006
	CodeRelayPushFail  = 4007
)

// Engine 同步引擎。
type Engine struct {
	DB         *store.DB
	Secrets    *store.SecretBox
	Aliyun     *aliyun.Provider
	SSHFactory func(ctx context.Context, opt sshx.Options) (SSHConn, error)
	SpoolDir   string

	// Concurrency 并发上限（默认 3）；MaxRetries 每步重试次数（默认 1，
	// 指数退避 1s/2s/4s...）；Platform 目标平台（默认 linux/amd64）
	Concurrency int
	MaxRetries  int
	Platform    [2]string

	mu      sync.Mutex
	running map[string]*runState
}

// SSHConn 抽象 SSH 连接能力（真实实现 sshx.Client，测试可替换）。
type SSHConn interface {
	StartStream(ctx context.Context, command string) (StdinSession, error)
	HasDigest(ctx context.Context, ref, digest string) (bool, error)
	Close() error
}

// StdinSession 流式会话。
type StdinSession interface {
	Stdin() interface {
		Write(p []byte) (int, error)
		Close() error
	}
	Stderr() string
	Wait() error
	Close()
}

type runState struct {
	ctx     context.Context
	cancel  context.CancelFunc
	logs    *logBuffer
	counter atomic.Int64
	task    *model.TaskRecord
	mu      sync.Mutex // 保护 task.Steps 现场更新
}

func (rs *runState) progress(delta int64) {
	rs.counter.Add(delta)
}

// New 构造引擎。
func New(db *store.DB, secrets *store.SecretBox, aliyunProv *aliyun.Provider, spoolDir string) *Engine {
	return &Engine{
		DB: db, Secrets: secrets, Aliyun: aliyunProv, SpoolDir: spoolDir,
		SSHFactory: realSSHFactory, running: map[string]*runState{},
	}
}

// sshConnAdapter 把 *sshx.Client 适配为 SSHConn 接口。
type sshConnAdapter struct{ c *sshx.Client }

func (a sshConnAdapter) StartStream(ctx context.Context, command string) (StdinSession, error) {
	return a.c.StartStream(ctx, command)
}
func (a sshConnAdapter) HasDigest(ctx context.Context, ref, digest string) (bool, error) {
	return a.c.HasDigest(ctx, ref, digest)
}
func (a sshConnAdapter) Close() error { return a.c.Close() }

func realSSHFactory(ctx context.Context, opt sshx.Options) (SSHConn, error) {
	conn, err := sshx.Dial(ctx, opt)
	if err != nil {
		return nil, err
	}
	return sshConnAdapter{c: conn}, nil
}

// CreateTask 校验输入、落库 queued 并启动异步执行。
func (e *Engine) CreateTask(in model.TaskInput, now time.Time) (model.TaskRecord, error) {
	if len(in.ImageIDs) == 0 {
		return model.TaskRecord{}, errs.New(errs.CodeBadRequest, "镜像清单不能为空")
	}
	mode := in.Mode
	if mode == "" {
		mode = model.ModeM1
	}
	if mode != model.ModeM1 && mode != model.ModeM2 {
		return model.TaskRecord{}, errs.New(errs.CodeBadRequest, "模式必须是 m1 或 m2")
	}
	if mode == model.ModeM1 && len(in.TargetServerIDs) == 0 {
		return model.TaskRecord{}, errs.New(errs.CodeBadRequest, "M1 模式必须选择目标服务器")
	}
	if mode == model.ModeM2 && in.RelayRegistryID == "" {
		return model.TaskRecord{}, errs.New(errs.CodeBadRequest, "M2 模式必须选择中转 registry")
	}

	task := model.TaskRecord{
		ID: model.NewID("task"), Mode: mode, Status: model.TaskQueued,
		ImageRefs: []string{}, TargetServerIDs: in.TargetServerIDs,
		RelayRegistryID: in.RelayRegistryID, CreatedAt: now,
	}
	// 解析镜像引用（来自清单项）
	for _, imgID := range in.ImageIDs {
		raw, err := e.DB.Get(bucketImages, imgID)
		if err != nil || raw == nil {
			return model.TaskRecord{}, errs.New(errs.CodeNotFound, "镜像清单项不存在: "+imgID)
		}
		var img model.ImageRecord
		if err := jsonUnmarshal(raw, &img); err != nil {
			return model.TaskRecord{}, errs.Wrap(errs.CodeInternal, "数据损坏", err)
		}
		task.ImageRefs = append(task.ImageRefs, img.Ref)
	}
	if err := e.persist(task); err != nil {
		return model.TaskRecord{}, err
	}
	// 同步注册运行现场：Cancel 可在 goroutine 调度前生效
	ctx, cancel := context.WithCancel(context.Background())
	rs := &runState{ctx: ctx, cancel: cancel, logs: newLogBuffer(), task: &task}
	e.mu.Lock()
	e.running[task.ID] = rs
	e.mu.Unlock()
	go e.run(rs, task.ID)
	return task, nil
}

// GetTask 读取任务（运行中优先内存现场）。
func (e *Engine) GetTask(id string) (model.TaskRecord, bool) {
	e.mu.Lock()
	rs, live := e.running[id]
	e.mu.Unlock()
	if live {
		rs.mu.Lock()
		defer rs.mu.Unlock()
		snap := *rs.task
		snap.Steps = append([]model.TaskStep{}, rs.task.Steps...)
		return snap, true
	}
	raw, err := e.DB.Get(bucketTasks, id)
	if err != nil || raw == nil {
		return model.TaskRecord{}, false
	}
	var task model.TaskRecord
	if err := jsonUnmarshal(raw, &task); err != nil {
		return model.TaskRecord{}, false
	}
	return task, true
}

// ListTasks 任务历史（倒序）。
func (e *Engine) ListTasks() []model.TaskRecord {
	var tasks []model.TaskRecord
	_ = e.DB.ForEach(bucketTasks, func(_ string, value []byte) error {
		var t model.TaskRecord
		if jsonUnmarshal(value, &t) == nil {
			tasks = append(tasks, t)
		}
		return nil
	})
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].CreatedAt.After(tasks[j].CreatedAt) })
	return tasks
}

// Cancel 取消运行中的任务；已开始的传输经 ctx 中止。
func (e *Engine) Cancel(id string) error {
	e.mu.Lock()
	rs, live := e.running[id]
	e.mu.Unlock()
	if !live {
		return errs.New(errs.CodeNotFound, "任务不在运行中")
	}
	rs.cancel()
	return nil
}

// LogsSince 读取自 seq 之后的日志行（运行中取内存现场，终态取落盘快照）。
func (e *Engine) LogsSince(id string, since int) ([]model.LogLine, bool) {
	e.mu.Lock()
	rs, live := e.running[id]
	e.mu.Unlock()
	if live {
		return rs.logs.since(since), true
	}
	raw, err := e.DB.Get(bucketTasks, id)
	if err != nil || raw == nil {
		return nil, false
	}
	var task model.TaskRecord
	if jsonUnmarshal(raw, &task) != nil {
		return nil, false
	}
	var out []model.LogLine
	for _, ln := range task.Logs {
		if ln.Seq > since {
			out = append(out, ln)
		}
	}
	return out, true
}

// capLogs 日志快照截断（最多保留 200 行）。
func capLogs(lines []model.LogLine) []model.LogLine {
	if len(lines) > 200 {
		return lines[len(lines)-200:]
	}
	return lines
}

const (
	bucketImages = "images"
	bucketTasks  = "tasks"
)

func (e *Engine) persist(task model.TaskRecord) error {
	raw, err := jsonMarshal(task)
	if err != nil {
		return errs.Wrap(errs.CodeInternal, "任务序列化失败", err)
	}
	return e.DB.Put(bucketTasks, task.ID, raw)
}

// run 任务主循环：步骤矩阵 + 并发池 + 重试。rs.task 是唯一现场，全部经 rs.mu 读写。
func (e *Engine) run(rs *runState, taskID string) {
	ctx := rs.ctx

	// 目标展开
	type syncTarget struct {
		id   string
		name string
	}
	var targets []syncTarget
	rs.mu.Lock()
	mode := rs.task.Mode
	refs := append([]string{}, rs.task.ImageRefs...)
	serverIDs := append([]string{}, rs.task.TargetServerIDs...)
	relayID := rs.task.RelayRegistryID
	rs.mu.Unlock()
	if mode == model.ModeM1 {
		for _, sid := range serverIDs {
			if rec, ok := e.loadServer(sid); ok {
				targets = append(targets, syncTarget{id: sid, name: rec.Name})
			}
		}
	} else if rec, ok := e.loadRegistry(relayID); ok {
		targets = append(targets, syncTarget{id: rec.ID, name: rec.Name})
	}

	rs.mu.Lock()
	rs.task.Status = model.TaskRunning
	rs.task.StartedAt = time.Now()
	for _, ref := range refs {
		for _, tgt := range targets {
			rs.task.Steps = append(rs.task.Steps, model.TaskStep{
				ImageRef: ref, Target: tgt.name, TargetID: tgt.id, Status: model.StepPending,
			})
		}
	}
	stepCount := len(rs.task.Steps)
	snapshot := e.snapshotTask(rs)
	rs.mu.Unlock()
	_ = e.persist(snapshot)
	rs.logs.append("info", fmt.Sprintf("任务开始：模式 %s，%d 镜像 × %d 目标", mode, len(refs), len(targets)))

	limit := e.Concurrency
	if limit <= 0 {
		limit = 3
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i := 0; i < stepCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			e.runStep(ctx, rs, idx)
		}(i)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	// 定期落盘进度
	ticker := time.NewTicker(time.Second)
	persistLoop := true
	for persistLoop {
		select {
		case <-done:
			persistLoop = false
		case <-ticker.C:
			_ = e.persist(e.snapshotTask(rs))
		case <-ctx.Done():
			// 等待在途步骤退出
			<-done
			persistLoop = false
		}
	}
	ticker.Stop()

	rs.mu.Lock()
	if ctx.Err() != nil {
		rs.task.Status = model.TaskCanceled
		rs.task.Code = CodeTaskCanceled
		rs.task.Message = "任务已取消"
		for i := range rs.task.Steps {
			if rs.task.Steps[i].Status == model.StepPending || rs.task.Steps[i].Status == model.StepRunning {
				rs.task.Steps[i].Status = model.StepCanceled
			}
		}
	} else {
		failed, skipped := 0, 0
		for _, s := range rs.task.Steps {
			if s.Status == model.StepFailed {
				failed++
			}
			if s.Status == model.StepSkipped {
				skipped++
			}
		}
		switch {
		case failed > 0:
			rs.task.Status = model.TaskFailed
			rs.task.Code = CodePartialFail
			rs.task.Message = fmt.Sprintf("%d 个步骤失败", failed)
		default:
			rs.task.Status = model.TaskSuccess
			rs.task.Code = 0
			if skipped == len(rs.task.Steps) && len(rs.task.Steps) > 0 {
				rs.task.Message = "全部已存在，无需传输"
			}
		}
	}
	rs.task.Bytes = rs.counter.Load()
	rs.task.Logs = capLogs(rs.logs.since(0))
	rs.task.FinishedAt = time.Now()
	final := e.snapshotTask(rs)
	rs.mu.Unlock()
	_ = e.persist(final)
	rs.logs.append("info", "任务结束："+final.Status)

	e.mu.Lock()
	delete(e.running, taskID)
	e.mu.Unlock()
	rs.cancel()
}

// snapshotTask 在持锁状态下拷贝任务现场（Steps 深拷贝，避免与 runStep 并发写竞争）。
func (e *Engine) snapshotTask(rs *runState) model.TaskRecord {
	snap := *rs.task
	snap.Steps = append([]model.TaskStep{}, rs.task.Steps...)
	return snap
}

// runStep 执行单个「镜像 × 目标」步骤（含指数退避重试）。
func (e *Engine) runStep(ctx context.Context, rs *runState, idx int) {
	rs.mu.Lock()
	step := &rs.task.Steps[idx]
	step.Status = model.StepRunning
	step.StartedAt = time.Now()
	ref := step.ImageRef
	targetName := step.Target
	rs.mu.Unlock()
	rs.logs.append("info", "开始同步 "+ref+" → "+targetName)

	maxRetries := e.MaxRetries
	if maxRetries < 0 {
		maxRetries = 1
	}
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if ctx.Err() != nil {
			break
		}
		bytesBefore := rs.counter.Load()
		err := e.executeStep(ctx, rs, idx, ref)
		if err == nil {
			rs.mu.Lock()
			step2 := &rs.task.Steps[idx]
			step2.Status = model.StepSuccess
			step2.Bytes = rs.counter.Load() - bytesBefore
			step2.FinishedAt = time.Now()
			rs.mu.Unlock()
			rs.logs.append("info", "完成 "+ref+" → "+targetName)
			return
		}
		if errors.Is(err, errSkipped) {
			rs.mu.Lock()
			step2 := &rs.task.Steps[idx]
			step2.Status = model.StepSkipped
			step2.Skipped = true
			step2.Message = "目标已存在同 digest 镜像，跳过传输"
			step2.FinishedAt = time.Now()
			rs.mu.Unlock()
			rs.logs.append("info", "已存在，跳过 "+ref+" → "+targetName)
			return
		}
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			break
		}
		lastErr = err
		bizErr := errs.From(err)
		rs.logs.append("warn", fmt.Sprintf("失败（第 %d 次）%s → %s：%s",
			attempt+1, ref, targetName, bizErr.Message))
		if attempt < maxRetries {
			backoff := time.Duration(1<<uint(attempt)) * time.Second
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
		}
	}

	rs.mu.Lock()
	step2 := &rs.task.Steps[idx]
	step2.Status = model.StepFailed
	if lastErr != nil {
		biz := errs.From(lastErr)
		step2.ErrorCode = biz.Code
		step2.Message = biz.Message
	} else {
		step2.ErrorCode = CodeTaskCanceled
		step2.Message = "已取消"
	}
	step2.FinishedAt = time.Now()
	rs.mu.Unlock()
}

// errSkipped 幂等命中信号。
var errSkipped = errors.New("skipped")

// executeStep 单次执行：解析来源 → 拉取 manifest → 幂等检查 → M1/M2 传输。
func (e *Engine) executeStep(ctx context.Context, rs *runState, idx int, ref string) error {
	rs.mu.Lock()
	target := rs.task.Steps[idx].Target
	targetID := rs.task.Steps[idx].TargetID
	mode := rs.task.Mode
	rs.mu.Unlock()

	// 镜像 → 来源 registry
	imgRec, ok := e.findImageByRef(ref)
	if !ok {
		return errs.New(errs.CodeNotFound, "镜像清单项不存在: "+ref)
	}
	srcReg, ok := e.loadRegistry(imgRec.RegistryID)
	if !ok {
		return errs.New(errs.CodeNotFound, "来源 registry 不存在")
	}
	srcClient, err := e.registryClient(srcReg)
	if err != nil {
		return err
	}

	parsed, err := registry.ParseRef(ref)
	if err != nil {
		return err
	}
	repo := parsed.Repository
	if parsed.Registry != "" && srcReg.Address != "" {
		// 引用带 host 时以登记的 registry 地址为准，仓库取路径部分
	}
	manifestBody, manifestDigest, mediaType, err := srcClient.GetManifest(ctx, repo, parsed.Tag)
	if err != nil {
		return mapSourceError(err)
	}
	if registry.IsIndex(mediaType) {
		idxList, err := registry.ParseIndex(manifestBody)
		if err != nil {
			return err
		}
		goos, goarch := e.platform()
		child := registry.SelectPlatform(idxList, goos, goarch)
		manifestBody, manifestDigest, mediaType, err = srcClient.GetManifest(ctx, repo, child)
		if err != nil {
			return mapSourceError(err)
		}
	}
	if manifestDigest == "" {
		manifestDigest = archive.SHA256Hex(manifestBody)
	}
	imgManifest, err := registry.ParseImageManifest(manifestBody)
	if err != nil {
		return err
	}

	if mode == model.ModeM1 {
		return e.pushM1(ctx, rs, srcClient, repo, parsed.Tag, manifestDigest, imgManifest, targetID, target)
	}
	return e.pushM2(ctx, rs, srcClient, repo, parsed.Tag, manifestDigest, mediaType, manifestBody, imgManifest, targetID)
}

// pushM1 SSH 直推：spool 各层 → docker load tar → SSH 管道。
func (e *Engine) pushM1(ctx context.Context, rs *runState, src *registry.Client, repo, tag, digest string,
	m *registry.ImageManifest, serverID, targetName string) error {
	srvRec, ok := e.loadServer(serverID)
	if !ok {
		return errs.New(errs.CodeNotFound, "目标服务器不存在")
	}
	secret, err := e.Secrets.Open(srvRec.SecretSealed)
	if err != nil {
		return err
	}
	opt := sshx.Options{Host: srvRec.Host, Port: srvRec.Port, Username: srvRec.Username, AuthType: srvRec.AuthType}
	switch srvRec.AuthType {
	case model.AuthPassword:
		opt.Password = secret
	case model.AuthPrivateKey:
		opt.PrivateKey = secret
	}
	conn, err := e.SSHFactory(ctx, opt)
	if err != nil {
		return err
	}
	defer conn.Close()

	repoTag := repo + ":" + tag
	has, err := conn.HasDigest(ctx, repoTag, digest)
	if err == nil && has {
		return errSkipped
	}
	sess, err := conn.StartStream(ctx, "docker load")
	if err != nil {
		return err
	}
	lw := archive.NewLoadWriter(sess.Stdin())

	// config blob（小，整块读入）
	cfgRC, _, err := src.OpenBlob(ctx, repo, m.Config.Digest, 0)
	if err != nil {
		sess.Close()
		return mapSourceError(err)
	}
	cfgBytes := make([]byte, m.Config.Size)
	if _, err := readFull(cfgRC, cfgBytes); err != nil {
		cfgRC.Close()
		sess.Close()
		return errs.Wrap(CodeTransferStop, "传输中断", err)
	}
	cfgRC.Close()
	if err := lw.WriteConfig(m.Config.Digest, cfgBytes); err != nil {
		sess.Close()
		return errs.Wrap(CodeTransferStop, "传输中断", err)
	}
	rs.progress(int64(len(cfgBytes)))

	// 各层：下载解压 spool → 写 tar
	spoolDir := e.SpoolDir
	if spoolDir == "" {
		spoolDir = filepath.Join(os.TempDir(), "open-img-sync-spool")
	}
	if err := os.MkdirAll(spoolDir, 0o700); err != nil {
		return errs.Wrap(errs.CodeInternal, "创建缓存目录失败", err)
	}
	for _, layer := range m.Layers {
		if ctx.Err() != nil {
			sess.Close()
			return ctx.Err()
		}
		if err := e.spoolLayer(ctx, src, rs, repo, layer, spoolDir, lw); err != nil {
			sess.Close()
			return err
		}
	}
	if err := lw.Finish(repoTag, m.Config.Digest, layerDigests(m)); err != nil {
		sess.Close()
		return errs.Wrap(CodeTransferStop, "传输中断", err)
	}
	// 关闭 stdin 表示输入完毕；远端已结束时的 EOF 不是错误
	_ = sess.Stdin().Close()
	waitErr := sess.Wait()
	sess.Close()
	if waitErr != nil {
		// 远端 docker load 失败：错误归因（磁盘满/其他）已在 sshx 分类
		return waitErr
	}
	return nil
}

func layerDigests(m *registry.ImageManifest) []string {
	out := make([]string, 0, len(m.Layers))
	for _, l := range m.Layers {
		out = append(out, l.Digest)
	}
	return out
}

// spoolLayer 单层下载：blob 流 → 解压 → 临时文件 → tar 条目 → 删除。
func (e *Engine) spoolLayer(ctx context.Context, src *registry.Client, rs *runState,
	repo string, layer registry.Descriptor, spoolDir string, lw *archive.LoadWriter) error {
	blobRC, _, err := src.OpenBlob(ctx, repo, layer.Digest, 0)
	if err != nil {
		return mapSourceError(err)
	}
	defer blobRC.Close()

	tmpFile, err := os.CreateTemp(spoolDir, "layer-*.tar")
	if err != nil {
		return errs.Wrap(errs.CodeInternal, "创建缓存文件失败", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath) //nolint:errcheck // 尽力清理

	if _, err := archive.DecompressTo(layer.MediaType, blobRC, rs.progress, tmpFile); err != nil {
		tmpFile.Close()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errs.Wrap(CodeTransferStop, "传输中断", err)
	}
	if err := tmpFile.Close(); err != nil {
		return errs.Wrap(errs.CodeInternal, "关闭缓存文件失败", err)
	}
	if err := lw.WriteLayerFile(layer.Digest, tmpPath); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errs.Wrap(CodeTransferStop, "传输中断", err)
	}
	return nil
}

// pushM2 中转推送：blob 存在性检查跳过已存在层，全流式转发。
func (e *Engine) pushM2(ctx context.Context, rs *runState, src *registry.Client, repo, tag, digest,
	mediaType string, manifestBody []byte, m *registry.ImageManifest, relayID string) error {
	relay, ok := e.loadRegistry(relayID)
	if !ok {
		return errs.New(errs.CodeNotFound, "中转 registry 不存在")
	}
	dst, err := e.registryClient(relay)
	if err != nil {
		return err
	}
	if exists, _ := dst.HeadManifest(ctx, repo, digest); exists {
		return errSkipped
	}
	// config + layers
	blobs := append([]registry.Descriptor{m.Config}, m.Layers...)
	for _, b := range blobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		has, err := dst.BlobExists(ctx, repo, b.Digest)
		if err != nil {
			return errs.Wrap(CodeRelayPushFail, "推送中转 registry 失败", nil)
		}
		if has {
			continue // 已存在层跳过（幂等）
		}
		blobRC, size, err := src.OpenBlob(ctx, repo, b.Digest, 0)
		if err != nil {
			return mapSourceError(err)
		}
		location, err := dst.InitiateUpload(ctx, repo)
		if err != nil {
			blobRC.Close()
			return err
		}
		counted := &countReadCloser{rc: blobRC, f: rs.progress}
		if size <= 0 {
			size = b.Size
		}
		if err := dst.PutBlob(ctx, location, b.Digest, size, counted); err != nil {
			counted.Close()
			return err
		}
		counted.Close()
	}
	if err := dst.PutManifest(ctx, repo, tag, mediaType, manifestBody); err != nil {
		return err
	}
	return nil
}

type countReadCloser struct {
	rc io.ReadCloser
	f  func(delta int64)
}

func (c *countReadCloser) Read(p []byte) (int, error) {
	n, err := c.rc.Read(p)
	if n > 0 && c.f != nil {
		c.f(int64(n))
	}
	return n, err
}

func (c *countReadCloser) Close() error { return c.rc.Close() }

// registryClient 按类型构造来源/中转客户端（凭据解密或临时凭据）。
func (e *Engine) registryClient(reg model.RegistryRecord) (*registry.Client, error) {
	switch reg.Type {
	case model.RegistryTypeStandard:
		secret, err := e.Secrets.Open(reg.SecretSealed)
		if err != nil {
			return nil, err
		}
		return registry.NewClient(reg.Address, reg.Username, secret), nil
	case model.RegistryTypeAliyunTemp:
		if e.Aliyun == nil {
			return nil, errs.New(errs.CodeInternal, "临时凭据提供者未配置")
		}
		tok, err := e.Aliyun.Fetch(context.Background(), reg)
		if err != nil {
			return nil, err
		}
		return registry.NewClient(reg.Address, tok.Username, tok.Password), nil
	default:
		return registry.NewClient(reg.Address, "", ""), nil
	}
}

// mapSourceError 把 registry 段错误映射为同步段语义：源认证失败 → 4001、
// 传输中断 → 4002；其余（网络不可达、引用不合法等）保持原错误码透出。
func mapSourceError(err error) error {
	biz := errs.From(err)
	switch biz.Code {
	case registry.CodeAuthFailed:
		return errs.Wrap(CodeSourceAuthFail, "源 registry 认证失败", biz.Cause())
	case registry.CodeTransfer:
		return errs.Wrap(CodeTransferStop, "传输中断", biz.Cause())
	default:
		return err
	}
}

func (e *Engine) platform() (string, string) {
	if e.Platform[0] != "" {
		return e.Platform[0], e.Platform[1]
	}
	return "linux", "amd64"
}

func (e *Engine) loadServer(id string) (model.ServerRecord, bool) {
	raw, err := e.DB.Get("servers", id)
	if err != nil || raw == nil {
		return model.ServerRecord{}, false
	}
	var rec model.ServerRecord
	if jsonUnmarshal(raw, &rec) != nil {
		return model.ServerRecord{}, false
	}
	return rec, true
}

func (e *Engine) loadRegistry(id string) (model.RegistryRecord, bool) {
	raw, err := e.DB.Get("registries", id)
	if err != nil || raw == nil {
		return model.RegistryRecord{}, false
	}
	var rec model.RegistryRecord
	if jsonUnmarshal(raw, &rec) != nil {
		return model.RegistryRecord{}, false
	}
	return rec, true
}

func (e *Engine) findImageByRef(ref string) (model.ImageRecord, bool) {
	var found model.ImageRecord
	var ok bool
	_ = e.DB.ForEach(bucketImages, func(_ string, value []byte) error {
		var img model.ImageRecord
		if jsonUnmarshal(value, &img) == nil && img.Ref == ref && !ok {
			found, ok = img, true
		}
		return nil
	})
	return found, ok
}

// Drain 等待运行中的任务全部结束（优雅退出用，#8）；
// 超时返回 false（剩余任务随进程退出中止）。
func (e *Engine) Drain(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		e.mu.Lock()
		n := len(e.running)
		e.mu.Unlock()
		if n == 0 {
			return true
		}
		if time.Now().After(deadline) {
			slog.Warn("graceful drain timeout", "running_tasks", n)
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// logBuffer 环形日志缓冲（最近 1000 行）。
type logBuffer struct {
	mu    sync.Mutex
	lines []model.LogLine
	seq   int
}

func newLogBuffer() *logBuffer { return &logBuffer{} }

func (l *logBuffer) append(level, line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	l.lines = append(l.lines, model.LogLine{
		Seq: l.seq, Time: time.Now().Format("15:04:05"), Level: level, Line: line,
	})
	if len(l.lines) > 1000 {
		l.lines = l.lines[len(l.lines)-1000:]
	}
	slog.Info("task", "level", level, "msg", line)
}

func (l *logBuffer) since(after int) []model.LogLine {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []model.LogLine
	for _, ln := range l.lines {
		if ln.Seq > after {
			out = append(out, ln)
		}
	}
	return out
}
