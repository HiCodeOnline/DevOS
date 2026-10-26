// Package sftpapp 提供 Xftp 风格的双栏文件管理应用。
package sftpapp

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/pkg/sftp"

	"devos/internal/plugin"
	"devos/internal/profile"
	"devos/internal/sshtools"
)

//go:embed web
var webFS embed.FS

type SFTP struct {
	store *profile.Store
	log   *slog.Logger
	jobs  *jobStore
}

func New() *SFTP { return &SFTP{} }

func (p *SFTP) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:          "sftp",
		Name:        "SFTP 文件",
		Description: "双栏文件管理、传输队列、权限与在线编辑",
		Version:     "0.2.0",
		Category:    "开发者",
		Icon:        `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M3 8a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8Z"/><path d="M8 14h8"/><path d="M13 11l3 3-3 3"/></svg>`,
		Accent:      "#10b981",
		Entry:       "index.html",
		Width:       1180,
		Height:      760,
		Multi:       false,
		Builtin:     true,
		Backend:     true,
	}
}

func (p *SFTP) Init(ctx *plugin.Context) error {
	store, err := profile.Open(ctx.RootDir)
	if err != nil {
		return err
	}
	p.store = store
	p.log = ctx.Logger
	p.jobs = newJobStore()
	return nil
}

func (p *SFTP) Assets() fs.FS {
	sub, _ := fs.Sub(webFS, "web")
	return sub
}

