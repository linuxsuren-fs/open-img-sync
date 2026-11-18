// Package store 提供 bbolt 持久化封装。
// 库文件中不存在明文凭据：凡凭据字段必须经 SecretBox 加密后写入。
package store

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	bolt "go.etcd.io/bbolt"
)

// DB 是 bbolt 的薄封装，按 bucket + key 存取字节。
type DB struct {
	db   *bolt.DB
	path string
}

// Open 打开（必要时创建）数据库文件。父目录自动创建（0700）。
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("创建数据目录: %w", err)
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{})
	if err != nil {
		return nil, fmt.Errorf("打开数据库 %s: %w", path, err)
	}
	return &DB{db: db, path: path}, nil
}

// Path 返回库文件路径。
func (d *DB) Path() string { return d.path }

// Close 关闭数据库。
func (d *DB) Close() error { return d.db.Close() }

// EnsureBuckets 确保给定 bucket 存在。
func (d *DB) EnsureBuckets(names ...string) error {
	return d.db.Update(func(tx *bolt.Tx) error {
		for _, name := range names {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return fmt.Errorf("创建 bucket %s: %w", name, err)
			}
		}
		return nil
	})
}

// Put 写入 key/value。
func (d *DB) Put(bucket, key string, value []byte) error {
	return d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket %s 不存在", bucket)
		}
		return b.Put([]byte(key), value)
	})
}

// Get 读取；不存在返回 nil, nil。
func (d *DB) Get(bucket, key string) ([]byte, error) {
	var out []byte
	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket %s 不存在", bucket)
		}
		raw := b.Get([]byte(key))
		if raw != nil {
			out = bytes.Clone(raw)
		}
		return nil
	})
	return out, err
}

// Delete 删除；key 不存在不是错误。
func (d *DB) Delete(bucket, key string) error {
	return d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket %s 不存在", bucket)
		}
		return b.Delete([]byte(key))
	})
}

// ForEach 遍历 bucket 内全部 key/value（升序）。
func (d *DB) ForEach(bucket string, fn func(key string, value []byte) error) error {
	return d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket %s 不存在", bucket)
		}
		return b.ForEach(func(k, v []byte) error {
			return fn(string(k), bytes.Clone(v))
		})
	})
}

// ViewGroup 在一个只读事务里执行 fn，减少重复打开事务的样板。
func (d *DB) ViewGroup(fn func(tx *bolt.Tx) error) error { return d.db.View(fn) }
