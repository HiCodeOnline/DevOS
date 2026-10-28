// Package datadir 负责数据目录位置、备份归档以及目录迁移。
// 还原与迁移都排入队列，在下次启动时执行，避免运行中的 SQLite 连接被替换。
package datadir

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// BackupMarker 是备份包内的标记文件，用于拒绝非 DevOS 备份。
	BackupMarker = "devos-backup.json"
	// PendingRestoreFile 是待还原备份的暂存文件名。
	PendingRestoreFile = "pending-restore.zip"
	// PendingMigrateFile 是待迁移目标的暂存文件名。
	PendingMigrateFile = "pending-migrate.json"
	// BackupDirName 是自动备份目录名。
	BackupDirName = "backups"

	maxArchiveEntries = 200000
	maxArchiveBytes   = 8 << 30
)

var (
	ErrEmpty          = errors.New("请输入数据目录")
	ErrNotAbs         = errors.New("数据目录必须为绝对路径")
	ErrVolumeRoot     = errors.New("不能直接使用磁盘根目录")
	ErrMkdir          = errors.New("无法创建数据目录")
	ErrNotWritable    = errors.New("数据目录不可写")
	ErrSameDir        = errors.New("目标目录与当前数据目录相同")
	ErrNestedDir      = errors.New("目标目录不能位于当前数据目录内，也不能包含当前数据目录")
	ErrTargetNotEmpty = errors.New("目标目录非空，请选择空目录或确认覆盖")
	ErrInvalidArchive = errors.New("该文件不是有效的 DevOS 备份")
	ErrNoPending      = errors.New("没有等待执行的任务")
)

type location struct {
	DataDir string `json:"dataDir"`
	SavedAt string `json:"savedAt"`
}

type migrateJob struct {
	Target    string `json:"target"`
	Overwrite bool   `json:"overwrite"`
	SavedAt   string `json:"savedAt"`
}

// ConfigRoot 返回固定配置目录（存放 location.json 与待执行任务）。
func ConfigRoot() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "DevOS"), nil
}

// LocationPath 返回数据目录记录文件路径。
func LocationPath() (string, error) {
	root, err := ConfigRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "location.json"), nil
}

// WriteLocation 记录数据目录位置，供下次启动读取。
func WriteLocation(dir string) error {
	path, err := LocationPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(location{DataDir: dir, SavedAt: time.Now().Format(time.RFC3339)}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(payload, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Normalize 校验并创建用户指定的数据目录。
func Normalize(input string) (string, error) {
	dir := strings.TrimSpace(input)
	if dir == "" {
		return "", ErrEmpty
	}
	if !filepath.IsAbs(dir) {
		return "", ErrNotAbs
	}
	clean := filepath.Clean(dir)
	volume := filepath.VolumeName(clean)
	if volume != "" && (clean == volume || clean == volume+string(filepath.Separator)) {
		return "", ErrVolumeRoot
	}
	if err := os.MkdirAll(clean, 0o755); err != nil {
		return "", fmt.Errorf("%w: %v", ErrMkdir, err)
	}
	probe := filepath.Join(clean, ".devos-write-probe")
	if err := os.WriteFile(probe, []byte("devos"), 0o600); err != nil {
		return "", ErrNotWritable
	}
	_ = os.Remove(probe)
	return clean, nil
}

// DirStats 统计目录中的文件数量与总字节数，用于设置页展示。
func DirStats(dir string) (int, int64, error) {
	files := 0
	var bytes int64
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		files++
		bytes += info.Size()
		return nil
	})
	return files, bytes, err
}

// BackupsDir 返回自动备份目录。
func BackupsDir() (string, error) {
	root, err := ConfigRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, BackupDirName), nil
}

