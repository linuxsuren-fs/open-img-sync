package sshx

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/linuxsuren/open-img-sync/internal/errs"
)

// 同步段（4xxx）中由 SSH 边界判定的错误码（与 docs/error-codes.md 对齐）。
const (
	CodeDiskFull   = 4003 // 目标机磁盘已满
	CodeLoadFailed = 4004 // 目标机加载镜像失败
)

func errsWrap(code int, msg string, cause error) error { return errs.Wrap(code, msg, cause) }

// StreamSession 一条带 stdin 管道的远端命令会话（docker load 流式灌入用）。
type StreamSession struct {
	session *ssh.Session
	stdin   interface {
		Write(p []byte) (int, error)
		Close() error
	}
	stderr strings.Builder
	done   chan error
}

// StartStream 启动远端命令，返回可写的 stdin。
// stderr 被捕获；Wait 返回执行结果（退出码非零视为错误）。
func (c *Client) StartStream(_ context.Context, command string) (*StreamSession, error) {
	session, err := c.conn.NewSession()
	if err != nil {
		return nil, errsWrap(CodeUnreachable, "SSH 会话建立失败", err)
	}
	stdinPipe, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return nil, errsWrap(CodeUnreachable, "SSH 管道建立失败", err)
	}
	s := &StreamSession{session: session, stdin: stdinPipe, done: make(chan error, 1)}
	session.Stderr = &s.stderr
	if err := session.Start(command); err != nil {
		session.Close()
		return nil, errsWrap(CodeDockerMissing, "远端命令启动失败", err)
	}
	go func() {
		s.done <- session.Wait()
	}()
	return s, nil
}

// Stdin 返回远端进程标准输入。
func (s *StreamSession) Stdin() interface {
	Write(p []byte) (int, error)
	Close() error
} {
	return s.stdin
}

// Stderr 返回已捕获的远端 stderr。
func (s *StreamSession) Stderr() string { return s.stderr.String() }

// Wait 等待命令结束；退出码非零时按 stderr 内容分类错误码。
func (s *StreamSession) Wait() error {
	err := <-s.done
	_ = s.stdin.Close()
	if err == nil {
		return nil
	}
	stderr := s.stderr.String()
	switch {
	case strings.Contains(stderr, "no space left"):
		return errsWrap(CodeDiskFull, "目标机磁盘已满", fmt.Errorf("%s", stderr))
	default:
		return errsWrap(CodeLoadFailed, "目标机加载镜像失败", fmt.Errorf("%s", stderr))
	}
}

// Close 释放会话资源。
func (s *StreamSession) Close() {
	_ = s.session.Close()
}

// HasDigest 检查目标机是否已存在指定 digest 的镜像（幂等判断）。
func (c *Client) HasDigest(ctx context.Context, ref, digest string) (bool, error) {
	out, exit, err := c.RunOutput(ctx, fmt.Sprintf(
		"docker image inspect --format '{{json .RepoDigests}}' %q 2>/dev/null", ref))
	if err != nil || exit != 0 {
		return false, nil // 不存在（或 inspect 失败按不存在处理，走完整同步）
	}
	return strings.Contains(out, digest), nil
}
