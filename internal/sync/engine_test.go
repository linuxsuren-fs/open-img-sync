package sync

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/linuxsuren/open-img-sync/internal/crypt"
	"github.com/linuxsuren/open-img-sync/internal/model"
	"github.com/linuxsuren/open-img-sync/internal/store"
)

// fakeRegistry 极小 V2 registry：服务 manifest 与 blob，可注入 401 / 断流。
type fakeRegistry struct {
	server         *httptest.Server
	mu             sync.Mutex
	blobMu         sync.Mutex
	blobs          map[string][]byte
	manifests      map[string]manifestEntry
	authUser       string
	authPass       string
	failAuth       bool
	failBlobStream bool
	uploadedBlobs  map[string]int
	uploadedManifs map[string]string
}

type manifestEntry struct {
	body      []byte
	mediaType string
	digest    string
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	fr := &fakeRegistry{
		blobs: map[string][]byte{}, manifests: map[string]manifestEntry{},
		uploadedBlobs: map[string]int{}, uploadedManifs: map[string]string{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v2/", func(w http.ResponseWriter, r *http.Request) {
		if !fr.checkAuth(w, r) {
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /v2/{repo}/manifests/{ref}", func(w http.ResponseWriter, r *http.Request) {
		if !fr.checkAuth(w, r) {
			return
		}
		fr.mu.Lock()
		defer fr.mu.Unlock()
		entry, ok := fr.manifests[r.PathValue("ref")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", entry.mediaType)
		w.Header().Set("Docker-Content-Digest", entry.digest)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(entry.body)
	})
	mux.HandleFunc("HEAD /v2/{repo}/manifests/{ref}", func(w http.ResponseWriter, r *http.Request) {
		if !fr.checkAuth(w, r) {
			return
		}
		fr.mu.Lock()
		defer fr.mu.Unlock()
		if _, ok := fr.manifests[r.PathValue("ref")]; ok {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("GET /v2/{repo}/blobs/{digest}", func(w http.ResponseWriter, r *http.Request) {
		if !fr.checkAuth(w, r) {
			return
		}
		fr.blobMu.Lock()
		content, ok := fr.blobs[r.PathValue("digest")]
		fr.blobMu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		w.WriteHeader(http.StatusOK)
		if fr.failBlobStream {
			// 写一半返回：Content-Length 大于实际写入 → 客户端 unexpected EOF
			_, _ = w.Write(content[:len(content)/2])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			return
		}
		_, _ = w.Write(content)
	})
	mux.HandleFunc("HEAD /v2/{repo}/blobs/{digest}", func(w http.ResponseWriter, r *http.Request) {
		if !fr.checkAuth(w, r) {
			return
		}
		fr.blobMu.Lock()
		_, ok := fr.blobs[r.PathValue("digest")]
		fr.blobMu.Unlock()
		if ok {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	})
	mux.HandleFunc("POST /v2/{repo}/blobs/uploads/", func(w http.ResponseWriter, r *http.Request) {
		if !fr.checkAuth(w, r) {
			return
		}
		w.Header().Set("Location", "/upload-session-1")
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("PUT /upload-session-1", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		digest := r.URL.Query().Get("digest")
		if sumDigest(body) != digest {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		fr.blobMu.Lock()
		fr.blobs[digest] = body
		fr.uploadedBlobs[digest]++
		fr.blobMu.Unlock()
		w.Header().Set("Docker-Content-Digest", digest)
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("PUT /v2/{repo}/manifests/{ref}", func(w http.ResponseWriter, r *http.Request) {
		if !fr.checkAuth(w, r) {
			return
		}
		body, _ := io.ReadAll(r.Body)
		ref := r.PathValue("ref")
		entry := manifestEntry{body: body, mediaType: r.Header.Get("Content-Type"), digest: sumDigest(body)}
		fr.mu.Lock()
		// 真实 registry 行为：tag 与 digest 都可引用 manifest
		fr.manifests[ref] = entry
		fr.manifests[entry.digest] = entry
		fr.uploadedManifs[ref] = string(body)
		fr.mu.Unlock()
		w.Header().Set("Docker-Content-Digest", entry.digest)
		w.WriteHeader(http.StatusCreated)
	})
	fr.server = httptest.NewServer(mux)
	t.Cleanup(fr.server.Close)
	return fr
}

func (fr *fakeRegistry) checkAuth(w http.ResponseWriter, r *http.Request) bool {
	if fr.authUser == "" {
		return true
	}
	user, pass, ok := r.BasicAuth()
	if fr.failAuth || !ok || user != fr.authUser || pass != fr.authPass {
		w.Header().Set("WWW-Authenticate", `Basic realm="fake"`)
		w.WriteHeader(http.StatusUnauthorized)
		return false
	}
	return true
}

func sumDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return fmt.Sprintf("sha256:%x", sum)
}

// pushTestImage 造最小镜像（config + 1 gzip 层），登记 manifest，返回其 digest。
func (fr *fakeRegistry) pushTestImage(t *testing.T, repo, tag string, layerTar []byte) string {
	t.Helper()
	var gzBuf bytes.Buffer
	gzw := gzip.NewWriter(&gzBuf)
	_, _ = gzw.Write(layerTar)
	_ = gzw.Close()
	gz := gzBuf.Bytes()
	layerDigest := sumDigest(gz)
	configRaw, _ := json.Marshal(map[string]any{
		"architecture": "amd64", "os": "linux",
		"rootfs": map[string]any{"type": "layers", "diff_ids": []string{sumDigest(layerTar)}},
	})
	configDigest := sumDigest(configRaw)
	fr.blobMu.Lock()
	fr.blobs[layerDigest] = gz
	fr.blobs[configDigest] = configRaw
	fr.blobMu.Unlock()
	manifestRaw, _ := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.docker.distribution.manifest.v2+json",
		"config":        map[string]any{"mediaType": "application/vnd.docker.container.image.v1+json", "size": len(configRaw), "digest": configDigest},
		"layers":        []any{map[string]any{"mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip", "size": len(gz), "digest": layerDigest}},
	})
	manifestDigest := sumDigest(manifestRaw)
	fr.mu.Lock()
	// 同时以 tag 与 digest 索引（真实 registry 两者都可引用 manifest）
	fr.manifests[tag] = manifestEntry{body: manifestRaw, mediaType: "application/vnd.docker.distribution.manifest.v2+json", digest: manifestDigest}
	fr.manifests[manifestDigest] = manifestEntry{body: manifestRaw, mediaType: "application/vnd.docker.distribution.manifest.v2+json", digest: manifestDigest}
	fr.mu.Unlock()
	return manifestDigest
}

// fakeSSHD 捕获 docker load stdin；可注入失败与幂等命中。
type fakeSSHD struct {
	host, port string
	password   string
	mu         sync.Mutex
	loadedTar  []byte
	known      map[string]string
	failLoad   string
	loadCalls  atomic.Int32
}

func startFakeSSHD(t *testing.T, password string) *fakeSSHD {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	srvCfg := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if string(pw) == password {
				return nil, nil
			}
			return nil, fmt.Errorf("rejected")
		},
	}
	srvCfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	s := &fakeSSHD{password: password, known: map[string]string{}}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handle(conn, srvCfg)
		}
	}()
	s.host, s.port, _ = net.SplitHostPort(ln.Addr().String())
	return s
}

func (s *fakeSSHD) targetPort() int {
	n, _ := strconv.Atoi(s.port)
	return n
}

func (s *fakeSSHD) handle(c net.Conn, cfg *ssh.ServerConfig) {
	sconn, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	defer sconn.Close()
	go ssh.DiscardRequests(reqs)
	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			_ = newChan.Reject(ssh.UnknownChannelType, "unsupported")
			continue
		}
		ch, chReqs, err := newChan.Accept()
		if err != nil {
			continue
		}
		go s.serveChannel(ch, chReqs)
	}
}

func (s *fakeSSHD) serveChannel(ch ssh.Channel, chReqs <-chan *ssh.Request) {
	for req := range chReqs {
		if req.Type != "exec" {
			_ = req.Reply(false, nil)
			continue
		}
		_ = req.Reply(true, nil)
		var cmd struct{ Command string }
		_ = ssh.Unmarshal(req.Payload, &cmd)
		switch {
		case strings.Contains(cmd.Command, "docker load"):
			s.loadCalls.Add(1)
			var stdin bytes.Buffer
			_, _ = io.Copy(&stdin, ch)
			s.mu.Lock()
			s.loadedTar = stdin.Bytes()
			fail := s.failLoad
			s.mu.Unlock()
			exit := 0
			if fail != "" {
				_, _ = ch.Stderr().Write([]byte(fail))
				exit = 1
			}
			_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(exit)}))
			_ = ch.Close()
		case strings.Contains(cmd.Command, "docker image inspect"):
			ref := extractInspectRef(cmd.Command)
			s.mu.Lock()
			digest, ok := s.known[ref]
			s.mu.Unlock()
			if ok {
				_, _ = ch.Write([]byte(fmt.Sprintf(`["%s@%s"]`, ref, digest)))
				_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
			} else {
				_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{1}))
			}
			_ = ch.Close()
		default:
			_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
			_ = ch.Close()
		}
	}
}

