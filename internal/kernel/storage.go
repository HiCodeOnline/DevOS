package kernel

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/gofiber/fiber/v2"

	"devos/internal/datadir"
	"devos/internal/platform"
)

// registerStorageRoutes 注册数据目录的备份、还原与迁移接口。
func (k *Kernel) registerStorageRoutes(app *fiber.App) {
	api := app.Group("/api/storage")
	api.Get("/info", k.handleStorageInfo)
	api.Get("/backup", k.handleStorageBackup)
	api.Post("/restore", k.handleStorageRestore)
	api.Post("/migrate", k.handleStorageMigrate)
	api.Post("/browse", k.handleStorageBrowse)
	api.Post("/pending/cancel", k.handleStorageCancel)
}

func (k *Kernel) handleStorageInfo(c *fiber.Ctx) error {
	restore, migrate := datadir.PendingState()
	files, bytes, err := datadir.DirStats(k.dataDir)
	if err != nil {
		files, bytes = 0, 0
	}
	configDir, _ := datadir.ConfigRoot()
	backups, _ := datadir.BackupsDir()
	return c.JSON(fiber.Map{
		"dataDir":        k.dataDir,
		"configDir":      configDir,
		"backupDir":      backups,
		"files":          files,
		"bytes":          bytes,
		"pendingRestore": restore,
		"pendingMigrate": migrate,
		"version":        Version,
	})
}

func (k *Kernel) handleStorageBackup(c *fiber.Ctx) error {
	if err := k.verifyRequestOrigin(c); err != nil {
		return err
	}
	name := "devos-backup-" + time.Now().Format("20060102-150405") + ".zip"
	c.Set("Content-Type", "application/zip")
	c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	c.Set("Cache-Control", "no-store")
	// 直接压缩到响应流，避免在磁盘上留下临时归档。
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		if err := datadir.Backup(w, k.dataDir); err != nil {
			k.log.Error("备份写入响应失败", "err", err)
		}
		_ = w.Flush()
	})
	return nil
}

func (k *Kernel) handleStorageRestore(c *fiber.Ctx) error {
	if err := k.verifyRequestOrigin(c); err != nil {
		return err
	}
	file, err := c.FormFile("file")
	if err != nil {
		return storageError(c, fiber.StatusBadRequest, "missing_file", errors.New("请选择备份文件"))
	}
	if file.Size <= 0 {
		return storageError(c, fiber.StatusBadRequest, "empty_file", errors.New("备份文件为空"))
	}
	src, err := file.Open()
	if err != nil {
		return storageError(c, fiber.StatusBadRequest, "read_failed", errors.New("无法读取上传文件"))
	}
	root, err := datadir.ConfigRoot()
	if err != nil {
		return storageError(c, fiber.StatusInternalServerError, "root_failed", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return storageError(c, fiber.StatusInternalServerError, "root_failed", err)
	}
	upload := filepath.Join(root, "pending-restore.upload")
	out, err := os.Create(upload)
	if err != nil {
		src.Close()
		return storageError(c, fiber.StatusInternalServerError, "save_failed", err)
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		src.Close()
		_ = os.Remove(upload)
		return storageError(c, fiber.StatusInternalServerError, "save_failed", err)
	}
	out.Close()
	src.Close()
	if err := datadir.QueueRestore(upload); err != nil {
		_ = os.Remove(upload)
		return storageError(c, fiber.StatusBadRequest, "invalid_archive", err)
	}
	return c.JSON(fiber.Map{"ok": true, "restartRequired": true})
}

func (k *Kernel) handleStorageMigrate(c *fiber.Ctx) error {
	if err := k.verifyRequestOrigin(c); err != nil {
		return err
	}
	var in struct {
		Dir       string `json:"dir"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := c.BodyParser(&in); err != nil {
		return storageError(c, fiber.StatusBadRequest, "parse_failed", errors.New("参数解析失败"))
	}
	target, err := datadir.QueueMigrate(k.dataDir, in.Dir, in.Overwrite)
	if err != nil {
		status := fiber.StatusBadRequest
		code := "invalid_target"
		switch {
		case errors.Is(err, datadir.ErrSameDir), errors.Is(err, datadir.ErrNestedDir):
			code = "invalid_target"
		case errors.Is(err, datadir.ErrTargetNotEmpty):
			status = fiber.StatusConflict
			code = "not_empty"
		}
		return storageError(c, status, code, err)
	}
	return c.JSON(fiber.Map{"ok": true, "target": target, "restartRequired": true})
}

func (k *Kernel) handleStorageBrowse(c *fiber.Ctx) error {
	if err := k.verifyRequestOrigin(c); err != nil {
		return err
	}
	dir, err := platform.BrowseForFolder(k.dataDir)
	if err != nil {
		return storageError(c, fiber.StatusNotImplemented, "browse_unsupported", err)
	}
	return c.JSON(fiber.Map{"dir": dir})
}

func (k *Kernel) handleStorageCancel(c *fiber.Ctx) error {
	if err := k.verifyRequestOrigin(c); err != nil {
		return err
	}
	if err := datadir.CancelPending(); err != nil && !errors.Is(err, datadir.ErrNoPending) {
		return storageError(c, fiber.StatusInternalServerError, "cancel_failed", err)
	}
	return c.JSON(fiber.Map{"ok": true})
}

func storageError(c *fiber.Ctx, status int, code string, err error) error {
	return c.Status(status).JSON(fiber.Map{"error": err.Error(), "code": code})
}
