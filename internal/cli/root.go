// Package cli 承载子命令分发：serve（默认）、version、self-check。
package cli

import (
	"flag"
	"fmt"
)

// 版本变量由构建脚本经 -ldflags 注入（见 Makefile），缺省为开发态。
var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

// Run 解析并执行子命令。无参数时默认执行 serve，行为与显式 serve 完全一致
// （该默认行为属于对外契约，见 docs/architecture.md 决策点 D）。
func Run(args []string) error {
	if len(args) == 0 {
		return serve(args)
	}
	switch args[0] {
	case "serve":
		return serve(args[1:])
	case "version":
		return printVersion()
	case "self-check":
		return selfCheck(args[1:])
	default:
		fs := flag.NewFlagSet("open-img-sync", flag.ContinueOnError)
		showVersion := fs.Bool("version", false, "输出版本信息")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *showVersion {
			return printVersion()
		}
		return fmt.Errorf("未知子命令 %q，可用：serve、version", args[0])
	}
}

func printVersion() error {
	fmt.Printf("open-img-sync %s\ncommit: %s\nbuild time: %s\n", version, commit, buildTime)
	return nil
}