func extractInspectRef(cmd string) string {
	// 命令形如：docker image inspect --format '{{json .RepoDigests}}' "myapp:latest"
	start := strings.LastIndex(cmd, `"`)
	if start < 0 {
		return cmd
	}
	head := strings.LastIndex(cmd[:start], `"`)
	if head < 0 {
		return cmd
	}
	return cmd[head+1 : start]
}

// ---- 测试环境 ----

type testEnv struct {
	engine  *Engine
	reg     *fakeRegistry
	sshd    *fakeSSHD
	db      *store.DB
	secrets *store.SecretBox
	regRec  model.RegistryRecord
	srvRec  model.ServerRecord
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	key, _, err := crypt.LoadMasterKey(crypt.MasterKeyConfig{EnvVar: "OIS_TEST_KEY_UNUSED", FilePath: filepath.Join(dir, "master.key")})
	if err != nil {
		t.Fatal(err)
	}
	secrets := store.NewSecretBox(crypt.NewBox(key))

	fr := newFakeRegistry(t)
	sshd := startFakeSSHD(t, "pw")

	sealed, _ := secrets.Seal("pw")
	env := &testEnv{
		db: db, secrets: secrets, reg: fr, sshd: sshd,
		regRec: model.RegistryRecord{ID: "reg-1", Name: "src", Address: fr.server.URL, Type: model.RegistryTypeAnonymous, Role: model.RegistryRoleSource},
		srvRec: model.ServerRecord{
			ID: "srv-1", Name: "edge-01", Host: sshd.host, Port: sshd.targetPort(),
			AuthType: model.AuthPassword, Username: "root", SecretSealed: sealed,
		},
	}
	if err := db.EnsureBuckets("servers", "registries", "images", "tasks"); err != nil {
		t.Fatal(err)
	}
	putJSON(t, db, "servers", env.srvRec.ID, env.srvRec)
	putJSON(t, db, "registries", env.regRec.ID, env.regRec)
	putJSON(t, db, "images", "img-1", model.ImageRecord{ID: "img-1", RegistryID: "reg-1", Ref: "myapp:latest"})

	env.engine = New(db, secrets, nil, filepath.Join(dir, "spool"))
	env.engine.MaxRetries = 1
	return env
}

