// Package sshx 封装 SSH 连接、命令执行与连通性探测（#2）。
// 错误分类面向稳定错误码：服务器不可达 / 认证失败 / Docker 不可用 / 超时。
package sshx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/linuxsuren/open-img-sync/internal/errs"
)

// 服务器段（2xxx）错误码。
const (
	CodeUnreachable   = 2001 // 服务器不可达
	CodeAuthFailed    = 2002 // SSH 认证失败
	CodeDockerMissing = 2003 // 目标机 Docker 不可用
	CodeTimeout       = 2004 // 连接超时
)

// Options 一次 SSH 连接的全部参数。
type Options struct {
	Host       string
	Port       int
	Username   string
	AuthType   string // model.AuthPassword / model.AuthPrivateKey
	Password   string
	PrivateKey string
}

// DefaultTimeout 连通性测试默认超时，可被 Options.Timeout 覆盖。
const DefaultTimeout = 10 * time.Second

// Client 是一条已建立的 SSH 连接。
type Client struct {
	conn *ssh.Client
}

// Dial 建立连接；返回的错误已分类为 errs.E（技术根因在 cause 里，只进日志）。
func Dial(ctx context.Context, opt Options) (*Client, error) {
	timeout := DefaultTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 {
			timeout = remaining
		}
	}
	var authMethods []ssh.AuthMethod
	switch opt.AuthType {
	case "password":
		authMethods = append(authMethods, ssh.Password(opt.Password))
	case "private_key":
		signer, err := signerFromPEM(opt.PrivateKey)
		if err != nil {
			return nil, errs.Wrap(errs.CodeBadRequest, "私钥格式不合法", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	default:
		return nil, errs.New(errs.CodeBadRequest, "认证方式必须是 password 或 private_key")
	}

	cfg := &ssh.ClientConfig{
		User:            opt.Username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // 内网受管主机，暂不做 known_hosts 校验
		Timeout:         timeout,
	}
	addr := net.JoinHostPort(opt.Host, fmt.Sprintf("%d", opt.Port))
	if opt.Port == 0 {
		addr = net.JoinHostPort(opt.Host, "22")
	}
	conn, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, classifyDialError(err, timeout, addr)
	}
	return &Client{conn: conn}, nil
}

// classifyDialError 把拨号/握手错误映射为稳定错误码；提示携带目标地址与超时值，
// 用户一眼能看出“哪台机器、哪种失败”。
func classifyDialError(err error, timeout time.Duration, addr string) error {
	if err == nil {
		return nil
	}
	timeoutText := fmt.Sprintf("，上限 %s", timeout)
	msg := err.Error()
	switch {
	case strings.Contains(msg, "unable to authenticate"),
		strings.Contains(msg, "no supported methods remain"):
		return errs.Wrap(CodeAuthFailed, "SSH 认证失败（"+addr+"）：账号或私钥被拒绝", err)
	case isTimeoutErr(err), strings.Contains(msg, "i/o timeout"):
		return errs.Wrap(CodeTimeout, "SSH 连接超时（"+addr+timeoutText+"）：目标机无响应", err)
	case strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "no route to host"),
		strings.Contains(msg, "unreachable"),
		strings.Contains(msg, "Unknown user"):
		return errs.Wrap(CodeUnreachable, "服务器不可达（"+addr+"）：连接被拒绝或无路由", err)
	default:
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return errs.Wrap(CodeTimeout, "SSH 连接超时（"+addr+timeoutText+"）：目标机无响应", err)
		}
		// 其余网络类错误统一归为不可达，根因进日志
		return errs.Wrap(CodeUnreachable, "服务器不可达（"+addr+"）", err)
	}
}

func signerFromPEM(pem string) (ssh.Signer, error) {
	return ssh.ParsePrivateKey([]byte(pem))
}

// isTimeoutErr 判断错误链中是否携带 net.Error 且超时。
func isTimeoutErr(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// Close 关闭连接。
func (c *Client) Close() error { return c.conn.Close() }

// RunOutput 在目标机执行命令，返回合并输出与退出码；exitCode -1 表示会话层失败。
func (c *Client) RunOutput(ctx context.Context, command string) (output string, exitCode int, err error) {
	session, err := c.conn.NewSession()
	if err != nil {
		return "", -1, errs.Wrap(CodeUnreachable, "SSH 会话建立失败", err)
	}
	defer session.Close()

	done := make(chan error, 1)
	go func() {
		combined, e := session.CombinedOutput(command)
		output = string(combined)
		if e != nil {
			if exitErr, ok := e.(*ssh.ExitError); ok {
				exitCode = exitErr.ExitStatus()
				e = nil
			} else {
				exitCode = -1
			}
		} else {
			exitCode = 0
		}
		done <- e
	}()
	select {
	case <-ctx.Done():
		_ = session.Close()
		return output, -1, errs.Wrap(CodeTimeout, "命令执行超时", ctx.Err())
	case e := <-done:
		return output, exitCode, e
	}
}

// TestResult 连通性测试结果（对外契约）。
type TestResult struct {
	Reachable bool   `json:"reachable"`
	DockerOK  bool   `json:"docker_ok"`
	LatencyMS int64  `json:"latency_ms"`
	Code      int    `json:"code"`
	Message   string `json:"message"`
}

// TestDocker 探测：SSH 握手 + `docker version`，任一失败按稳定错误码返回结果。
func TestDocker(ctx context.Context, opt Options) TestResult {
	start := time.Now()
	client, err := Dial(ctx, opt)
	if err != nil {
		return testResultFromErr(err, time.Since(start))
	}
	defer client.Close()
	latency := time.Since(start).Milliseconds()

	out, exit, err := client.RunOutput(ctx, "docker version --format '{{.Server.Version}}'")
	if err != nil {
		return TestResult{Reachable: true, DockerOK: false, LatencyMS: latency,
			Code: errs.CodeInternal, Message: "探测命令执行失败"}
	}
	if exit != 0 || strings.TrimSpace(out) == "" {
		return TestResult{Reachable: true, DockerOK: false, LatencyMS: latency,
			Code: CodeDockerMissing, Message: "目标机 Docker 不可用"}
	}
	return TestResult{Reachable: true, DockerOK: true, LatencyMS: latency,
		Code: 0, Message: "可达，Docker " + strings.TrimSpace(out)}
}

func testResultFromErr(err error, since time.Duration) TestResult {
	e := errs.From(err)
	return TestResult{Reachable: false, DockerOK: false, LatencyMS: since.Milliseconds(),
		Code: e.Code, Message: e.Message}
}