func (p *SFTP) Register(api fiber.Router) {
	api.Get("/profiles", func(c *fiber.Ctx) error {
		return c.JSON(p.store.List())
	})

	api.Post("/profiles", func(c *fiber.Ctx) error {
		var in profile.Profile
		if err := c.BodyParser(&in); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
		}
		if in.Host == "" || in.User == "" {
			return fiber.NewError(fiber.StatusBadRequest, "主机与用户名不能为空")
		}
		saved, err := p.store.Save(in)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		return c.JSON(saved)
	})

	api.Get("/list", func(c *fiber.Ctx) error {
		dir := cleanRemote(c.Query("path", "/"))
		var entries []entry
		err := p.withClient(c.Query("profile"), func(cl *sftp.Client) error {
			list, err := cl.ReadDir(dir)
			if err != nil {
				return err
			}
			entries = make([]entry, 0, len(list))
			for _, fi := range list {
				entries = append(entries, entry{
					Name:    fi.Name(),
					Size:    fi.Size(),
					Mode:    fi.Mode().String(),
					ModTime: fi.ModTime().Format(time.RFC3339),
					IsDir:   fi.IsDir(),
				})
			}
			sortEntries(entries)
			return nil
		})
		if err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		return c.JSON(fiber.Map{"path": dir, "entries": entries})
	})

	api.Get("/local/home", func(c *fiber.Ctx) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return c.JSON(fiber.Map{"path": "drives"})
		}
		return c.JSON(fiber.Map{"path": home})
	})

	api.Get("/local/list", func(c *fiber.Ctx) error {
		dir, entries, err := listLocal(c.Query("path"))
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		return c.JSON(fiber.Map{"path": dir, "entries": entries})
	})

	api.Post("/mkdir", func(c *fiber.Ctx) error {
		var in struct {
			Profile string `json:"profile"`
			Path    string `json:"path"`
		}
		if err := c.BodyParser(&in); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
		}
		target := cleanRemote(in.Path)
		err := p.withClient(in.Profile, func(cl *sftp.Client) error { return cl.Mkdir(target) })
		if err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		return c.JSON(fiber.Map{"ok": true, "path": target})
	})

	api.Post("/remove", func(c *fiber.Ctx) error {
		var in struct {
			Profile   string   `json:"profile"`
			Paths     []string `json:"paths"`
			Recursive bool     `json:"recursive"`
		}
		if err := c.BodyParser(&in); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
		}
		err := p.withClient(in.Profile, func(cl *sftp.Client) error {
			for _, raw := range in.Paths {
				target := cleanRemote(raw)
				st, err := cl.Stat(target)
				if err != nil {
					return fmt.Errorf("%s: %w", target, err)
				}
				if !st.IsDir() {
					if err := cl.Remove(target); err != nil {
						return fmt.Errorf("%s: %w", target, err)
					}
					continue
				}
				if in.Recursive {
					if err := removeTree(cl, target); err != nil {
						return err
					}
					continue
				}
				if err := cl.RemoveDirectory(target); err != nil {
					return fmt.Errorf("%s: %w", target, err)
				}
			}
			return nil
		})
		if err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		return c.JSON(fiber.Map{"ok": true})
	})

	api.Post("/rename", func(c *fiber.Ctx) error {
		var in struct {
			Profile string `json:"profile"`
			From    string `json:"from"`
			To      string `json:"to"`
		}
		if err := c.BodyParser(&in); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
		}
		from, to := cleanRemote(in.From), cleanRemote(in.To)
		if err := p.withClient(in.Profile, func(cl *sftp.Client) error { return cl.Rename(from, to) }); err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		return c.JSON(fiber.Map{"ok": true, "from": from, "to": to})
	})

	api.Post("/chmod", func(c *fiber.Ctx) error {
		var in struct {
			Profile string `json:"profile"`
			Path    string `json:"path"`
			Mode    string `json:"mode"`
		}
		if err := c.BodyParser(&in); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
		}
		mode, err := strconv.ParseUint(strings.TrimSpace(in.Mode), 8, 32)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "权限格式应为八进制，例如 644")
		}
		target := cleanRemote(in.Path)
		if err := p.withClient(in.Profile, func(cl *sftp.Client) error {
			return cl.Chmod(target, os.FileMode(mode))
		}); err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		return c.JSON(fiber.Map{"ok": true})
	})

	api.Get("/read", func(c *fiber.Ctx) error {
		const limit = 2 * 1024 * 1024
		target := cleanRemote(c.Query("path"))
		profileID := c.Query("profile")
		client, err := p.connect(profileID)
		if err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		defer client.Close()

		file, err := client.Open(target)
		if err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		defer file.Close()
		st, err := file.Stat()
		if err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		if st.Size() > limit {
			return fiber.NewError(fiber.StatusBadRequest, "文件超过 2MB，无法在线编辑")
		}
		content, err := io.ReadAll(io.LimitReader(file, limit))
		if err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		binary := strings.IndexByte(string(content), 0) >= 0
		if binary {
			content = nil
		}
		return c.JSON(fiber.Map{"content": string(content), "size": st.Size(), "binary": binary})
	})

	api.Post("/write", func(c *fiber.Ctx) error {
		var in struct {
			Profile string `json:"profile"`
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := c.BodyParser(&in); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
		}
		target := cleanRemote(in.Path)
		err := p.withClient(in.Profile, func(cl *sftp.Client) error {
			file, err := cl.Create(target)
			if err != nil {
				return err
			}
			defer file.Close()
			_, err = file.Write([]byte(in.Content))
			return err
		})
		if err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		return c.JSON(fiber.Map{"ok": true})
	})

	api.Post("/upload", func(c *fiber.Ctx) error {
		profileID := c.FormValue("profile")
		dir := cleanRemote(c.FormValue("path", "/"))
		file, err := c.FormFile("file")
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "未收到文件")
		}
		src, err := file.Open()
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		defer src.Close()

		target := path.Join(dir, file.Filename)
		err = p.withClient(profileID, func(cl *sftp.Client) error {
			dst, err := cl.Create(target)
			if err != nil {
				return err
			}
			defer dst.Close()
			_, err = io.Copy(dst, src)
			return err
		})
		if err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		return c.JSON(fiber.Map{"ok": true, "path": target})
	})

	api.Get("/download", func(c *fiber.Ctx) error {
		profileID := c.Query("profile")
		target := cleanRemote(c.Query("path"))
		client, err := p.connect(profileID)
		if err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		defer client.Close()

		f, err := client.Open(target)
		if err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		name := path.Base(target)
		c.Set("Content-Type", "application/octet-stream")
		c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
		return c.SendStream(f, int(st.Size()))
	})

	api.Post("/transfer", func(c *fiber.Ctx) error {
		var in struct {
			Profile   string         `json:"profile"`
			Direction string         `json:"direction"`
			TargetDir string         `json:"targetDir"`
			Items     []transferItem `json:"items"`
		}
		if err := c.BodyParser(&in); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
		}
		if len(in.Items) == 0 {
			return fiber.NewError(fiber.StatusBadRequest, "请选择要传输的文件")
		}
		if in.Direction != "upload" && in.Direction != "download" {
			return fiber.NewError(fiber.StatusBadRequest, "传输方向不正确")
		}
		prof, ok := p.store.Get(in.Profile)
		if !ok {
			return fiber.NewError(fiber.StatusBadRequest, "会话不存在")
		}
		job := p.jobs.create(in.Direction)
		go p.runTransfer(prof, p.resolveJump(prof), job.ID, in.Direction, in.TargetDir, in.Items)
		return c.JSON(fiber.Map{"ok": true, "job": job})
	})

	api.Get("/transfer/:id", func(c *fiber.Ctx) error {
		job, ok := p.jobs.snapshot(c.Params("id"))
		if !ok {
			return fiber.NewError(fiber.StatusNotFound, "任务不存在")
		}
		return c.JSON(job)
	})
}

