package sshx

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/linuxsuren/open-img-sync/internal/model"
)

// fakeSSHServer 在进程内起一个真实握手路径的 SSH 服务，用于连通性分类测试。
type fakeSSHServer struct {
	host       string
	port       int
	password   string
	publicKey  ssh.PublicKey
	dockerOut  string
	dockerExit int
}

func startFakeSSHServer(t *testing.T, cfg fakeSSHServer) *fakeSSHServer {
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
		PasswordCallback: func(conn ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if cfg.password != "" && string(pw) == cfg.password {
				return nil, nil
			}
			return nil, fmt.Errorf("password rejected for %q", conn.User())
		},
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if cfg.publicKey != nil && string(key.Marshal()) == string(cfg.publicKey.Marshal()) {
				return nil, nil
			}
			return nil, fmt.Errorf("public key rejected")
		},
	}
	srvCfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	s := &cfg
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleFakeConn(conn, srvCfg, s.dockerOut, s.dockerExit)
		}
	}()
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	s.host, s.port = host, port
	return s
}

func handleFakeConn(c net.Conn, cfg *ssh.ServerConfig, dockerOut string, dockerExit int) {
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
		go func() {
			for req := range chReqs {
				if req.Type != "exec" {
					_ = req.Reply(false, nil)
					continue
				}
				_ = req.Reply(true, nil)
				var cmd struct{ Command string }
				_ = ssh.Unmarshal(req.Payload, &cmd)
				if dockerExit == 0 {
					_, _ = ch.Write([]byte(dockerOut))
				} else {
					_, _ = ch.Stderr().Write([]byte("docker: command not found\n"))
				}
				_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(dockerExit)}))
				_ = ch.Close()
			}
		}()
	}
}

func (s *fakeSSHServer) options() Options {
	return Options{Host: s.host, Port: s.port, Username: "root", AuthType: model.AuthPassword, Password: s.password}
}

func TestTestDockerSuccess(t *testing.T) {
	srv := startFakeSSHServer(t, fakeSSHServer{password: "pw", dockerOut: "24.0.7", dockerExit: 0})
	res := TestDocker(context.Background(), srv.options())
	if !res.Reachable || !res.DockerOK || res.Code != 0 {
		t.Fatalf("result = %+v", res)
	}
	if res.Message != "可达，Docker 24.0.7" {
		t.Fatalf("message = %q", res.Message)
	}
}

func TestTestDockerAuthFailed(t *testing.T) {
	srv := startFakeSSHServer(t, fakeSSHServer{password: "correct", dockerOut: "24.0.7"})
	res := TestDocker(context.Background(), Options{
		Host: srv.host, Port: srv.port, Username: "root",
		AuthType: model.AuthPassword, Password: "wrong",
	})
	if res.Reachable || res.DockerOK {
		t.Fatalf("认证失败不应可达: %+v", res)
	}
	if res.Code != CodeAuthFailed {
		t.Fatalf("code = %d, want %d（SSH 认证失败）", res.Code, CodeAuthFailed)
	}
}

func TestTestDockerMissing(t *testing.T) {
	srv := startFakeSSHServer(t, fakeSSHServer{password: "pw", dockerExit: 127})
	res := TestDocker(context.Background(), srv.options())
	if !res.Reachable || res.DockerOK {
		t.Fatalf("Docker 缺失应 reachable=true docker_ok=false: %+v", res)
	}
	if res.Code != CodeDockerMissing {
		t.Fatalf("code = %d, want %d", res.Code, CodeDockerMissing)
	}
}

func TestTestDockerPrivateKeyAuth(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		t.Fatal(err)
	}
	srv := startFakeSSHServer(t, fakeSSHServer{password: "unused", publicKey: pub, dockerOut: "25.0.1"})
	pemBlock, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	res := TestDocker(context.Background(), Options{
		Host: srv.host, Port: srv.port, Username: "root",
		AuthType: model.AuthPrivateKey, PrivateKey: string(pem.EncodeToMemory(pemBlock)),
	})
	if !res.Reachable || !res.DockerOK {
		t.Fatalf("私钥认证应成功: %+v", res)
	}
}

func TestTestDockerUnreachable(t *testing.T) {
	// 找一个必然拒绝连接的本机端口
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	ln.Close()

	res := TestDocker(context.Background(), Options{
		Host: host, Port: port, Username: "root", AuthType: model.AuthPassword, Password: "x",
	})
	if res.Code != CodeUnreachable {
		t.Fatalf("closed port code = %d, want %d", res.Code, CodeUnreachable)
	}
}

func TestTestDockerTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	res := TestDocker(ctx, Options{
		Host: "10.255.255.1", Port: 22, Username: "root", AuthType: model.AuthPassword, Password: "x",
	})
	if res.Code != CodeTimeout {
		t.Fatalf("code = %d, want %d（连接超时）", res.Code, CodeTimeout)
	}
}
