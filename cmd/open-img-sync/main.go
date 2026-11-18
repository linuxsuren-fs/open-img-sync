// open-img-sync 把 Docker 镜像同步到无法直连外网的服务器。
//
// 命令形态：
//
//	open-img-sync           # 无参数等价于 serve
//	open-img-sync serve     # 启动 Web + API 服务
//	open-img-sync version   # 输出版本信息（与 -version 等价）
package main

import (
	"fmt"
	"os"

	"github.com/linuxsuren/open-img-sync/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "open-img-sync:", err)
		os.Exit(1)
	}
}
