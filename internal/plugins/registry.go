// Package plugins 汇总所有内置插件。
package plugins

import (
	"devos/internal/plugin"
	"devos/internal/plugins/apitool"
	"devos/internal/plugins/db"
	notesapp "devos/internal/plugins/notes"
	"devos/internal/plugins/pluginmgr"
	"devos/internal/plugins/sftp"
	"devos/internal/plugins/ssh"
	"devos/internal/plugins/toolbox"
	"devos/internal/plugins/vault"
)

// All 返回随内核编译的内置插件列表。
func All() []plugin.Plugin {
	return []plugin.Plugin{
		sshapp.New(),
		sftpapp.New(),
		dbapp.New(),
		apitool.New(),
		notesapp.New(),
		toolbox.New(),
		vault.New(),
		pluginmgr.New(),
	}
}