// ---------- 类型 ----------

type entry struct {
	Name    string `json:"name"`
	Path    string `json:"path,omitempty"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	ModTime string `json:"modTime"`
	IsDir   bool   `json:"isDir"`
}

type transferItem struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	IsDir bool   `json:"isDir"`
}

type Job struct {
	ID         string `json:"id"`
	Direction  string `json:"direction"`
	Status     string `json:"status"` // running | done | error
	Current    string `json:"current"`
	Error      string `json:"error,omitempty"`
	TotalFiles int    `json:"totalFiles"`
	DoneFiles  int    `json:"doneFiles"`
	TotalBytes int64  `json:"totalBytes"`
	DoneBytes  int64  `json:"doneBytes"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt,omitempty"`
}

type jobStore struct {
	mu   sync.Mutex
	jobs map[string]*Job
}

func newJobStore() *jobStore {
	return &jobStore{jobs: map[string]*Job{}}
}

func (s *jobStore) create(direction string) Job {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 控制内存占用：任务累积超过 80 个时清理已结束的旧任务。
	if len(s.jobs) > 80 {
		for id, job := range s.jobs {
			if job.Status != "running" {
				delete(s.jobs, id)
			}
		}
	}
	id := fmt.Sprintf("t%d", time.Now().UnixNano())
	job := &Job{ID: id, Direction: direction, Status: "running", StartedAt: time.Now().Format(time.RFC3339)}
	s.jobs[id] = job
	return *job
}

func (s *jobStore) set(id string, fn func(*Job)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job, ok := s.jobs[id]; ok {
		fn(job)
	}
}

func (s *jobStore) addBytes(id string, n int64) {
	s.set(id, func(j *Job) { j.DoneBytes += n })
}

func (s *jobStore) snapshot(id string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return Job{}, false
	}
	return *job, true
}

func (s *jobStore) finish(id string, err error) {
	s.set(id, func(j *Job) {
		if err != nil {
			j.Status = "error"
			j.Error = err.Error()
		} else {
			j.Status = "done"
			j.Current = ""
		}
		j.FinishedAt = time.Now().Format(time.RFC3339)
	})
}

// ---------- 传输 ----------

type transferTask struct {
	src  string
	dst  string
	size int64
}

