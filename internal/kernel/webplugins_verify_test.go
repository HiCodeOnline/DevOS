package kernel

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildWebAppNextDemo(t *testing.T) {
	base := writeTree(t, filepath.Join(t.TempDir(), "nextdemo"), nextDemoFiles("1.0.0"))

	wa, err := buildWebApp(base)
	if err != nil {
		t.Fatalf("buildWebApp: %v", err)
	}
	if wa.manifest.ID != "nextdemo" || wa.manifest.StaticRoot != "out" || wa.manifest.Entry != "index.html" {
		t.Fatalf("清单规范化异常: %+v", wa.manifest)
	}
	if wa.manifest.Icon != "/apps/nextdemo/icon.svg" {
		t.Fatalf("icon = %q", wa.manifest.Icon)
	}

	cases := map[string]string{
		"":                           "index.html",
		"about":                      "about.html",
		"about/":                     "about.html",
		"_next/static/chunks/app.js": "_next/static/chunks/app.js",
	}
	for rel, want := range cases {
		name, ok := wa.resolveAsset(rel)
		if !ok || name != want {
			t.Fatalf("resolveAsset(%q) = %q,%v，期望 %q", rel, name, ok, want)
		}
	}
	if name, ok := wa.resolveAsset("no-such-page"); !ok || name != "index.html" {
		t.Fatalf("SPA 回退应为 index.html，实际 %q", name)
	}

	wa.spa = false
	if _, ok := wa.resolveAsset("no-such-page"); ok {
		t.Fatal("关闭 SPA 后不应回退")
	}
	if _, ok := wa.resolveAsset("../plugin.json"); ok {
		t.Fatal("越界路径不应命中")
	}
}

func TestBuildWebAppInvalid(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, filepath.Join(dir, "badicon"), map[string]string{
		"plugin.json":    `{"id":"badicon","name":"Bad Icon","version":"1.0.0","icon":"missing.png","entry":"index.html"}`,
		"web/index.html": "<h1>bad icon</h1>",
	})
	writeTree(t, filepath.Join(dir, "badspec"), map[string]string{
		"plugin.json":    `{"id":"badspec","name":"Bad Spec","version":"1.0.0","entry":"index.html","specVersion":2}`,
		"web/index.html": "<h1>bad spec</h1>",
	})
	for _, name := range []string{"badicon", "badspec"} {
		if _, err := buildWebApp(filepath.Join(dir, name)); err == nil {
			t.Fatalf("%s 应当校验失败", name)
		} else {
			t.Logf("%s 校验失败（预期）: %v", name, err)
		}
	}
}

func TestExtractZipSlipRejected(t *testing.T) {
	zipPath := makeZip(t, filepath.Join(t.TempDir(), "slip.zip"), map[string]string{
		"plugin.json": `{"id":"slip","name":"Slip","version":"1.0.0","entry":"index.html"}`,
		"../evil.txt": "pwned",
	})
	err := extractZip(zipPath, t.TempDir())
	if err == nil {
		t.Fatal("Zip Slip 应当被拒绝")
	}
	if !strings.Contains(err.Error(), "越界") {
		t.Fatalf("错误信息不符合预期: %v", err)
	}
}

func TestExtractAndLocatePluginRoot(t *testing.T) {
	zipPath := nextDemoZip(t, "1.0.0")
	dst := t.TempDir()
	if err := extractZip(zipPath, dst); err != nil {
		t.Fatalf("extractZip: %v", err)
	}
	root, err := locatePluginRoot(dst)
	if err != nil {
		t.Fatalf("locatePluginRoot: %v", err)
	}
	if filepath.Base(root) != "nextdemo" {
		t.Fatalf("root = %q", root)
	}
	if _, err := os.Stat(filepath.Join(root, "out", "_next", "static", "chunks", "app.js")); err != nil {
		t.Fatalf("解压后缺少资源: %v", err)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct{ a, b, want string }{
		{"1.1.0", "1.0.0", "newer"},
		{"0.9.0", "1.0.0", "older"},
		{"1.0.0", "1.0.0", "same"},
		{"v1.0.0", "1.0", "same"},
		{"1.0.1", "1.0", "newer"},
	}
	for _, tc := range cases {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Fatalf("compareVersions(%q,%q)=%q want %q", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestZipSymlinkRejected(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "symlink.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	hdr := &zip.FileHeader{Name: "link", Method: zip.Store}
	hdr.SetMode(os.ModeSymlink | 0o777)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("../../etc/passwd"))
	_ = zw.Close()
	_ = f.Close()

	if err := extractZip(zipPath, t.TempDir()); err == nil {
		t.Fatal("符号链接应当被拒绝")
	}
}