// Backup 将数据目录写入 zip 流。备份目录自身与待执行任务不会被包含，避免递归膨胀。
func Backup(w io.Writer, dir string) error {
	zw := zip.NewWriter(w)
	header := fmt.Sprintf(`{"app":"devos","createdAt":%q,"dataDir":%q}`,
		time.Now().Format(time.RFC3339), dir)
	if _, err := writeZipEntry(zw, BackupMarker, strings.NewReader(header)); err != nil {
		return err
	}
	excluded := exclusionSet()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil || rel == "." {
			return nil
		}
		if d.IsDir() {
			if excluded[filepath.Clean(path)] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if excluded[filepath.Clean(path)] {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		info, err := d.Info()
		if err != nil {
			return err
		}
		entry, err := createZipEntry(zw, filepath.ToSlash(rel), info)
		if err != nil {
			return err
		}
		_, err = io.Copy(entry, file)
		return err
	})
	if err != nil {
		_ = zw.Close()
		return err
	}
	return zw.Close()
}

// BackupToFile 将数据目录写入指定 zip 文件。
func BackupToFile(path, dir string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := Backup(file, dir); err != nil {
		file.Close()
		_ = os.Remove(path)
		return err
	}
	return file.Close()
}

// VerifyArchive 校验归档来自 DevOS 备份且条目安全。
func VerifyArchive(path string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return ErrInvalidArchive
	}
	defer reader.Close()
	marked := false
	entries := 0
	var total uint64
	for _, file := range reader.File {
		if !safeArchivePath(file.Name) {
			return ErrInvalidArchive
		}
		if file.Mode()&os.ModeSymlink != 0 {
			return ErrInvalidArchive
		}
		if file.Name == BackupMarker {
			marked = true
		}
		entries++
		total += file.UncompressedSize64
	}
	if !marked || entries <= 1 || entries > maxArchiveEntries || total > maxArchiveBytes {
		return ErrInvalidArchive
	}
	return nil
}

// ExtractArchive 将备份包解压到目标目录。
func ExtractArchive(path, dest string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return ErrInvalidArchive
	}
	defer reader.Close()
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	for _, file := range reader.File {
		if !safeArchivePath(file.Name) {
			return ErrInvalidArchive
		}
		// 标记文件只用于校验来源，不写入数据目录。
		if file.Name == BackupMarker {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(file.Name))
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if file.Mode()&os.ModeSymlink != 0 {
			return ErrInvalidArchive
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		mode := file.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			src.Close()
			return err
		}
		_, copyErr := io.Copy(out, src)
		src.Close()
		if closeErr := out.Close(); copyErr == nil {
			copyErr = closeErr
		}
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}

// CopyTree 递归复制目录，用于迁移数据。
func CopyTree(src, dst string) error {
	excluded := exclusionSet()
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			if rel != "." && excluded[filepath.Clean(path)] {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !d.Type().IsRegular() || excluded[filepath.Clean(path)] {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dst, rel)), 0o755); err != nil {
			return err
		}
		return copyFile(path, filepath.Join(dst, rel))
	})
}

// QueueRestore 校验备份并把上传文件转为待还原任务。
func QueueRestore(uploaded string) error {
	if err := VerifyArchive(uploaded); err != nil {
		return err
	}
	root, err := ConfigRoot()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	target := filepath.Join(root, PendingRestoreFile)
	_ = os.Remove(target)
	if err := os.Rename(uploaded, target); err != nil {
		// 跨卷上传时退化为复制。
		if copyErr := copyFile(uploaded, target); copyErr != nil {
			return copyErr
		}
		_ = os.Remove(uploaded)
	}
	return nil
}