func (p *SFTP) runTransfer(prof profile.Profile, jump *profile.Profile, jobID, direction, targetDir string, items []transferItem) {
	client, err := p.connectProfiles(prof, jump)
	if err != nil {
		p.jobs.finish(jobID, err)
		return
	}
	defer client.Close()

	var (
		files []transferTask
		dirs  []string
	)
	if direction == "upload" {
		files, dirs, err = expandUpload(targetDir, items)
	} else {
		files, dirs, err = expandDownload(client, targetDir, items)
	}
	if err != nil {
		p.jobs.finish(jobID, err)
		return
	}

	var totalBytes int64
	for _, f := range files {
		totalBytes += f.size
	}
	p.jobs.set(jobID, func(j *Job) {
		j.TotalFiles = len(files)
		j.TotalBytes = totalBytes
	})

	buf := make([]byte, 256*1024)
	for _, dir := range dirs {
		if direction == "upload" {
			if err := client.MkdirAll(dir); err != nil {
				p.jobs.finish(jobID, err)
				return
			}
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			p.jobs.finish(jobID, err)
			return
		}
	}

	for _, task := range files {
		p.jobs.set(jobID, func(j *Job) { j.Current = task.src })
		if direction == "upload" {
			err = uploadFile(client, task, func(n int64) { p.jobs.addBytes(jobID, n) }, buf)
		} else {
			err = downloadFile(client, task, func(n int64) { p.jobs.addBytes(jobID, n) }, buf)
		}
		if err != nil {
			p.jobs.finish(jobID, err)
			return
		}
		p.jobs.set(jobID, func(j *Job) { j.DoneFiles++ })
	}
	p.jobs.finish(jobID, nil)
}

