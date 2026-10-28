package kernel

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gofiber/fiber/v2"

	"devos/internal/datadir"
)

func TestStorageInfoAndBackup(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	k := newSecurityKernel(t)
	if err := os.WriteFile(filepath.Join(k.dataDir, "config.json"), []byte(`{"port":7788}`), 0o600); err != nil {
		t.Fatal(err)
	}

	res := request(t, k, "GET", "/api/storage/info", "", nil, devosHeaders(nil))
	if res.Code != fiber.StatusOK {
		t.Fatalf("info = %d %s", res.Code, res.Body)
	}
	var info struct {
		DataDir string `json:"dataDir"`
		Files   int    `json:"files"`
	}
	if err := json.Unmarshal(res.Body, &info); err != nil {
		t.Fatalf("解析 info: %v", err)
	}
	if info.DataDir != k.dataDir || info.Files < 1 {
		t.Fatalf("info 内容不符: %+v", info)
	}

	// 备份接口要求来源标识。
	if res := request(t, k, "GET", "/api/storage/backup", "", nil, nil); res.Code != fiber.StatusBadRequest {
		t.Fatalf("缺少请求标识应返回 400，实际 %d", res.Code)
	}
	res = request(t, k, "GET", "/api/storage/backup", "", nil, devosHeaders(nil))
	if res.Code != fiber.StatusOK {
		t.Fatalf("backup = %d %s", res.Code, res.Body)
	}
	if !bytes.HasPrefix(res.Body, []byte("PK")) {
		t.Fatalf("备份内容不是 zip 归档")
	}
}

func TestStorageMigrateValidation(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	k := newSecurityKernel(t)

	body, _ := json.Marshal(map[string]string{"dir": filepath.Join(k.dataDir, "nested")})
	res := request(t, k, "POST", "/api/storage/migrate", string(body), nil, devosHeaders(nil))
	if res.Code != fiber.StatusBadRequest {
		t.Fatalf("嵌套目录应返回 400，实际 %d %s", res.Code, res.Body)
	}
	if _, target := datadir.PendingState(); target != "" {
		t.Fatalf("非法目标不应排入队列: %s", target)
	}

	body, _ = json.Marshal(map[string]string{"dir": filepath.Join(t.TempDir(), "devos-data")})
	res = request(t, k, "POST", "/api/storage/migrate", string(body), nil, devosHeaders(nil))
	if res.Code != fiber.StatusOK {
		t.Fatalf("迁移入队失败: %d %s", res.Code, res.Body)
	}
	if _, target := datadir.PendingState(); target == "" {
		t.Fatal("迁移目标应记录在队列中")
	}

	res = request(t, k, "POST", "/api/storage/pending/cancel", "", nil, devosHeaders(nil))
	if res.Code != fiber.StatusOK {
		t.Fatalf("取消任务失败: %d %s", res.Code, res.Body)
	}
	if restore, target := datadir.PendingState(); restore || target != "" {
		t.Fatal("取消后不应残留任务")
	}
}

func TestStorageRestoreRejectsMissingFile(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	k := newSecurityKernel(t)
	res := request(t, k, "POST", "/api/storage/restore", "", nil, devosHeaders(nil))
	if res.Code != fiber.StatusBadRequest {
		t.Fatalf("缺少文件应返回 400，实际 %d %s", res.Code, res.Body)
	}
}
