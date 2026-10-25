package common

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/errors"
)

func GetIp() (ip string) {
	ips, err := net.InterfaceAddrs()
	if err != nil {
		log.Println(err)
		return ip
	}

	for _, a := range ips {
		if ipNet, ok := a.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
			if ipNet.IP.To4() != nil {
				ip = ipNet.IP.String()
				if strings.HasPrefix(ip, "10") {
					return
				}
				if strings.HasPrefix(ip, "172") {
					return
				}
				if strings.HasPrefix(ip, "192.168") {
					return
				}
				ip = ""
			}
		}
	}
	return
}

func GetNetworkIps() []string {
	var networkIps []string
	ips, err := net.InterfaceAddrs()
	if err != nil {
		log.Println(err)
		return networkIps
	}

	for _, a := range ips {
		if ipNet, ok := a.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
			if ipNet.IP.To4() != nil {
				ip := ipNet.IP.String()
				// Include common private network ranges
				if strings.HasPrefix(ip, "10.") ||
					strings.HasPrefix(ip, "172.") ||
					strings.HasPrefix(ip, "192.168.") {
					networkIps = append(networkIps, ip)
				}
			}
		}
	}
	return networkIps
}

// IsRunningInContainer detects if the application is running inside a container
func IsRunningInContainer() bool {
	// Method 1: Check for .dockerenv file (Docker containers)
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}

	// Method 2: Check cgroup for container indicators
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		content := string(data)
		if strings.Contains(content, "docker") ||
			strings.Contains(content, "containerd") ||
			strings.Contains(content, "kubepods") ||
			strings.Contains(content, "/lxc/") {
			return true
		}
	}

	// Method 3: Check environment variables commonly set by container runtimes
	containerEnvVars := []string{
		"KUBERNETES_SERVICE_HOST",
		"DOCKER_CONTAINER",
		"container",
	}

	for _, envVar := range containerEnvVars {
		if os.Getenv(envVar) != "" {
			return true
		}
	}

	// Method 4: Check if init process is not the traditional init
	if data, err := os.ReadFile("/proc/1/comm"); err == nil {
		comm := strings.TrimSpace(string(data))
		// In containers, process 1 is often not "init" or "systemd"
		if comm != "init" && comm != "systemd" {
			// Additional check: if it's a common container entrypoint
			if strings.Contains(comm, "docker") ||
				strings.Contains(comm, "containerd") ||
				strings.Contains(comm, "runc") {
				return true
			}
		}
	}

	return false
}

var sizeKB = 1024
var sizeMB = sizeKB * 1024
var sizeGB = sizeMB * 1024

func Interface2String(inter interface{}) string {
	switch inter.(type) {
	case string:
		return inter.(string)
	case int:
		return fmt.Sprintf("%d", inter.(int))
	case float64:
		return strconv.FormatFloat(inter.(float64), 'f', -1, 64)
	case bool:
		if inter.(bool) {
			return "true"
		} else {
			return "false"
		}
	case nil:
		return ""
	}
	return fmt.Sprintf("%v", inter)
}

func IsIP(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil
}

func GetUUID() string {
	code := uuid.New().String()
	code = strings.Replace(code, "-", "", -1)
	return code
}

const keyChars = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

func init() {
	rand.New(rand.NewSource(time.Now().UnixNano()))
}

func GenerateRandomCharsKey(length int) (string, error) {
	b := make([]byte, length)
	maxI := big.NewInt(int64(len(keyChars)))

	for i := range b {
		n, err := crand.Int(crand.Reader, maxI)
		if err != nil {
			return "", err
		}
		b[i] = keyChars[n.Int64()]
	}

	return string(b), nil
}

func GenerateRandomKey(length int) (string, error) {
	bytes := make([]byte, length*3/4) // 对于48位的输出，这里应该是36
	if _, err := crand.Read(bytes); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(bytes), nil
}

func GenerateKey() (string, error) {
	key, err := GenerateRandomCharsKey(48)
	if err != nil {
		return "", err
	}
	return APITokenPrefix + key, nil
}

func GetRandomInt(max int) int {
	//rand.Seed(time.Now().UnixNano())
	return rand.Intn(max)
}

func GetTimestamp() int64 {
	return time.Now().Unix()
}

func Max(a int, b int) int {
	if a >= b {
		return a
	} else {
		return b
	}
}

func GetPointer[T any](v T) *T {
	return &v
}

func Any2Type[T any](data any) (T, error) {
	var zero T
	bytes, err := json.Marshal(data)
	if err != nil {
		return zero, err
	}
	var res T
	err = json.Unmarshal(bytes, &res)
	if err != nil {
		return zero, err
	}
	return res, nil
}

// GetAudioDuration returns the duration of an audio file in seconds.
func GetAudioDuration(ctx context.Context, filename string, ext string) (float64, error) {
	// ffprobe -v error -show_entries format=duration -of default=noprint_wrappers=1:nokey=1 {{input}}
	c := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", filename)
	output, err := c.Output()
	if err != nil {
		return 0, errors.Wrap(err, "failed to get audio duration")
	}
	durationStr := string(bytes.TrimSpace(output))
	if durationStr == "N/A" {
		// Create a temporary output file name
		tmpFp, err := os.CreateTemp("", "audio-*"+ext)
		if err != nil {
			return 0, errors.Wrap(err, "failed to create temporary file")
		}
		tmpName := tmpFp.Name()
		// Close immediately so ffmpeg can open the file on Windows.
		_ = tmpFp.Close()
		defer os.Remove(tmpName)

		// ffmpeg -y -i filename -vcodec copy -acodec copy <tmpName>
		ffmpegCmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", filename, "-vcodec", "copy", "-acodec", "copy", tmpName)
		if err := ffmpegCmd.Run(); err != nil {
			return 0, errors.Wrap(err, "failed to run ffmpeg")
		}

		// Recalculate the duration of the new file
		c = exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", tmpName)
		output, err := c.Output()
		if err != nil {
			return 0, errors.Wrap(err, "failed to get audio duration after ffmpeg")
		}
		durationStr = string(bytes.TrimSpace(output))
	}
	return strconv.ParseFloat(durationStr, 64)
}
