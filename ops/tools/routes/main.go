// Command routes prints every route the API server registers, one
// "METHOD /path" per line, sorted. It builds the router the server builds,
// without starting it, so the list is what gin actually serves.
package main

import (
	"fmt"
	"io"
	"sort"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/router"
)

func main() {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard
	engine := gin.New()
	router.SetRouter(engine)
	lines := make([]string, 0, len(engine.Routes()))
	for _, route := range engine.Routes() {
		lines = append(lines, route.Method+" "+route.Path)
	}
	sort.Strings(lines)
	for _, line := range lines {
		fmt.Println(line)
	}
}
