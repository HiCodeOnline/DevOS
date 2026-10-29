package desktop

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

func TestSelectWindowsAsset(t *testing.T) {
	assets := []github.ReleaseAsset{
		{Name: "devos_1.2.0_windows-x64.zip"},
		{Name: "devos_1.2.0_windows-x64.exe"},
		{Name: "devos_1.2.0_windows-arm64.exe"},
		{Name: "devos_1.2.0_linux-amd64.tar.gz"},
		{Name: "devos_1.2.0_web-linux-amd64.tar.gz"},
	}
	cases := []struct {
		arch string
		want string
	}{
		{"amd64", "devos_1.2.0_windows-x64.exe"},
		{"arm64", "devos_1.2.0_windows-arm64.exe"},
	}
	for _, c := range cases {
		idx := selectWindowsAsset(updater.CheckRequest{Platform: "windows", Arch: c.arch}, assets)
		if idx < 0 {
			t.Fatalf("arch %s: 未找到可更新的资产", c.arch)
		}
		if got := assets[idx].Name; got != c.want {
			t.Fatalf("arch %s: 选中 %q，期望 %q", c.arch, got, c.want)
		}
	}
}

func TestSelectWindowsAssetSkipsArchives(t *testing.T) {
	// 只有 zip 时不应选中：归档含多个顶层条目，无法直接替换进程。
	assets := []github.ReleaseAsset{{Name: "devos_1.2.0_windows-x64.zip"}}
	if idx := selectWindowsAsset(updater.CheckRequest{Platform: "windows", Arch: "amd64"}, assets); idx != -1 {
		t.Fatalf("期望不选中 zip，实际选中 %q", assets[idx].Name)
	}
}
