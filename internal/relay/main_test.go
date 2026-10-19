package relay

import (
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

// gin.SetMode writes package-level state in gin. Several tests here run in
// parallel and each used to set it, so the race detector saw concurrent writes
// to the same variable. The mode is the same for every test in this package,
// so set it once before any of them starts.
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}
