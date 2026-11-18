// Package archive 把 registry 的 manifest + blob 组装为 docker load 兼容的
// tar（M1 模式核心，#5）。
//
// tar 条目必须先声明大小，而 registry 只给出压缩层大小，因此每层先解压到
// 临时文件（spool），再以已知大小写入 tar：磁盘占用上限 = 最大一层，写完即删，
// 不产生整镜像的中间文件。SSH 管道侧仍是流式的（边写 tar 边传输）。
package archive

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"strings"
)

// MediaTypeGzipLayer gzip 压缩层（docker schema2 / OCI 默认）。
const MediaTypeGzipLayer = "application/vnd.docker.image.rootfs.diff.tar.gzip"

// hexOf 取 digest 的十六进制部分（sha256:xxx → xxx）。
func hexOf(digest string) string {
	if idx := strings.Index(digest, ":"); idx >= 0 {
		return digest[idx+1:]
	}
	return digest
}

// DecompressTo 把 registry blob 流解压（gzip 层）写入 w；onRead 以压缩侧字节
// 为口径回调进度。返回未压缩字节数。
func DecompressTo(mediaType string, blob io.Reader, onRead func(delta int64), w io.Writer) (int64, error) {
	var src io.Reader = blob
	if onRead != nil {
		src = &countingReader{r: blob, f: onRead}
	}
	if strings.Contains(mediaType, "gzip") {
		gz, err := gzip.NewReader(src)
		if err != nil {
			return 0, err
		}
		defer gz.Close()
		return io.Copy(w, gz)
	}
	return io.Copy(w, src)
}

type countingReader struct {
	r io.Reader
	f func(delta int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 && c.f != nil {
		c.f(int64(n))
	}
	return n, err
}

// LoadWriter 产出 docker save 形态的 tar：
//
//	<config-hex>.json        config blob 内容
//	<layer-hex>/layer.tar    解压后的层
//	manifest.json            [{Config, RepoTags, Layers}]
type LoadWriter struct {
	tw *tar.Writer
}

// NewLoadWriter 构造。
func NewLoadWriter(w io.Writer) *LoadWriter {
	return &LoadWriter{tw: tar.NewWriter(w)}
}

// WriteConfig 写入 config blob（小文件，整块读入）。
func (lw *LoadWriter) WriteConfig(digest string, content []byte) error {
	if err := lw.tw.WriteHeader(&tar.Header{
		Name: hexOf(digest) + ".json", Mode: 0o600, Size: int64(len(content)),
	}); err != nil {
		return err
	}
	_, err := lw.tw.Write(content)
	return err
}

// WriteLayerFile 把已解压到磁盘的层写入 tar（name = <layer-hex>/layer.tar）。
func (lw *LoadWriter) WriteLayerFile(digest, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if err := lw.tw.WriteHeader(&tar.Header{
		Name: hexOf(digest) + "/layer.tar", Mode: 0o600, Size: info.Size(),
	}); err != nil {
		return err
	}
	_, err = io.Copy(lw.tw, f)
	return err
}

// Finish 写入 manifest.json 并收尾 tar。
func (lw *LoadWriter) Finish(repoTag, configDigest string, layerDigests []string) error {
	layerNames := make([]string, 0, len(layerDigests))
	for _, d := range layerDigests {
		layerNames = append(layerNames, hexOf(d)+"/layer.tar")
	}
	manifest := []map[string]any{{
		"Config":   hexOf(configDigest) + ".json",
		"RepoTags": []string{repoTag},
		"Layers":   layerNames,
	}}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := lw.tw.WriteHeader(&tar.Header{
		Name: "manifest.json", Mode: 0o600, Size: int64(len(raw)),
	}); err != nil {
		return err
	}
	if _, err := lw.tw.Write(raw); err != nil {
		return err
	}
	return lw.tw.Close()
}

// SHA256Hex 计算内容摘要（校验用）。
func SHA256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}
