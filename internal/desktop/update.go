package desktop

import (
	"strings"

	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

// UpdateRepository 是自动更新使用的 GitHub 仓库（owner/repo）。
const UpdateRepository = "HiCodeOnline/DevOS"

// UpdateChecksumAsset 是发布流水线生成的校验和文件，用于下载后的摘要校验。
const UpdateChecksumAsset = "SHA256SUMS.txt"

// selectWindowsAsset 从 Release 资产中选出可直接替换当前进程的可执行文件。
// 发布包同时包含 zip 与裸 exe：Wails 的更新器要求归档内只有一个顶层条目，
// 而我们的 zip 还带 README/LICENSE，因此这里固定选择裸 exe。
func selectWindowsAsset(req updater.CheckRequest, assets []github.ReleaseAsset) int {
	arch := strings.ToLower(req.Arch)
	for i, asset := range assets {
		name := strings.ToLower(asset.Name)
		if !strings.HasSuffix(name, ".exe") || !strings.Contains(name, "windows") {
			continue
		}
		switch arch {
		case "amd64":
			if strings.Contains(name, "amd64") || strings.Contains(name, "x64") {
				return i
			}
		case "arm64":
			if strings.Contains(name, "arm64") || strings.Contains(name, "aarch64") {
				return i
			}
		default:
			if strings.Contains(name, arch) {
				return i
			}
		}
	}
	return -1
}
