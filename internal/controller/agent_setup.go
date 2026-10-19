package controller

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/agentsetup"
	"github.com/lazu-ai/lazu/internal/setting/system_setting"
)

// agentSetupBaseURL is the Lazu origin the scripts write into agent configs.
// The configured server address wins; a deployment that never set one falls
// back to the host the request reached, which is what the console shows too.
//
// config.yaml ships `server_address: http://localhost:3000`, so a deployment
// that never set PUBLIC_SERVER_ADDRESS still has a syntactically valid
// address. Writing that into an installer would point the agent at port 3000
// on the developer's own machine, so a loopback address is only trusted when
// the request itself came from loopback.
func agentSetupBaseURL(c *gin.Context) (string, bool) {
	configured, err := agentsetup.NormalizeBaseURL(system_setting.ServerAddress)
	if err == nil && (!isLoopbackURL(configured) || isLoopbackHost(requestHost(c))) {
		return configured, true
	}
	base, err := agentsetup.NormalizeBaseURL(requestScheme(c) + "://" + requestHost(c))
	return base, err == nil
}

// firstForwarded reads a header a chain of proxies may have appended to
// ("https, http"); the leftmost value is the one the client actually used.
func firstForwarded(c *gin.Context, name string) string {
	value := c.GetHeader(name)
	if comma := strings.IndexByte(value, ','); comma >= 0 {
		value = value[:comma]
	}
	return strings.TrimSpace(value)
}

// requestScheme is what the client typed, not what reached this process: a
// TLS terminator in front speaks plain HTTP to us and says so in a header.
func requestScheme(c *gin.Context) string {
	if forwarded := firstForwarded(c, "X-Forwarded-Proto"); forwarded != "" {
		if strings.EqualFold(forwarded, "https") {
			return "https"
		}
		if strings.EqualFold(forwarded, "http") {
			return "http"
		}
	}
	if c.Request.TLS != nil {
		return "https"
	}
	return "http"
}

// requestHost keeps the port the client used, which a proxy that rewrites
// Host can only tell us through X-Forwarded-Host.
func requestHost(c *gin.Context) string {
	if forwarded := firstForwarded(c, "X-Forwarded-Host"); forwarded != "" {
		return forwarded
	}
	return c.Request.Host
}

func isLoopbackURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return isLoopbackHost(parsed.Host)
}

func isLoopbackHost(host string) bool {
	name := host
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		name = parsed
	}
	name = strings.Trim(strings.ToLower(name), "[]")
	if name == "localhost" || strings.HasSuffix(name, ".localhost") {
		return true
	}
	// Every spelling of the loopback range, not the two or three that get
	// typed most often: 127.0.0.0/8 has sixteen million addresses and ::1 can
	// be written out in full.
	if ip := net.ParseIP(name); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// GetAgentSetupScript serves GET /setup/:tool (POSIX sh) and /setup/:tool.ps1
// (PowerShell). The response is plain text meant to be piped into a shell.
func GetAgentSetupScript(c *gin.Context) {
	tool, shell, ok := agentsetup.ParseName(c.Param("tool"))
	if !ok {
		c.String(http.StatusNotFound, "unknown setup script; available: %s\n", strings.Join(agentsetup.Tools, ", "))
		return
	}
	base, ok := agentSetupBaseURL(c)
	if !ok {
		c.String(http.StatusInternalServerError, "server address is not a valid http(s) URL\n")
		return
	}
	script, err := agentsetup.Render(tool, shell, base)
	if err != nil {
		c.String(http.StatusInternalServerError, "could not render setup script\n")
		return
	}
	c.Header("X-Content-Type-Options", "nosniff")
	// A request-derived base URL must not be shared through a cache.
	if configured, err := agentsetup.NormalizeBaseURL(system_setting.ServerAddress); err == nil && configured == base {
		c.Header("Cache-Control", "public, max-age=300")
	} else {
		c.Header("Cache-Control", "no-store")
	}
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(script))
}
