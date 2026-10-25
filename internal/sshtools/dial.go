// Package sshtools 提供 SSH / SFTP / 跳板机共用的连接能力。
package sshtools

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"

	"devos/internal/profile"
)

// Dial 建立直连。
func Dial(p profile.Profile) (*ssh.Client, error) {
	return DialVia(p, nil)
}

// DialVia 建立连接，可选经由跳板机（ProxyJump）。
func DialVia(p profile.Profile, jump *profile.Profile) (*ssh.Client, error) {
	cfg, err := clientConfig(p)
	if err != nil {
		return nil, err
	}

	if jump == nil || jump.Host == "" || jump.ID == p.ID {
		return ssh.Dial("tcp", addr(p.Host, p.Port), cfg)
	}

	jumpCfg, err := clientConfig(*jump)
	if err != nil {
		return nil, fmt.Errorf("跳板机配置错误: %w", err)
	}
	jumpClient, err := ssh.Dial("tcp", addr(jump.Host, jump.Port), jumpCfg)
	if err != nil {
		return nil, fmt.Errorf("连接跳板机失败: %w", err)
	}

	conn, err := jumpClient.Dial("tcp", addr(p.Host, p.Port))
	if err != nil {
		jumpClient.Close()
		return nil, fmt.Errorf("跳板机转发失败: %w", err)
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr(p.Host, p.Port), cfg)
	if err != nil {
		conn.Close()
		jumpClient.Close()
		return nil, err
	}
	client := ssh.NewClient(c, chans, reqs)
	go func() {
		// 跳板机连接随目标连接一起释放
		_ = jumpClient.Wait()
		client.Close()
		jumpClient.Close()
	}()
	return client, nil
}

func clientConfig(p profile.Profile) (*ssh.ClientConfig, error) {
	var auths []ssh.AuthMethod
	switch p.Auth {
	case "key":
		var (
			signer ssh.Signer
			err    error
		)
		if p.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(p.PrivateKey), []byte(p.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(p.PrivateKey))
		}
		if err != nil {
			return nil, fmt.Errorf("解析私钥失败: %w", err)
		}
		auths = append(auths, ssh.PublicKeys(signer))
	default:
		if p.Password == "" {
			return nil, errors.New("缺少登录密码")
		}
		pw := p.Password
		auths = append(auths, ssh.Password(pw))
		auths = append(auths, ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
			answers := make([]string, len(questions))
			for i := range answers {
				answers[i] = pw
			}
			return answers, nil
		}))
	}

	port := p.Port
	if port == 0 {
		port = 22
	}
	return &ssh.ClientConfig{
		User:            p.User,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // 原型阶段跳过主机指纹校验
		Timeout:         10 * time.Second,
	}, nil
}

func addr(host string, port int) string {
	if port == 0 {
		port = 22
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// StartKeepAlive 周期性发送保活请求，连接异常时主动关闭连接。
func StartKeepAlive(client *ssh.Client, every time.Duration) func() {
	if every <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); err != nil {
					client.Close()
					return
				}
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

// Shell 描述一个已分配 PTY 的交互式会话。
type Shell struct {
	Session *ssh.Session
	Stdin   io.WriteCloser
	Stdout  io.Reader
}

// StartShell 申请 PTY 并启动交互式 Shell。startup 为按目标编码转换后的启动命令。
func StartShell(client *ssh.Client, termType string, cols, rows int, startup []byte) (*Shell, error) {
	sess, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("创建会话失败: %w", err)
	}
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := sess.RequestPty(termType, rows, cols, modes); err != nil {
		sess.Close()
		return nil, fmt.Errorf("申请终端失败: %w", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	if err := sess.Shell(); err != nil {
		sess.Close()
		return nil, fmt.Errorf("启动 Shell 失败: %w", err)
	}
	if len(startup) > 0 {
		go func() {
			time.Sleep(280 * time.Millisecond)
			_, _ = stdin.Write(append(startup, '\n'))
		}()
	}
	return &Shell{Session: sess, Stdin: stdin, Stdout: stdout}, nil
}

// LookupEncoding 按名称返回字符编码，UTF-8 返回 nil。
func LookupEncoding(name string) encoding.Encoding {
	switch name {
	case "", "utf-8", "utf8", "UTF-8":
		return nil
	case "gbk", "GBK", "cp936":
		return simplifiedchinese.GBK
	case "gb18030", "GB18030":
		return simplifiedchinese.GB18030
	case "big5", "BIG5":
		return traditionalchinese.Big5
	case "euc-jp", "EUC-JP":
		return japanese.EUCJP
	case "shift-jis", "sjis", "SHIFT_JIS":
		return japanese.ShiftJIS
	default:
		return nil
	}
}

// ExitCode 从会话等待结果中提取退出码。
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		return exit.ExitStatus()
	}
	return -1
}
