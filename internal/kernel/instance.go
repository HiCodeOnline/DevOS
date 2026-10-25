package kernel

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Listen 从 basePort 开始尝试监听，返回实际监听器与端口。
func Listen(basePort, tries int) (net.Listener, int, error) {
	if tries <= 0 {
		tries = 20
	}
	var lastErr error
	for i := 0; i < tries; i++ {
		port := basePort + i
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			return ln, port, nil
		}
		lastErr = err
	}
	return nil, 0, fmt.Errorf("在端口 %d~%d 上均无法监听: %w", basePort, basePort+tries-1, lastErr)
}

// WaitReady 轮询健康检查，等待服务就绪。
func WaitReady(baseURL string, timeout time.Duration) bool {
	client := &http.Client{Timeout: 300 * time.Millisecond}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/api/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(80 * time.Millisecond)
	}
	return false
}

// FindDevOS 扫描端口段，若已有 DevOS 实例在运行则返回其地址。
func FindDevOS(basePort, tries int) (string, bool) {
	if tries <= 0 {
		tries = 20
	}
	client := &http.Client{Timeout: 300 * time.Millisecond}
	for i := 0; i < tries; i++ {
		url := fmt.Sprintf("http://127.0.0.1:%d", basePort+i)
		resp, err := client.Get(url + "/api/health")
		if err != nil {
			continue
		}
		var body struct {
			App string `json:"app"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK && body.App == "devos" {
			return url, true
		}
	}
	return "", false
}