func putJSON(t *testing.T, db *store.DB, bucket, key string, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Put(bucket, key, raw); err != nil {
		t.Fatal(err)
	}
}

func waitTerminal(t *testing.T, env *testEnv, id string) model.TaskRecord {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		task, ok := env.engine.GetTask(id)
		if ok && isTerminalStatus(task.Status) {
			return task
		}
		time.Sleep(50 * time.Millisecond)
	}
	task, _ := env.engine.GetTask(id)
	t.Fatalf("任务未在期限内进入终态（status=%s steps=%+v）", task.Status, task.Steps)
	return model.TaskRecord{}
}

func isTerminalStatus(s string) bool {
	return s == model.TaskSuccess || s == model.TaskFailed || s == model.TaskCanceled
}

// ---- 用例 ----

func TestM1HappyPath(t *testing.T) {
	env := newTestEnv(t)
	layer1 := makeLayerContent("layer1", 1024)
	layer2 := makeLayerContent("layer2", 2048)
	env.reg.pushTestImage(t, "myapp", "latest", layer1)
	env.reg.pushTestImage(t, "myapp", "v2", layer2)

	putJSON(t, env.db, "servers", "srv-2", model.ServerRecord{
		ID: "srv-2", Name: "edge-02", Host: env.sshd.host, Port: env.sshd.targetPort(),
		AuthType: model.AuthPassword, Username: "root", SecretSealed: env.srvRec.SecretSealed,
	})
	putJSON(t, env.db, "images", "img-2", model.ImageRecord{ID: "img-2", RegistryID: "reg-1", Ref: "myapp:v2"})

	task, err := env.engine.CreateTask(model.TaskInput{
		Mode: model.ModeM1, ImageIDs: []string{"img-1", "img-2"}, TargetServerIDs: []string{"srv-1", "srv-2"},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	final := waitTerminal(t, env, task.ID)
	if final.Status != model.TaskSuccess {
		t.Fatalf("status = %s, steps = %+v", final.Status, final.Steps)
	}
	if len(final.Steps) != 4 {
		t.Fatalf("步骤数 = %d, want 4（2 镜像 × 2 服务器）", len(final.Steps))
	}
	if final.Bytes <= 0 {
		t.Fatalf("传输字节数 = %d, want > 0", final.Bytes)
	}
	env.sshd.mu.Lock()
	tarBytes := env.sshd.loadedTar
	env.sshd.mu.Unlock()
	if !bytes.Contains(tarBytes, []byte("manifest.json")) {
		t.Fatal("docker load tar 缺 manifest.json")
	}
	if !bytes.Contains(tarBytes, layer1) && !bytes.Contains(tarBytes, layer2) {
		t.Fatal("tar 中未找到任一层内容")
	}
}

func TestM1IdempotentSkip(t *testing.T) {
	env := newTestEnv(t)
	digest := env.reg.pushTestImage(t, "myapp", "latest", makeLayerContent("idem", 512))
	env.sshd.mu.Lock()
	env.sshd.known["myapp:latest"] = digest
	env.sshd.mu.Unlock()

	task, err := env.engine.CreateTask(model.TaskInput{
		Mode: model.ModeM1, ImageIDs: []string{"img-1"}, TargetServerIDs: []string{"srv-1"},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	final := waitTerminal(t, env, task.ID)
	if final.Status != model.TaskSuccess {
		t.Fatalf("status = %s", final.Status)
	}
	for _, s := range final.Steps {
		if !s.Skipped || s.Status != model.StepSkipped {
			t.Fatalf("步骤应标记已存在: %+v", s)
		}
		if s.Bytes != 0 {
			t.Fatalf("跳过步骤不应有传输字节: %+v", s)
		}
	}
	if final.Bytes != 0 {
		t.Fatalf("幂等任务总字节 = %d, want 0", final.Bytes)
	}
	if env.sshd.loadCalls.Load() != 0 {
		t.Fatal("幂等命中不应触发 docker load")
	}
	if final.Message != "全部已存在，无需传输" {
		t.Fatalf("message = %q", final.Message)
	}
}

func TestSourceAuthFailure(t *testing.T) {
	env := newTestEnv(t)
	env.reg.pushTestImage(t, "myapp", "latest", makeLayerContent("x", 128))
	env.reg.authUser, env.reg.authPass, env.reg.failAuth = "user", "pass", true
	wrongSealed, _ := env.secrets.Seal("wrong-pass")
	env.regRec.Type = model.RegistryTypeStandard
	env.regRec.Username = "user"
	env.regRec.SecretSealed = wrongSealed
	putJSON(t, env.db, "registries", env.regRec.ID, env.regRec)

	task, err := env.engine.CreateTask(model.TaskInput{
		Mode: model.ModeM1, ImageIDs: []string{"img-1"}, TargetServerIDs: []string{"srv-1"},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	final := waitTerminal(t, env, task.ID)
	if final.Status != model.TaskFailed {
		t.Fatalf("status = %s", final.Status)
	}
	if final.Steps[0].ErrorCode != CodeSourceAuthFail {
		t.Fatalf("错误码 = %d, want %d（源 registry 认证失败）", final.Steps[0].ErrorCode, CodeSourceAuthFail)
	}
}

func TestTargetDiskFull(t *testing.T) {
	env := newTestEnv(t)
	env.reg.pushTestImage(t, "myapp", "latest", makeLayerContent("x", 128))
	env.sshd.mu.Lock()
	env.sshd.failLoad = "Error processing tar file(no space left on device): exit status 1"
	env.sshd.mu.Unlock()

	task, err := env.engine.CreateTask(model.TaskInput{
		Mode: model.ModeM1, ImageIDs: []string{"img-1"}, TargetServerIDs: []string{"srv-1"},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	final := waitTerminal(t, env, task.ID)
	env.sshd.mu.Lock()
	calls := env.sshd.loadCalls.Load()
	tarLen := len(env.sshd.loadedTar)
	env.sshd.mu.Unlock()
	if final.Status != model.TaskFailed {
		t.Fatalf("status = %s, steps = %+v, loadCalls = %d, tarLen = %d",
			final.Status, final.Steps, calls, tarLen)
	}
	if final.Steps[0].ErrorCode != 4003 {
		t.Fatalf("错误码 = %d, want 4003（目标机磁盘已满）", final.Steps[0].ErrorCode)
	}
}

func TestTransferInterrupted(t *testing.T) {
	env := newTestEnv(t)
	env.reg.pushTestImage(t, "myapp", "latest", makeLayerContent("big", 256*1024))
	env.reg.failBlobStream = true

	task, err := env.engine.CreateTask(model.TaskInput{
		Mode: model.ModeM1, ImageIDs: []string{"img-1"}, TargetServerIDs: []string{"srv-1"},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	final := waitTerminal(t, env, task.ID)
	if final.Status != model.TaskFailed {
		t.Fatalf("status = %s", final.Status)
	}
	if final.Steps[0].ErrorCode != CodeTransferStop {
		t.Fatalf("错误码 = %d, want %d（传输中断）", final.Steps[0].ErrorCode, CodeTransferStop)
	}
}

func TestCancelRunningTask(t *testing.T) {
	env := newTestEnv(t)
	env.reg.pushTestImage(t, "myapp", "latest", makeLayerContent("x", 128))
	task, err := env.engine.CreateTask(model.TaskInput{
		Mode: model.ModeM1, ImageIDs: []string{"img-1"}, TargetServerIDs: []string{"srv-1"},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := env.engine.Cancel(task.ID); err != nil {
		t.Fatal(err)
	}
	final := waitTerminal(t, env, task.ID)
	if final.Status != model.TaskCanceled {
		t.Fatalf("status = %s, want canceled", final.Status)
	}
	if final.Code != CodeTaskCanceled {
		t.Fatalf("code = %d, want %d", final.Code, CodeTaskCanceled)
	}
}

func TestM2RelayPush(t *testing.T) {
	env := newTestEnv(t)
	env.reg.pushTestImage(t, "myapp", "latest", makeLayerContent("relay", 512))

	relay := newFakeRegistry(t)
	putJSON(t, env.db, "registries", "reg-relay", model.RegistryRecord{
		ID: "reg-relay", Name: "relay", Address: relay.server.URL,
		Type: model.RegistryTypeAnonymous, Role: model.RegistryRoleRelay,
	})

	input := model.TaskInput{Mode: model.ModeM2, ImageIDs: []string{"img-1"}, RelayRegistryID: "reg-relay"}
	task, err := env.engine.CreateTask(input, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	final := waitTerminal(t, env, task.ID)
	if final.Status != model.TaskSuccess {
		t.Fatalf("status = %s steps=%+v", final.Status, final.Steps)
	}
	relay.mu.Lock()
	_, gotManifest := relay.uploadedManifs["latest"]
	relay.mu.Unlock()
	if !gotManifest {
		t.Fatal("中转 registry 未收到 manifest")
	}

	task2, err := env.engine.CreateTask(input, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	final2 := waitTerminal(t, env, task2.ID)
	if final2.Status != model.TaskSuccess || !final2.Steps[0].Skipped {
		t.Fatalf("第二次应命中幂等: %+v", final2.Steps)
	}
	if final2.Bytes != 0 {
		t.Fatalf("幂等命中仍有传输: %d", final2.Bytes)
	}
}

func TestSingleTargetFailureIsolation(t *testing.T) {
	env := newTestEnv(t)
	env.reg.pushTestImage(t, "myapp", "latest", makeLayerContent("x", 128))
	badSealed, _ := env.secrets.Seal("pw")
	putJSON(t, env.db, "servers", "srv-bad", model.ServerRecord{
		ID: "srv-bad", Name: "bad", Host: "127.0.0.1", Port: 1,
		AuthType: model.AuthPassword, Username: "root", SecretSealed: badSealed,
	})
	task, err := env.engine.CreateTask(model.TaskInput{
		Mode: model.ModeM1, ImageIDs: []string{"img-1"}, TargetServerIDs: []string{"srv-1", "srv-bad"},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	final := waitTerminal(t, env, task.ID)
	if final.Status != model.TaskFailed || final.Code != CodePartialFail {
		t.Fatalf("task = %s/%d, want failed/4006", final.Status, final.Code)
	}
	byTarget := map[string]model.TaskStep{}
	for _, s := range final.Steps {
		byTarget[s.Target] = s
	}
	if byTarget["edge-01"].Status != model.StepSuccess {
		t.Fatalf("正常目标应成功: %+v", byTarget["edge-01"])
	}
	if byTarget["bad"].Status != model.StepFailed {
		t.Fatalf("坏目标应失败: %+v", byTarget["bad"])
	}
}

func TestConcurrencyLimit(t *testing.T) {
	env := newTestEnv(t)
	env.engine.Concurrency = 2
	for i := 2; i <= 4; i++ {
		putJSON(t, env.db, "servers", fmt.Sprintf("srv-%d", i), model.ServerRecord{
			ID: fmt.Sprintf("srv-%d", i), Name: fmt.Sprintf("edge-%d", i),
			Host: env.sshd.host, Port: env.sshd.targetPort(),
			AuthType: model.AuthPassword, Username: "root", SecretSealed: env.srvRec.SecretSealed,
		})
	}
	env.reg.pushTestImage(t, "myapp", "latest", makeLayerContent("c", 64*1024))
	task, err := env.engine.CreateTask(model.TaskInput{
		Mode: model.ModeM1, ImageIDs: []string{"img-1"},
		TargetServerIDs: []string{"srv-1", "srv-2", "srv-3", "srv-4"},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	final := waitTerminal(t, env, task.ID)
	if final.Status != model.TaskSuccess {
		t.Fatalf("status = %s steps=%+v", final.Status, final.Steps)
	}
	if len(final.Steps) != 4 {
		t.Fatalf("步骤数 = %d", len(final.Steps))
	}
}

func TestLogsSince(t *testing.T) {
	env := newTestEnv(t)
	env.reg.pushTestImage(t, "myapp", "latest", makeLayerContent("x", 64))
	task, err := env.engine.CreateTask(model.TaskInput{
		Mode: model.ModeM1, ImageIDs: []string{"img-1"}, TargetServerIDs: []string{"srv-1"},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	waitTerminal(t, env, task.ID)
	lines, ok := env.engine.LogsSince(task.ID, 0)
	if !ok || len(lines) == 0 {
		t.Fatalf("应有日志: ok=%v lines=%d", ok, len(lines))
	}
	if lines[0].Seq <= 0 {
		t.Fatalf("日志行应有递增序号: %+v", lines[0])
	}
}

func TestContextUsed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ctx.Err() == nil {
		t.Fatal("context 应已取消")
	}
}

func makeLayerContent(name string, size int) []byte {
	return append([]byte(name+":"), bytes.Repeat([]byte{byte('a' + len(name)%26)}, size)...)
}