// QueueMigrate 校验目标目录并写入待迁移任务。
func QueueMigrate(current, target string, overwrite bool) (string, error) {
	clean, err := Normalize(target)
	if err != nil {
		return "", err
	}
	currentClean := filepath.Clean(current)
	if strings.EqualFold(clean, currentClean) {
		return "", ErrSameDir
	}
	if insideDir(clean, currentClean) || insideDir(currentClean, clean) {
		return "", ErrNestedDir
	}
	if err := ensureEmpty(clean); err != nil {
		if !overwrite {
			return "", err
		}
	}
	root, err := ConfigRoot()
	if err != nil {
		return "", err
	}
	payload, err := json.MarshalIndent(migrateJob{
		Target:    clean,
		Overwrite: overwrite,
		SavedAt:   time.Now().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(root, PendingMigrateFile), append(payload, '\n'), 0o600); err != nil {
		return "", err
	}
	return clean, nil
}

// PendingState 返回是否存在待还原与待迁移任务及其目标。
func PendingState() (bool, string) {
	root, err := ConfigRoot()
	if err != nil {
		return false, ""
	}
	restore := false
	if info, err := os.Stat(filepath.Join(root, PendingRestoreFile)); err == nil && !info.IsDir() {
		restore = true
	}
	target := ""
	if data, err := os.ReadFile(filepath.Join(root, PendingMigrateFile)); err == nil {
		var job migrateJob
		if json.Unmarshal(data, &job) == nil {
			target = job.Target
		}
	}
	return restore, target
}

// CancelPending 清除待还原与待迁移任务。
func CancelPending() error {
	root, err := ConfigRoot()
	if err != nil {
		return err
	}
	removed := false
	for _, name := range []string{PendingRestoreFile, PendingMigrateFile} {
		if err := os.Remove(filepath.Join(root, name)); err == nil {
			removed = true
		}
	}
	if !removed {
		return ErrNoPending
	}
	return nil
}

// ApplyPending 在服务启动、插件初始化前执行待还原与待迁移任务。
// 返回本次启动实际使用的数据目录；任务失败时保留或标记队列文件并继续使用原目录。
func ApplyPending(log *slog.Logger, current string) string {
	root, err := ConfigRoot()
	if err != nil {
		log.Warn("无法读取配置目录，跳过待执行任务", "err", err)
		return current
	}
	current = applyRestore(log, root, current)
	return applyMigrate(log, root, current)
}

func applyRestore(log *slog.Logger, root, current string) string {
	archive := filepath.Join(root, PendingRestoreFile)
	if info, err := os.Stat(archive); err != nil || info.IsDir() {
		return current
	}
	log.Info("检测到待还原的备份", "archive", archive)
	if err := VerifyArchive(archive); err != nil {
		log.Error("备份文件无效，已跳过还原", "err", err)
		failPending(archive)
		return current
	}
	safetyDir, err := BackupsDir()
	if err != nil {
		log.Error("无法确定备份目录，已跳过还原", "err", err)
		return current
	}
	safety := filepath.Join(safetyDir, "devos-before-restore-"+time.Now().Format("20060102-150405")+".zip")
	if err := BackupToFile(safety, current); err != nil {
		log.Error("还原前自动备份失败，已取消还原", "err", err)
		failPending(archive)
		return current
	}
	stamp := time.Now().Format("20060102-150405")
	parent := filepath.Dir(current)
	base := filepath.Base(current)
	staging := filepath.Join(parent, "."+base+".restore-"+stamp)
	retired := filepath.Join(parent, "."+base+".old-"+stamp)
	_ = os.RemoveAll(staging)
	if err := ExtractArchive(archive, staging); err != nil {
		log.Error("解压备份失败，已取消还原", "err", err)
		_ = os.RemoveAll(staging)
		failPending(archive)
		return current
	}
	// 默认数据目录与配置目录相同，此时不能整体重命名，改为保留配置后替换内容。
	if samePath(current, root) {
		if err := clearDataDir(current); err != nil {
			log.Error("清理旧数据失败，已取消还原", "err", err)
			_ = os.RemoveAll(staging)
			failPending(archive)
			return current
		}
		if err := CopyTree(staging, current); err != nil {
			log.Error("写入还原数据失败，可从安全备份恢复", "err", err, "safetyBackup", safety)
			_ = os.RemoveAll(staging)
			failPending(archive)
			return current
		}
		_ = os.RemoveAll(staging)
		_ = os.Remove(archive)
		log.Info("数据还原完成", "safetyBackup", safety)
		return current
	}
	if err := os.Rename(current, retired); err != nil {
		log.Error("替换数据目录失败，已取消还原", "err", err)
		_ = os.RemoveAll(staging)
		failPending(archive)
		return current
	}
	if err := os.Rename(staging, current); err != nil {
		log.Error("启用还原数据失败，正在回滚", "err", err)
		_ = os.Rename(retired, current)
		_ = os.RemoveAll(staging)
		failPending(archive)
		return current
	}
	_ = os.RemoveAll(retired)
	_ = os.Remove(archive)
	log.Info("数据还原完成", "safetyBackup", safety)
	return current
}

// clearDataDir 在数据目录与配置目录重合时清空数据文件，保留目录记录、备份缓存与队列文件。
func clearDataDir(dir string) error {
	preserved := map[string]bool{
		"location.json":    true,
		PendingRestoreFile: true,
		PendingMigrateFile: true,
	}
	if backups, err := BackupsDir(); err == nil && samePath(filepath.Dir(backups), dir) {
		preserved[filepath.Base(backups)] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if preserved[entry.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func applyMigrate(log *slog.Logger, root, current string) string {
	jobFile := filepath.Join(root, PendingMigrateFile)
	data, err := os.ReadFile(jobFile)
	if err != nil {
		return current
	}
	var job migrateJob
	if err := json.Unmarshal(data, &job); err != nil || strings.TrimSpace(job.Target) == "" {
		log.Error("迁移任务无效，已跳过")
		failPending(jobFile)
		return current
	}
	target := filepath.Clean(job.Target)
	if strings.EqualFold(target, filepath.Clean(current)) || insideDir(target, current) || insideDir(current, target) {
		log.Error("迁移目标与当前数据目录冲突，已跳过", "target", target)
		failPending(jobFile)
		return current
	}
	log.Info("开始迁移数据目录", "from", current, "to", target)
	if err := os.MkdirAll(target, 0o755); err != nil {
		log.Error("无法创建迁移目标目录", "err", err)
		failPending(jobFile)
		return current
	}
	if err := CopyTree(current, target); err != nil {
		log.Error("迁移数据复制失败，继续使用原目录", "err", err)
		failPending(jobFile)
		return current
	}
	if err := WriteLocation(target); err != nil {
		log.Error("写入数据目录位置失败，继续使用原目录", "err", err)
		failPending(jobFile)
		return current
	}
	_ = os.Remove(jobFile)
	log.Info("数据迁移完成，原始目录可手动清理", "target", target)
	return target
}

func failPending(path string) {
	ext := filepath.Ext(path)
	if ext == ".zip" || ext == ".json" {
		_ = os.Rename(path, strings.TrimSuffix(path, ext)+".failed"+ext)
	}
}

func exclusionSet() map[string]bool {
	set := map[string]bool{}
	if dir, err := BackupsDir(); err == nil {
		set[filepath.Clean(dir)] = true
	}
	if root, err := ConfigRoot(); err == nil {
		set[filepath.Clean(filepath.Join(root, PendingRestoreFile))] = true
		set[filepath.Clean(filepath.Join(root, PendingMigrateFile))] = true
	}
	return set
}

func insideDir(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func ensureEmpty(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.Name() == ".devos-write-probe" {
			continue
		}
		return ErrTargetNotEmpty
	}
	return nil
}

func safeArchivePath(name string) bool {
	if name == "" || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
		return false
	}
	if filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

func writeZipEntry(zw *zip.Writer, name string, r io.Reader) (int64, error) {
	entry, err := zw.Create(name)
	if err != nil {
		return 0, err
	}
	return io.Copy(entry, r)
}

func createZipEntry(zw *zip.Writer, name string, info fs.FileInfo) (io.Writer, error) {
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return nil, err
	}
	header.Name = name
	header.Method = zip.Deflate
	return zw.CreateHeader(header)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
