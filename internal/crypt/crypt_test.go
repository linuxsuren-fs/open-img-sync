package crypt

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func newTestBox(t *testing.T) *Box {
	t.Helper()
	key, _, err := LoadMasterKey(MasterKeyConfig{EnvVar: "OIS_TEST_KEY_UNUSED", FilePath: filepath.Join(t.TempDir(), "master.key")})
	if err != nil {
		t.Fatalf("LoadMasterKey: %v", err)
	}
	return NewBox(key)
}

func TestSealOpenRoundtrip(t *testing.T) {
	box := newTestBox(t)
	for _, plain := range []string{"p@ssw0rd!", "-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END-----", "中文凭据"} {
		sealed, err := box.Seal(plain)
		if err != nil {
			t.Fatalf("Seal(%q): %v", plain, err)
		}
		if sealed == plain || bytes.Contains([]byte(sealed), []byte(plain)) {
			t.Fatalf("密文包含明文: %q", sealed)
		}
		got, err := box.Open(sealed)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if got != plain {
			t.Fatalf("往返不一致: got %q want %q", got, plain)
		}
	}
}

func TestSealEmptyStaysEmpty(t *testing.T) {
	box := newTestBox(t)
	sealed, err := box.Seal("")
	if err != nil || sealed != "" {
		t.Fatalf("空串应保持空串, got %q err %v", sealed, err)
	}
	got, err := box.Open("")
	if err != nil || got != "" {
		t.Fatalf("空串解密应返回空串, got %q err %v", got, err)
	}
}

func TestOpenTamperFails(t *testing.T) {
	box := newTestBox(t)
	sealed, _ := box.Seal("secret")
	// 篡改最后一个字符
	tampered := sealed[:len(sealed)-2] + "A="
	if _, err := box.Open(tampered); err == nil {
		t.Fatal("篡改后的密文应解密失败")
	}
}

func TestOpenWrongKeyFails(t *testing.T) {
	box1 := newTestBox(t)
	box2 := newTestBox(t)
	sealed, _ := box1.Seal("secret")
	if _, err := box2.Open(sealed); err == nil {
		t.Fatal("不同主密钥应解密失败")
	}
}

func TestMasterKeyEnvPriority(t *testing.T) {
	envKey := make([]byte, KeyLen)
	for i := range envKey {
		envKey[i] = byte(i)
	}
	t.Setenv("OIS_TEST_KEY", base64.StdEncoding.EncodeToString(envKey))
	keyFile := filepath.Join(t.TempDir(), "master.key")
	// 预置一个无效的文件密钥，证明 env 优先且未被读取
	if err := os.WriteFile(keyFile, []byte("not-a-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, source, err := LoadMasterKey(MasterKeyConfig{EnvVar: "OIS_TEST_KEY", FilePath: keyFile})
	if err != nil {
		t.Fatalf("env 优先装载应成功: %v", err)
	}
	if source != "env" {
		t.Fatalf("source = %q, want env", source)
	}
	if string(key[:]) != string(envKey) {
		t.Fatal("装载的密钥与环境变量不一致")
	}
}

func TestMasterKeyInvalidEnv(t *testing.T) {
	t.Setenv("OIS_TEST_KEY", "not-base64-32")
	_, _, err := LoadMasterKey(MasterKeyConfig{EnvVar: "OIS_TEST_KEY", FilePath: filepath.Join(t.TempDir(), "k")})
	if err == nil {
		t.Fatal("非法环境变量密钥应报错")
	}
}

func TestMasterKeyAutoGenerateFile(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "master.key")
	key1, source, err := LoadMasterKey(MasterKeyConfig{EnvVar: "OIS_TEST_KEY_UNUSED", FilePath: keyFile})
	if err != nil {
		t.Fatalf("自动生成: %v", err)
	}
	if source != "file" {
		t.Fatalf("source = %q, want file", source)
	}
	// 再次装载应得到同一密钥（稳定）
	key2, _, err := LoadMasterKey(MasterKeyConfig{EnvVar: "OIS_TEST_KEY_UNUSED", FilePath: keyFile})
	if err != nil {
		t.Fatalf("二次装载: %v", err)
	}
	if string(key1[:]) != string(key2[:]) {
		t.Fatal("二次装载的密钥应一致")
	}
	if runtime.GOOS != "windows" { // Windows 不维护 POSIX 权限位
		info, err := os.Stat(keyFile)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("密钥文件权限 = %v, want 0600", info.Mode().Perm())
		}
	}
}

func TestMaskAndFingerprint(t *testing.T) {
	if got := Mask("ssh", "whatever-long-secret"); got != "ssh-****" {
		t.Fatalf("Mask = %q", got)
	}
	if got := Mask("ssh", ""); got != "" {
		t.Fatalf("空密钥 Mask 应为空串, got %q", got)
	}
	fp := Fingerprint("secret-a")
	if fp == Fingerprint("secret-b") {
		t.Fatal("不同凭据指纹不应相同")
	}
	if len(fp) != len("sha256:")+8 {
		t.Fatalf("指纹长度异常: %q", fp)
	}
	if Fingerprint("") != "" {
		t.Fatal("空凭据指纹应为空串")
	}
}
