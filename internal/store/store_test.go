package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/linuxsuren/open-img-sync/internal/crypt"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.EnsureBuckets("t"); err != nil {
		t.Fatalf("EnsureBuckets: %v", err)
	}
	return db
}

func TestPutGetDelete(t *testing.T) {
	db := openTestDB(t)
	if err := db.Put("t", "k1", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	got, err := db.Get("t", "k1")
	if err != nil || string(got) != "v1" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if err := db.Delete("t", "k1"); err != nil {
		t.Fatal(err)
	}
	got, err = db.Get("t", "k1")
	if err != nil || got != nil {
		t.Fatalf("删除后 Get 应为 nil, got %q, %v", got, err)
	}
	if err := db.Delete("t", "missing"); err != nil {
		t.Fatalf("删除不存在的 key 不应报错: %v", err)
	}
}

func TestForEach(t *testing.T) {
	db := openTestDB(t)
	for _, kv := range [][2]string{{"a", "1"}, {"b", "2"}} {
		if err := db.Put("t", kv[0], []byte(kv[1])); err != nil {
			t.Fatal(err)
		}
	}
	var keys []string
	if err := db.ForEach("t", func(k string, _ []byte) error {
		keys = append(keys, k)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] != "a" || keys[1] != "b" {
		t.Fatalf("ForEach keys = %v", keys)
	}
}

// TestDBFileContainsNoPlaintext 锁定 #10 验收项：持久化文件二进制检查不含明文凭据。
func TestDBFileContainsNoPlaintext(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.EnsureBuckets("servers"); err != nil {
		t.Fatal(err)
	}

	key, _, err := crypt.LoadMasterKey(crypt.MasterKeyConfig{EnvVar: "OIS_TEST_KEY_UNUSED", FilePath: filepath.Join(dir, "master.key")})
	if err != nil {
		t.Fatal(err)
	}
	box := NewSecretBox(crypt.NewBox(key))

	const password = "super-secret-password-不落明文"
	sealed, err := box.Seal(password)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟资源记录：只有密文字段入库
	if err := db.Put("servers", "srv-1", []byte(`{"secret":"`+sealed+`"}`)); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(db.Path())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(password)) {
		t.Fatal("库文件中出现了明文凭据")
	}
}

func TestSecretBoxRoundtrip(t *testing.T) {
	dir := t.TempDir()
	key, _, err := crypt.LoadMasterKey(crypt.MasterKeyConfig{EnvVar: "OIS_TEST_KEY_UNUSED", FilePath: filepath.Join(dir, "master.key")})
	if err != nil {
		t.Fatal(err)
	}
	box := NewSecretBox(crypt.NewBox(key))
	sealed, err := box.Seal("value")
	if err != nil {
		t.Fatal(err)
	}
	got, err := box.Open(sealed)
	if err != nil || got != "value" {
		t.Fatalf("往返: %q, %v", got, err)
	}
	if _, err := box.Open("!!!not-base64!!!"); err == nil {
		t.Fatal("非法密文应报错")
	}
}