func expandUpload(targetDir string, items []transferItem) ([]transferTask, []string, error) {
	var files []transferTask
	var dirs []string
	for _, item := range items {
		info, err := os.Stat(item.Path)
		if err != nil {
			return nil, nil, err
		}
		name := item.Name
		if name == "" {
			name = filepath.Base(item.Path)
		}
		root := path.Join(cleanRemote(targetDir), name)
		if !info.IsDir() {
			files = append(files, transferTask{src: item.Path, dst: root, size: info.Size()})
			continue
		}
		dirs = append(dirs, root)
		err = filepath.WalkDir(item.Path, func(localPath string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(item.Path, localPath)
			if err != nil {
				return err
			}
			if rel == "." {
				return nil
			}
			dst := path.Join(root, filepath.ToSlash(rel))
			if d.IsDir() {
				dirs = append(dirs, dst)
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return err
			}
			files = append(files, transferTask{src: localPath, dst: dst, size: fi.Size()})
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	return files, dirs, nil
}

func expandDownload(client *sftp.Client, targetDir string, items []transferItem) ([]transferTask, []string, error) {
	var files []transferTask
	var dirs []string
	for _, item := range items {
		src := cleanRemote(item.Path)
		info, err := client.Stat(src)
		if err != nil {
			return nil, nil, err
		}
		name := item.Name
		if name == "" {
			name = path.Base(src)
		}
		root := filepath.Join(targetDir, name)
		if !info.IsDir() {
			files = append(files, transferTask{src: src, dst: root, size: info.Size()})
			continue
		}
		dirs = append(dirs, root)
		err = walkRemote(client, src, func(remotePath string, fi os.FileInfo) error {
			rel, err := filepath.Rel(src, remotePath)
			if err != nil {
				return err
			}
			if rel == "." {
				return nil
			}
			dst := filepath.Join(root, rel)
			if fi.IsDir() {
				dirs = append(dirs, dst)
				return nil
			}
			files = append(files, transferTask{src: remotePath, dst: dst, size: fi.Size()})
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	return files, dirs, nil
}

type countWriter struct {
	w     io.Writer
	total int64
	onAdd func(int64)
}

func (c *countWriter) Write(b []byte) (int, error) {
	n, err := c.w.Write(b)
	if n > 0 {
		c.total += int64(n)
		if c.onAdd != nil {
			c.onAdd(int64(n))
		}
	}
	return n, err
}

func uploadFile(client *sftp.Client, task transferTask, onAdd func(int64), buf []byte) error {
	if err := client.MkdirAll(path.Dir(task.dst)); err != nil {
		return err
	}
	src, err := os.Open(task.src)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := client.Create(task.dst)
	if err != nil {
		return err
	}
	if _, err := io.CopyBuffer(&countWriter{w: dst, onAdd: onAdd}, src, buf); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}

func downloadFile(client *sftp.Client, task transferTask, onAdd func(int64), buf []byte) error {
	if err := os.MkdirAll(filepath.Dir(task.dst), 0o755); err != nil {
		return err
	}
	src, err := client.Open(task.src)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(task.dst)
	if err != nil {
		return err
	}
	if _, err := io.CopyBuffer(&countWriter{w: dst, onAdd: onAdd}, src, buf); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}

// ---------- 辅助 ----------

func (p *SFTP) connect(profileID string) (*sftp.Client, error) {
	prof, ok := p.store.Get(profileID)
	if !ok {
		return nil, fmt.Errorf("会话不存在")
	}
	return p.connectProfiles(prof, p.resolveJump(prof))
}

func (p *SFTP) connectProfiles(prof profile.Profile, jump *profile.Profile) (*sftp.Client, error) {
	sshClient, err := sshtools.DialVia(prof, jump)
	if err != nil {
		return nil, err
	}
	client, err := sftp.NewClient(sshClient)
	if err != nil {
		sshClient.Close()
		return nil, err
	}
	return client, nil
}

func (p *SFTP) resolveJump(prof profile.Profile) *profile.Profile {
	if prof.JumpProfileID == "" {
		return nil
	}
	jump, ok := p.store.Get(prof.JumpProfileID)
	if !ok || jump.ID == prof.ID {
		return nil
	}
	return &jump
}

func (p *SFTP) withClient(profileID string, fn func(*sftp.Client) error) error {
	client, err := p.connect(profileID)
	if err != nil {
		return err
	}
	defer client.Close()
	return fn(client)
}

func cleanRemote(raw string) string {
	if raw == "" {
		return "/"
	}
	return path.Clean("/" + strings.TrimPrefix(raw, "/"))
}

func sortEntries(entries []entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
}

func walkRemote(client *sftp.Client, root string, fn func(string, os.FileInfo) error) error {
	fi, err := client.Stat(root)
	if err != nil {
		return err
	}
	if err := fn(root, fi); err != nil {
		return err
	}
	if !fi.IsDir() {
		return nil
	}
	list, err := client.ReadDir(root)
	if err != nil {
		return err
	}
	for _, child := range list {
		if err := walkRemote(client, path.Join(root, child.Name()), fn); err != nil {
			return err
		}
	}
	return nil
}

func removeTree(client *sftp.Client, root string) error {
	var dirs []string
	err := walkRemote(client, root, func(remotePath string, fi os.FileInfo) error {
		if fi.IsDir() {
			dirs = append(dirs, remotePath)
			return nil
		}
		return client.Remove(remotePath)
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := client.RemoveDirectory(dirs[i]); err != nil {
			return err
		}
	}
	return nil
}

func listLocal(raw string) (string, []entry, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "drives" {
		return "drives", localDrives(), nil
	}
	dir := filepath.Clean(raw)
	if len(dir) == 2 && dir[1] == ':' {
		dir += string(os.PathSeparator)
	}
	list, err := os.ReadDir(dir)
	if err != nil {
		return dir, nil, err
	}
	entries := make([]entry, 0, len(list))
	for _, item := range list {
		info, err := item.Info()
		if err != nil {
			continue
		}
		entries = append(entries, entry{
			Name:    item.Name(),
			Path:    filepath.Join(dir, item.Name()),
			Size:    info.Size(),
			Mode:    info.Mode().String(),
			ModTime: info.ModTime().Format(time.RFC3339),
			IsDir:   item.IsDir(),
		})
	}
	sortEntries(entries)
	return dir, entries, nil
}

func localDrives() []entry {
	var out []entry
	for c := 'A'; c <= 'Z'; c++ {
		root := string(c) + ":\\"
		st, err := os.Stat(root)
		if err != nil || !st.IsDir() {
			continue
		}
		out = append(out, entry{
			Name:    string(c) + ":",
			Path:    root,
			Mode:    "dr-xr-xr-x",
			ModTime: st.ModTime().Format(time.RFC3339),
			IsDir:   true,
		})
	}
	return out
}
