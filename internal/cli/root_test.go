package cli

import (
	"strings"
	"testing"
)

func TestVersionSubcommand(t *testing.T) {
	if err := Run([]string{"version"}); err != nil {
		t.Fatalf("version 子命令不应报错: %v", err)
	}
}

func TestUnknownSubcommand(t *testing.T) {
	err := Run([]string{"no-such-cmd"})
	if err == nil {
		t.Fatal("未知子命令应返回错误")
	}
	if !strings.Contains(err.Error(), "no-such-cmd") {
		t.Fatalf("错误信息应包含子命令名: %v", err)
	}
}
